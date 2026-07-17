// Package config handles persistence of the user's Zima Connect settings.
//
// Everything lives in a single JSON file under the XDG config directory
// (~/.config/zima-connect/config.json). We keep it deliberately small: the
// only thing the user is asked for is the ZimaOS "Remote ID" (a ZeroTier
// network ID), everything else is derived at runtime.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// networkIDPattern matches a ZeroTier network ID: 16 hexadecimal characters.
var networkIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// Config is the persisted application state.
type Config struct {
	// NetworkID is the ZeroTier network ID that ZimaOS shows under
	// Network -> Remote Login. This is what the UI calls the "Remote ID".
	NetworkID string `json:"network_id"`

	// ZimaHost, when set, pins the ZimaOS address on the ZeroTier network
	// (host or host:port). When empty the connection layer auto-discovers it.
	ZimaHost string `json:"zima_host,omitempty"`

	// Autostart records whether the user asked the app to launch on login.
	Autostart bool `json:"autostart"`

	path string
	mu   sync.Mutex
}

// NormalizeNetworkID trims formatting the user is likely to paste (spaces,
// uppercase, a leading "0x") and validates the result.
func NormalizeNetworkID(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "0x")
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return "", errors.New("network ID is empty")
	}
	if !networkIDPattern.MatchString(s) {
		return "", errors.New("network ID must be 16 hexadecimal characters")
	}
	return s, nil
}

// Dir returns the directory where the config file lives, creating it.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "zima-connect")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Load reads the config file, returning a zero-value Config (not an error) when
// the file does not yet exist.
func Load() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.json")
	c := &Config{path: path}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, err
	}
	c.path = path
	return c, nil
}

// Save writes the config atomically (write to a temp file, then rename).
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}
