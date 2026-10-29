package service_test

import (
	"testing"

	"github.com/IceWhaleTech/CasaOS-Gateway/service"
)

func TestHTTPSRedirectURL(t *testing.T) {
	cases := []struct {
		host, port, uri, want string
	}{
		{"192.168.1.10", "443", "/a?b=1", "https://192.168.1.10/a?b=1"},
		{"192.168.1.10:80", "443", "/", "https://192.168.1.10/"},
		{"nas.local:8080", "8443", "/x", "https://nas.local:8443/x"},
		{"[::1]:80", "443", "/", "https://[::1]/"},
		{"[fe80::1]", "8443", "/", "https://[fe80::1]:8443/"},
		{"nas.local", "", "/", "https://nas.local/"},
	}
	for _, c := range cases {
		if got := service.HTTPSRedirectURL(c.host, c.port, c.uri); got != c.want {
			t.Errorf("HTTPSRedirectURL(%q,%q,%q) = %q, want %q", c.host, c.port, c.uri, got, c.want)
		}
	}
}
