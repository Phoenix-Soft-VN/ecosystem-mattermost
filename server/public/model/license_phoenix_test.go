// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPhoenixLicenseDoesNotAdvertiseNonProduction(t *testing.T) {
	license := NewPermissiveLicense()
	require.False(t, license.IsNonProduction)
	require.Equal(t, LicenseShortSkuEnterpriseAdvanced, license.SkuShortName)

	raw, err := json.Marshal(license)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	require.Equal(t, false, fields["is_non_production"])
}
