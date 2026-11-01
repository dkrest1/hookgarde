package security

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Policy re-resolves and validates every connection, then dials the validated IP.
// TLS still verifies the original URL hostname. No environment proxy is used.
var ErrDNSUnavailable = errors.New("destination DNS unavailable")

type Policy struct {
	DevelopmentHosts []string
	Lookup           func(context.Context, string) ([]net.IPAddr, error)
}

var blocked = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/3"), netip.MustParsePrefix("::/96"), netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"), netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("fe80::/10"), netip.MustParsePrefix("ff00::/8")}

func Public(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if a.Is6() && !netip.MustParsePrefix("2000::/3").Contains(a) {
		return false
	}
	if !a.IsGlobalUnicast() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(a) {
			return false
		}
	}
	return true
}
func (p Policy) dev(host string) bool {
	for _, h := range p.DevelopmentHosts {
		if host == strings.TrimSpace(h) {
			return true
		}
	}
	return false
}
func (p Policy) parse(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 2048 || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("invalid destination URL")
	}
	if p.dev(u.Hostname()) {
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("invalid development scheme")
		}
	} else if u.Scheme != "https" || (u.Port() != "" && u.Port() != "443") {
		return nil, fmt.Errorf("destination must use HTTPS port 443")
	}
	return u, nil
}
func (p Policy) resolve(ctx context.Context, host string) ([]net.IPAddr, error) {
	lookup := p.Lookup
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	ips, e := lookup(ctx, host)
	if e != nil || len(ips) == 0 {
		return nil, ErrDNSUnavailable
	}
	if !p.dev(host) {
		for _, ip := range ips {
			if !Public(ip.IP) {
				return nil, fmt.Errorf("destination resolves to a prohibited address")
			}
		}
	}
	return ips, nil
}
func (p Policy) Validate(ctx context.Context, raw string) error {
	u, e := p.parse(raw)
	if e != nil {
		return e
	}
	_, e = p.resolve(ctx, u.Hostname())
	return e
}
func (p Policy) Client() *http.Client {
	tr := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 5 * time.Second, DisableKeepAlives: true, MaxResponseHeaderBytes: 32 * 1024, ForceAttemptHTTP2: false}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		if !p.dev(host) && port != "443" {
			return nil, fmt.Errorf("prohibited port")
		}
		ips, e := p.resolve(ctx, host)
		if e != nil {
			return nil, e
		}
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
