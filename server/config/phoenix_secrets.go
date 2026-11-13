// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork.

package config

import (
	"errors"
	"io"
	"os"
	"strings"
)

// LoadPhoenixDatasourceSecret implements the single allowlisted Compose secret-file input.
// Resolve it before native environment parsing, which otherwise treats the pathname as a DSN.
// Errors deliberately contain neither the path, secret content nor underlying driver error.
func LoadPhoenixDatasourceSecret() error {
	const fileKey = "MM_SQLSETTINGS_DATASOURCE_FILE"
	const valueKey = "MM_SQLSETTINGS_DATASOURCE"
	path, set := os.LookupEnv(fileKey)
	if !set {
		return nil
	}
	invalid := errors.New("invalid datasource secret-file configuration")
	if path == "" || os.Getenv(valueKey) != "" {
		return invalid
	}
	f, err := os.Open(path)
	if err != nil {
		return invalid
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 {
		return invalid
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return invalid
	}
	if err := os.Setenv(valueKey, value); err != nil {
		return invalid
	}
	return os.Unsetenv(fileKey)
}
