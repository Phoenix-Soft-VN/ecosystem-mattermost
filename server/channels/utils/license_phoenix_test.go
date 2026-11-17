// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package utils

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost/server/public/model"
)

// Phoenix experiment regression tests: ValidateLicense ignores the RSA signature and the
// service environment, but still rejects malformed envelopes and non-object payloads.

func phoenixEnvelope(payload []byte, signature []byte) []byte {
	raw := append(append([]byte{}, payload...), signature...)
	return []byte(base64.StdEncoding.EncodeToString(raw))
}

func TestPhoenixValidateLicenseAcceptsInvalidSignature(t *testing.T) {
	payload := `{"id":"phoenixtestlicense0000000001","features":{"users":5}}`
	signed := phoenixEnvelope([]byte(payload), bytes.Repeat([]byte{'A'}, 256))

	str, err := LicenseValidator.ValidateLicense(signed)
	require.NoError(t, err)
	require.Equal(t, payload, str)

	license, appErr := LicenseValidator.LicenseFromBytes(signed)
	require.Nil(t, appErr)
	require.NotNil(t, license)
	require.Equal(t, "phoenixtestlicense0000000001", license.Id)
	require.NotNil(t, license.Features)
	require.NotNil(t, license.Features.Users)
	require.Equal(t, 5, *license.Features.Users)
}

func TestPhoenixValidateLicenseIgnoresServiceEnvironment(t *testing.T) {
	for _, environment := range []string{model.ServiceEnvironmentProduction, model.ServiceEnvironmentTest} {
		t.Run(environment, func(t *testing.T) {
			// t.Setenv prevents t.Parallel; the service environment has no config equivalent.
			t.Setenv("MM_SERVICEENVIRONMENT", environment)

			str, err := LicenseValidator.ValidateLicense(validTestLicense)
			require.NoError(t, err)
			require.NotEmpty(t, str)
			require.NotErrorIs(t, err, ErrLicenseTestInProductionEnvironment)
			require.NotErrorIs(t, err, ErrLicenseProductionInTestEnvironment)
		})
	}
}

// A genuine signature may end in 0x00. The decoder must keep exactly the final 256 bytes as the
// signature and must not trim bytes from the envelope.
func TestPhoenixValidateLicensePreservesTrailingZeroSignatureByte(t *testing.T) {
	payload := `{"id":"phoenixtestlicense0000000001","features":{"users":5}}`
	signature := append(bytes.Repeat([]byte{'A'}, 255), 0x00)
	require.Len(t, signature, 256)
	signed := phoenixEnvelope([]byte(payload), signature)

	str, err := LicenseValidator.ValidateLicense(signed)
	require.NoError(t, err)
	require.Equal(t, payload, str)

	license, appErr := LicenseValidator.LicenseFromBytes(signed)
	require.Nil(t, appErr)
	require.Equal(t, "phoenixtestlicense0000000001", license.Id)
}

func TestPhoenixValidateLicenseRejectsMalformed(t *testing.T) {
	signature := bytes.Repeat([]byte{'A'}, 256)

	cases := map[string]struct {
		input  []byte
		reason string
	}{
		"nil":                          {nil, "not long enough"},
		"junk":                         {[]byte("junk"), "not long enough"},
		"not base64":                   {[]byte("!!!not base64!!!"), "encountered error decoding license"},
		"truncated 255 bytes":          {phoenixEnvelope(bytes.Repeat([]byte{'A'}, 255), nil), "not long enough"},
		"256 bytes exactly":            {phoenixEnvelope(bytes.Repeat([]byte{'A'}, 256), nil), "not long enough"},
		"256 null bytes":               {phoenixEnvelope(make([]byte, 256), nil), "not long enough"},
		"truncated JSON":               {phoenixEnvelope([]byte(`{"id":`), signature), "not valid JSON"},
		"JSON array payload":           {phoenixEnvelope([]byte(`[1,2]`), signature), "not valid JSON"},
		"JSON null payload":            {phoenixEnvelope([]byte(`null`), signature), "not a JSON object"},
		"JSON string payload":          {phoenixEnvelope([]byte(`"abc"`), signature), "not valid JSON"},
		"valid JSON without signature": {phoenixEnvelope([]byte(`{"id":"phoenixtestlicense0000000001"}`), nil), "not long enough"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			str, err := LicenseValidator.ValidateLicense(tc.input)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.reason)
			require.Empty(t, str)

			license, appErr := LicenseValidator.LicenseFromBytes(tc.input)
			require.Nil(t, license)
			require.NotNil(t, appErr)
		})
	}
}
