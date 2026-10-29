package service

import (
	"net"
	"strings"
)

// HTTPSRedirectURL builds the https:// URL for a request that arrived over plain HTTP, keeping the
// host the client used (IPv6 literals stay bracketed) and pointing at the configured HTTPS port.
func HTTPSRedirectURL(requestHost, tlsPort, requestURI string) string {
	host := requestHost
	if h, _, err := net.SplitHostPort(requestHost); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")

	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}

	if tlsPort != "" && tlsPort != "443" {
		host += ":" + tlsPort
	}

	return "https://" + host + requestURI
}
