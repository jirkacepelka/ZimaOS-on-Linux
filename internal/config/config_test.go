package config

import "testing"

func TestNormalizeNetworkID(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"8056c2e21c000001", "8056c2e21c000001", false},
		{"  8056C2E21C000001  ", "8056c2e21c000001", false}, // trim + lowercase
		{"0x8056c2e21c000001", "8056c2e21c000001", false},   // strip 0x
		{"8056 c2e2 1c00 0001", "8056c2e21c000001", false},  // strip spaces
		{"", "", true},                  // empty
		{"8056c2e21c00000", "", true},   // 15 chars
		{"8056c2e21c0000011", "", true}, // 17 chars
		{"8056c2e21c00zzzz", "", true},  // non-hex
	}
	for _, c := range cases {
		got, err := NormalizeNetworkID(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("NormalizeNetworkID(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeNetworkID(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeNetworkID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
