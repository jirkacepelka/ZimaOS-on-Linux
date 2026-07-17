package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortalOptions(t *testing.T) {
	on := portalOptions(true)
	for _, want := range []string{"'autostart': <true>", "'commandline': <['zima-connect', '--background']>", "handle_token"} {
		if !strings.Contains(on, want) {
			t.Errorf("enabled options missing %q in %q", want, on)
		}
	}

	off := portalOptions(false)
	if !strings.Contains(off, "'autostart': <false>") {
		t.Errorf("disabled options missing autostart<false>: %q", off)
	}
	if strings.Contains(off, "commandline") {
		t.Errorf("disabled options should not carry a commandline: %q", off)
	}
}

// TestXDGFileRoundTrip writes and removes the autostart entry in an isolated
// XDG config dir, verifying the file lifecycle without touching the real home.
func TestXDGFileRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	if err := setViaXDGFile(true, "/usr/bin/zima-connect --background"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	path := filepath.Join(tmp, "autostart", desktopFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected autostart file: %v", err)
	}
	if !strings.Contains(string(data), "Exec=/usr/bin/zima-connect --background") {
		t.Errorf("Exec line missing from entry:\n%s", data)
	}
	if !Enabled() {
		t.Error("Enabled() should be true after writing the entry")
	}

	if err := setViaXDGFile(false, ""); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("autostart file should be gone after disable")
	}

	// Disabling again must be a no-op, not an error.
	if err := setViaXDGFile(false, ""); err != nil {
		t.Errorf("second disable should be a no-op: %v", err)
	}
}
