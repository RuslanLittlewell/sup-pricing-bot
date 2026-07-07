package security

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
)

var privateIPBlocks []*net.IPNet

func init() {
	for _, cidr := range []string{
		"127.0.0.0/8",    // loopback
		"10.0.0.0/8",     // private
		"172.16.0.0/12",  // private
		"192.168.0.0/16", // private
		"169.254.0.0/16", // link-local
		"::1/128",        // loopback v6
		"fc00::/7",       // unique local
		"fe80::/10",      // link-local v6
	} {
		_, block, _ := net.ParseCIDR(cidr)
		if block != nil {
			privateIPBlocks = append(privateIPBlocks, block)
		}
	}
}

func ValidateURL(rawURL string) error {
	if len(rawURL) > 2000 {
		return fmt.Errorf("url too long")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http and https schemes are allowed")
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty host")
	}

	if isPrivate(host) {
		return fmt.Errorf("private or internal address not allowed")
	}

	return nil
}

// SafeDialControl is a net.Dialer.Control hook that rejects connections to private,
// loopback, or link-local IPs. It complements ValidateURL rather than replacing it:
// ValidateURL resolves DNS at check time, but the dialer resolves again before
// connecting, so a hostname that passed validation can re-resolve to an internal address
// by the time we dial (DNS rebinding / TOCTOU), and redirects reach the dialer without
// going through ValidateURL's host at all. Control runs after the dialer's own resolution,
// immediately before connecting to the concrete IP, closing that window.
//
// Only wire this into dialers used for direct (non-proxy) fetches. When a forward proxy is
// configured, the dialer connects to the operator-controlled proxy (which may legitimately
// be a private/localhost address), and the target host is resolved at the proxy — out of
// this hook's reach and out of scope for it.
func SafeDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("blocked dial to unresolved address %q", address)
	}
	if isPrivateIP(ip) {
		return fmt.Errorf("blocked dial to private address %s", ip)
	}
	return nil
}

func isPrivate(host string) bool {
	if strings.ToLower(host) == "localhost" {
		return true
	}

	ip := net.ParseIP(host)
	if ip == nil {
		addrs, err := net.LookupHost(host)
		if err != nil {
			return false
		}
		for _, addr := range addrs {
			parsed := net.ParseIP(addr)
			if parsed != nil && isPrivateIP(parsed) {
				return true
			}
		}
		return false
	}

	return isPrivateIP(ip)
}

func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return true
	}
	for _, block := range privateIPBlocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}
