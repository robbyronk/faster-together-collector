// Package auth handles device-flow login and persistence of the resulting
// bearer token on disk. The plaintext token is stored once at issue time;
// only the collector ever reads it.
package auth

import (
	"errors"
	"os"
	"path/filepath"
)

// DefaultTokenPath returns the per-platform path where the bearer token is
// stored: `<UserConfigDir>/fh-collector/token`.
//
// On macOS this is `~/Library/Application Support/fh-collector/token`; on
// Linux it follows XDG ($XDG_CONFIG_HOME or ~/.config); on Windows it's
// %AppData%.
func DefaultTokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "fh-collector", "token"), nil
}

// ReadToken returns the plaintext bearer token from `path`, or an empty
// string + nil error if the file does not exist.
func ReadToken(path string) (string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	// Trim trailing newline introduced by some editors.
	for len(bytes) > 0 && (bytes[len(bytes)-1] == '\n' || bytes[len(bytes)-1] == '\r') {
		bytes = bytes[:len(bytes)-1]
	}
	return string(bytes), nil
}

// WriteToken writes `plaintext` to `path` with mode 0600, creating parent
// directories as needed.
func WriteToken(path, plaintext string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(plaintext), 0o600)
}

// DeleteToken removes the token file. It is not an error if it doesn't exist.
func DeleteToken(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
