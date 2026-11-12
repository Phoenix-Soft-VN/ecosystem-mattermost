// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// KeySet resolves a Gateway RS256 verification key by key id. It never takes a key location
// from a token: the JWKS URL is fixed server-side configuration.
type KeySet interface {
	Key(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// ReplayStore consumes one-time jti values.
type ReplayStore interface {
	ConsumeReplay(ctx context.Context, kind, key string, expiresAtMS int64) (bool, error)
}

// JWKSSource fetches and caches a JWKS document from one configured HTTPS URL.
type JWKSSource struct {
	URL    string
	Client *http.Client
	TTL    time.Duration
	Now    func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	lastTry   time.Time
}

// NewJWKSSource builds a source; caFile (optional) adds a trust anchor for lab/internal TLS.
func NewJWKSSource(url, caFile string) (*JWKSSource, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("no certificate in PHOENIX CA bundle")
		}
		tlsCfg.RootCAs = pool
	}
	return &JWKSSource{URL: url, TTL: 5 * time.Minute, Now: time.Now,
		Client: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsCfg}}}, nil
}

func (j *JWKSSource) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.Now()
	if k, ok := j.keys[kid]; ok && now.Sub(j.fetchedAt) < j.TTL {
		return k, nil
	}
	// A missing kid forces a refetch for rotation, but at most once per 10 seconds.
	if j.keys != nil && now.Sub(j.lastTry) < 10*time.Second {
		if k, ok := j.keys[kid]; ok {
			return k, nil
		}
		return nil, errors.New("unknown signing key")
	}
	j.lastTry = now
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := j.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks fetch status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, err
	}
	keys, err := ParseJWKS(body)
	if err != nil {
		return nil, err
	}
	j.keys, j.fetchedAt = keys, now
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, errors.New("unknown signing key")
}

// ParseJWKS reads RSA signature keys only; other key types and uses are ignored.
func ParseJWKS(doc []byte) (map[string]*rsa.PublicKey, error) {
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(doc, &set); err != nil {
		return nil, err
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != "RS256") || k.Kid == "" {
			continue
		}
		nb, err1 := base64.RawURLEncoding.DecodeString(k.N)
		eb, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(nb) < 256 { // reject keys below 2048 bits
			continue
		}
		e := int(new(big.Int).SetBytes(eb).Int64())
		if e < 3 {
			continue
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}
	}
	if len(out) == 0 {
		return nil, errors.New("jwks has no usable RS256 key")
	}
	return out, nil
}

// Caller is the verified internal service caller.
type Caller struct {
	ID     string
	Scopes map[string]bool
	JTI    string
}

// CallerVerifier validates the C2 internal assertion: RS256 only, exact issuer and
// audience, required exp/iat/jti/sub, lifetime <= 60s, one-time jti.
type CallerVerifier struct {
	Issuer   string
	Audience string
	Keys     KeySet
	Replay   ReplayStore
	Now      func() time.Time
	Leeway   time.Duration
}

func (v *CallerVerifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// VerifyBearer authenticates an Authorization header value.
func (v *CallerVerifier) VerifyBearer(ctx context.Context, header string) (*Caller, *Error) {
	invalid := func(cause error) *Error {
		return wrapErr(http.StatusUnauthorized, CodeAuthInvalid, "invalid caller assertion", cause)
	}
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return nil, invalid(errors.New("missing bearer"))
	}
	raw := strings.TrimSpace(header[len(prefix):])
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(v.Issuer),
		jwt.WithAudience(v.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(v.now),
		jwt.WithLeeway(v.Leeway),
	)
	tok, err := parser.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("kid required")
		}
		return v.Keys.Key(ctx, kid)
	})
	if err != nil || !tok.Valid {
		return nil, invalid(err)
	}
	iat, err := claims.GetIssuedAt()
	if err != nil || iat == nil {
		return nil, invalid(errors.New("iat required"))
	}
	exp, _ := claims.GetExpirationTime()
	if exp.Sub(iat.Time) > MaxAssertionLife*time.Second {
		return nil, invalid(errors.New("assertion lifetime exceeds 60s"))
	}
	sub, _ := claims.GetSubject()
	jti, _ := claims["jti"].(string)
	if sub == "" || jti == "" || len(jti) > 255 {
		return nil, invalid(errors.New("sub and jti required"))
	}
	scopes := map[string]bool{}
	if s, ok := claims["scope"].(string); ok {
		for _, f := range strings.Fields(s) {
			scopes[f] = true
		}
	}
	if list, ok := claims["scp"].([]any); ok {
		for _, f := range list {
			if s, ok := f.(string); ok {
				scopes[s] = true
			}
		}
	}
	fresh, rerr := v.Replay.ConsumeReplay(ctx, "internal-assertion", sub+":"+jti, exp.Add(time.Minute).UnixMilli())
	if rerr != nil {
		return nil, wrapErr(http.StatusServiceUnavailable, CodeNotReady, "replay store unavailable", rerr)
	}
	if !fresh {
		return nil, invalid(errors.New("assertion replay"))
	}
	return &Caller{ID: sub, Scopes: scopes, JTI: jti}, nil
}
