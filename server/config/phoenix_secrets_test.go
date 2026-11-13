package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// Mutation: parsing the _FILE pathname as the DSN instead of reading the secret.
func TestPhoenixDatasourceSecret(t *testing.T) {
	file := filepath.Join(t.TempDir(), "datasource")
	const dsn = "postgres://fixture:private@db:5432/owned?sslmode=disable"
	for _, tc := range []struct {
		name, content, inline string
		good                  bool
	}{
		{"valid", dsn + "\n", "", true}, {"empty", "\n", "", false},
		{"conflict", dsn, "postgres://other", false}, {"oversize", string(make([]byte, 65537)), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MM_SQLSETTINGS_DATASOURCE_FILE", file)
			t.Setenv("MM_SQLSETTINGS_DATASOURCE", tc.inline)
			require.NoError(t, os.WriteFile(file, []byte(tc.content), 0600))
			err := LoadPhoenixDatasourceSecret()
			if !tc.good {
				require.Error(t, err)
				require.NotContains(t, err.Error(), dsn)
				return
			}
			require.NoError(t, err)
			cfg := &model.Config{}
			cfg.SetDefaults()
			applied := applyEnvironmentMap(cfg, GetEnvironment())
			require.Equal(t, dsn, *applied.SqlSettings.DataSource)
			require.Empty(t, os.Getenv("MM_SQLSETTINGS_DATASOURCE_FILE"))
		})
	}
	t.Setenv("MM_SQLSETTINGS_DATASOURCE_FILE", file+"-missing")
	t.Setenv("MM_SQLSETTINGS_DATASOURCE", "")
	require.EqualError(t, LoadPhoenixDatasourceSecret(), "invalid datasource secret-file configuration")
}
