package proxypool

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

// aliveCheckURL is a lightweight, reliably-up endpoint used only to confirm a SOCKS5
// proxy can actually complete an HTTPS round trip — not to check what it thinks it is.
const aliveCheckURL = "https://www.google.com/generate_204"

// CheckAlive reports whether a "host:port" SOCKS5 proxy can complete a real HTTPS
// request within a short deadline. Public proxy lists are unreliable by nature (dead,
// overloaded, or already blacklisted), so this is the only thing that determines whether
// a listed proxy is actually usable — not the source list's own claims about it. auth may
// be nil for an unauthenticated proxy.
func CheckAlive(ctx context.Context, address string, auth *proxy.Auth) bool {
	dialer, err := proxy.SOCKS5("tcp", address, auth, &net.Dialer{Timeout: 6 * time.Second})
	if err != nil {
		return false
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return false
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext:         contextDialer.DialContext,
			TLSHandshakeTimeout: 8 * time.Second,
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, aliveCheckURL, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

// CheckAliveHTTP is CheckAlive's counterpart for an HTTP(S) forward proxy (CONNECT-style)
// instead of SOCKS5 — needed for proxy sources that hand out plain HTTP proxies (see
// FetchGeonode), which a SOCKS5 dial can't talk to at all. username may be empty for an
// unauthenticated proxy.
func CheckAliveHTTP(ctx context.Context, address, username, password string) bool {
	proxyURL := &url.URL{Scheme: "http", Host: address}
	if username != "" {
		proxyURL.User = url.UserPassword(username, password)
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxyURL),
			TLSHandshakeTimeout: 8 * time.Second,
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, aliveCheckURL, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}
