// Copyright (c) Phoenix-Soft-VN. Licensed under the surrounding fork terms.
package api4

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/pem"
	"github.com/mattermost/mattermost/server/v8/channels/app"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
	"github.com/stretchr/testify/require"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// Mutation: drop saga avatar attributes, remove origin/version fence, or omit native image invalidation.
func TestPhoenixAvatarConvergence(t *testing.T) {
	th := Setup(t).InitBasic(t)
	adapter := app.NewPhoenixNative(th.App)
	ctx := context.Background()
	before, _, err := th.App.GetProfileImage(th.BasicUser)
	require.Nil(t, err)
	red := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			red.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, red))
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/avatar/redirect" {
			http.Redirect(w, r, "https://foreign.invalid/avatar/a", 302)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(encoded.Bytes())
	}))
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "avatar-ca.pem")
	require.NoError(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600))
	t.Setenv("PHOENIX_GATEWAY_URL", server.URL)
	t.Setenv("PHOENIX_CA_FILE", ca)
	url := server.URL + "/avatar/owned?v=2&s=unit"
	version := int64(2)
	require.NoError(t, adapter.UpdateAccountProfile(ctx, th.BasicUser.Id, "", phoenix.Profile{AvatarURL: &url, AvatarVersion: &version}))
	user, e := th.App.GetUser(th.Context, th.BasicUser.Id)
	require.Nil(t, e)
	require.Equal(t, "2", user.Props["phoenix_avatar_version"])
	data, _, e := th.App.GetProfileImage(user)
	require.Nil(t, e)
	require.NotEqual(t, sha256.Sum256(before), sha256.Sum256(data))
	decoded, _, e2 := image.Decode(bytes.NewReader(data))
	require.NoError(t, e2)
	r, g, b, _ := decoded.At(decoded.Bounds().Dx()/2, decoded.Bounds().Dy()/2).RGBA()
	require.EqualValues(t, 65535, r)
	require.Zero(t, g)
	require.Zero(t, b)
	version = 1
	require.NoError(t, adapter.UpdateAccountProfile(ctx, user.Id, "", phoenix.Profile{AvatarURL: &url, AvatarVersion: &version}))
	require.EqualValues(t, 1, calls.Load(), "stale version must not fetch or restore old avatar")
	version = 3
	foreign := "https://foreign.invalid/avatar/owned"
	require.Error(t, adapter.UpdateAccountProfile(ctx, user.Id, "", phoenix.Profile{AvatarURL: &foreign, AvatarVersion: &version}))
	require.EqualValues(t, 1, calls.Load())
	redirect := server.URL + "/avatar/redirect"
	require.Error(t, adapter.UpdateAccountProfile(ctx, user.Id, "", phoenix.Profile{AvatarURL: &redirect, AvatarVersion: &version}))
	require.EqualValues(t, 2, calls.Load())
	clear := ""
	require.NoError(t, adapter.UpdateAccountProfile(ctx, user.Id, "", phoenix.Profile{AvatarURL: &clear, AvatarVersion: &version}))
	user, e = th.App.GetUser(th.Context, user.Id)
	require.Nil(t, e)
	require.Equal(t, "3", user.Props["phoenix_avatar_version"])
	after, _, e := th.App.GetProfileImage(user)
	require.Nil(t, e)
	require.Equal(t, sha256.Sum256(before), sha256.Sum256(after))
}
