package zt

import (
	"os"
	"path/filepath"
)

// storageDir is where the embedded libzt engine persists its ZeroTier node
// identity, so the device keeps a stable address across restarts. It lives
// beside the app config. (Used only by the libzt build; harmless otherwise.)
func storageDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "zima-connect", "zerotier")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}
