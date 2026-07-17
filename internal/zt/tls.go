package zt

import "crypto/tls"

// insecureTLS returns a TLS config that skips certificate verification. It is
// used ONLY by the discovery probe, which reads the public landing page of hosts
// on the private ZeroTier network to identify ZimaOS. No credentials are sent
// during probing. Actual browsing happens in the user's real browser with its
// normal certificate handling.
func insecureTLS() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true} // #nosec G402 - identity probe only
}
