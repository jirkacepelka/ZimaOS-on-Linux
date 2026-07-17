package zt

import (
	"net"
	"testing"
)

func TestFirstIPv4(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"10.147.20.5/24"}, "10.147.20.5/24"},
		{[]string{"fd80:56c2::1/88", "10.147.20.5/24"}, "10.147.20.5/24"}, // skip IPv6
		{[]string{"fd80:56c2::1/88"}, ""},                                 // no IPv4
		{nil, ""},
	}
	for _, c := range cases {
		if got := firstIPv4(c.in); got != c.want {
			t.Errorf("firstIPv4(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIPOnly(t *testing.T) {
	if got := ipOnly("10.147.20.5/24"); got != "10.147.20.5" {
		t.Errorf("ipOnly = %q, want 10.147.20.5", got)
	}
	if got := ipOnly(""); got != "" {
		t.Errorf("ipOnly(empty) = %q, want empty", got)
	}
}

func TestNormalizeZimaURL(t *testing.T) {
	cases := map[string]string{
		"10.147.20.5":        "http://10.147.20.5",
		"10.147.20.5:8080":   "http://10.147.20.5:8080",
		"http://10.147.20.5": "http://10.147.20.5",
		"https://zima.local": "https://zima.local",
		"  10.147.20.5  ":    "http://10.147.20.5",
		"":                   "",
	}
	for in, want := range cases {
		if got := normalizeZimaURL(in); got != want {
			t.Errorf("normalizeZimaURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostsInSubnet(t *testing.T) {
	_, ipNet, _ := net.ParseCIDR("192.168.1.10/24")
	self := net.ParseIP("192.168.1.10")
	hosts := hostsInSubnet(ipNet, self, 1024)

	// /24 has 254 usable hosts, minus this machine's own address = 253.
	if len(hosts) != 253 {
		t.Fatalf("got %d hosts, want 253", len(hosts))
	}
	for _, h := range hosts {
		switch h {
		case "192.168.1.0":
			t.Error("network address should be excluded")
		case "192.168.1.255":
			t.Error("broadcast address should be excluded")
		case "192.168.1.10":
			t.Error("self address should be excluded")
		}
	}

	// The cap must be honoured.
	capped := hostsInSubnet(ipNet, self, 10)
	if len(capped) != 10 {
		t.Errorf("cap not honoured: got %d, want 10", len(capped))
	}
}
