package zt

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// firstIPv4 returns the first IPv4 address (in CIDR form, e.g. "10.147.20.5/24")
// from a list of ZeroTier-assigned addresses, or "" if there is none.
func firstIPv4(addrs []string) string {
	for _, a := range addrs {
		ip, _, err := net.ParseCIDR(a)
		if err != nil {
			continue
		}
		if ip.To4() != nil {
			return a
		}
	}
	return ""
}

// ipOnly strips the mask from a CIDR ("10.147.20.5/24" -> "10.147.20.5").
func ipOnly(cidr string) string {
	if cidr == "" {
		return ""
	}
	if i := strings.IndexByte(cidr, '/'); i >= 0 {
		return cidr[:i]
	}
	return cidr
}

// normalizeZimaURL turns a bare host, host:port, or full URL into a canonical
// ZimaOS web URL. ZimaOS serves its UI over plain HTTP on the ZeroTier network
// by default, so we assume http:// when no scheme is given.
func normalizeZimaURL(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host
	}
	return "http://" + host
}

// discoverZima scans the ZeroTier subnet described by assignedCIDR looking for
// the ZimaOS server. It probes every host in the subnet concurrently and
// returns the URL of the best match: a host that identifies as ZimaOS/CasaOS is
// preferred, otherwise the first host answering on the web port.
//
// This is intentionally best-effort. If the user pins ZimaHost in the config we
// never get here.
func discoverZima(ctx context.Context, assignedCIDR string) string {
	self, ipNet, err := net.ParseCIDR(assignedCIDR)
	if err != nil || self.To4() == nil {
		return ""
	}

	hosts := hostsInSubnet(ipNet, self, 1024)
	if len(hosts) == 0 {
		return ""
	}

	scanCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	type result struct {
		url    string
		strong bool // matched a ZimaOS/CasaOS signature
	}

	results := make(chan result, len(hosts))
	sem := make(chan struct{}, 64) // bound concurrency
	var wg sync.WaitGroup

	for _, host := range hosts {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-scanCtx.Done():
				return
			}
			if url, strong, ok := probeZima(scanCtx, ip); ok {
				results <- result{url: url, strong: strong}
			}
		}(host)
	}

	go func() { wg.Wait(); close(results) }()

	var fallback string
	for r := range results {
		if r.strong {
			return r.url // definitive match, stop early
		}
		if fallback == "" {
			fallback = r.url
		}
	}
	return fallback
}

// probeZima checks whether ip serves a ZimaOS-like web UI. It returns the URL,
// whether it strongly matched a signature, and whether it answered at all.
func probeZima(ctx context.Context, ip string) (url string, strong bool, ok bool) {
	for _, scheme := range []string{"http", "https"} {
		u := scheme + "://" + ip
		body, headers, ok2 := httpPeek(ctx, u)
		if !ok2 {
			continue
		}
		sig := strings.ToLower(body + " " + headers)
		if strings.Contains(sig, "zima") || strings.Contains(sig, "casaos") || strings.Contains(sig, "icewhale") {
			return u, true, true
		}
		// Answered, but no signature — remember as a weak candidate.
		return u, false, true
	}
	return "", false, false
}

// httpPeek fetches the first chunk of a URL with a tight timeout. It never
// follows into long downloads and tolerates self-signed TLS (ZimaOS local certs).
func httpPeek(ctx context.Context, url string) (body, headers string, ok bool) {
	cctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()

	client := &http.Client{
		Timeout: 2500 * time.Millisecond,
		Transport: &http.Transport{
			// ZimaOS local HTTPS uses a self-signed cert; we are only sniffing
			// for identity here, not transferring sensitive data.
			TLSClientConfig: insecureTLS(),
			DialContext:     (&net.Dialer{Timeout: 1500 * time.Millisecond}).DialContext,
		},
		// Do not follow redirects; the landing page is enough to identify.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", false
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", false
	}
	defer resp.Body.Close()

	var hb strings.Builder
	for k, v := range resp.Header {
		hb.WriteString(k)
		hb.WriteByte(':')
		hb.WriteString(strings.Join(v, ","))
		hb.WriteByte(' ')
	}
	chunk, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	return string(chunk), hb.String(), true
}

// hostsInSubnet enumerates usable host addresses in ipNet, skipping the network
// and broadcast addresses and this machine's own address. It caps the count so
// a misconfigured huge subnet cannot stall discovery.
func hostsInSubnet(ipNet *net.IPNet, self net.IP, max int) []string {
	var out []string
	ip := ipNet.IP.Mask(ipNet.Mask).To4()
	if ip == nil {
		return out
	}
	selfV4 := self.To4()
	for cur := cloneIP(ip); ipNet.Contains(cur); incIP(cur) {
		if isNetworkOrBroadcast(cur, ipNet) || cur.Equal(selfV4) {
			continue
		}
		out = append(out, cur.String())
		if len(out) >= max {
			break
		}
	}
	return out
}

func isNetworkOrBroadcast(ip net.IP, ipNet *net.IPNet) bool {
	network := ipNet.IP.Mask(ipNet.Mask).To4()
	if network == nil || ip.To4() == nil {
		return false
	}
	if ip.Equal(network) {
		return true
	}
	// Broadcast: network | ^mask
	bcast := make(net.IP, len(network))
	for i := range network {
		bcast[i] = network[i] | ^ipNet.Mask[i]
	}
	return ip.Equal(bcast)
}

func cloneIP(ip net.IP) net.IP {
	c := make(net.IP, len(ip))
	copy(c, ip)
	return c
}

func incIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}
