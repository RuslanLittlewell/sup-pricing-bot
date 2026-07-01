package extractor

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	utls "github.com/refraction-networking/utls"
)

// newUTLSRoundTripper builds an http.RoundTripper that performs the TLS handshake with
// a Chrome-like ClientHello (cipher suites, extensions, curves, GREASE — via uTLS)
// instead of Go's default crypto/tls fingerprint. Bot-detection systems (Akamai,
// Cloudflare, DataDome, PerimeterX) commonly fingerprint the TLS handshake itself
// (JA3/JA4) independently of HTTP headers, and Go's stock fingerprint is trivially
// distinguishable from a real browser's.
//
// Trade-off: this forces HTTP/1.1 (via ALPN) instead of offering h2 like a real
// browser would. Properly multiplexing HTTP/2 over a uTLS connection requires reaching
// into unexported net/http internals to hook the TLSNextProto upgrade path; doing that
// naively risks a broken connection (ALPN says h2, we speak h1 framing) which is worse
// than just not offering h2. The TLS layer fingerprint — the part actually being
// checked — is unaffected by this.
func newUTLSRoundTripper(proxyURL *url.URL) http.RoundTripper {
	return &utlsRoundTripper{
		dialer:   &net.Dialer{Timeout: 15 * time.Second},
		proxyURL: proxyURL,
		fallback: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}
}

type utlsRoundTripper struct {
	dialer   *net.Dialer
	proxyURL *url.URL
	fallback *http.Transport // used for non-https requests; uTLS only applies to TLS
}

func (rt *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return rt.fallback.RoundTrip(req)
	}

	ctx := req.Context()
	addr := addrForURL(req.URL)

	rawConn, err := rt.dialThroughProxy(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("utls dial: %w", err)
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	uConn, err := utlsHandshake(ctx, rawConn, host)
	if err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("utls handshake: %w", err)
	}

	if err := req.Write(uConn); err != nil {
		uConn.Close()
		return nil, fmt.Errorf("write request: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(uConn), req)
	if err != nil {
		uConn.Close()
		return nil, fmt.Errorf("read response: %w", err)
	}
	resp.Body = &connClosingBody{ReadCloser: resp.Body, conn: uConn}
	return resp, nil
}

// utlsHandshake completes a TLS handshake over rawConn using a Chrome ClientHello
// fingerprint, with ALPN pinned to http/1.1 (see newUTLSRoundTripper doc comment).
func utlsHandshake(ctx context.Context, rawConn net.Conn, host string) (*utls.UConn, error) {
	spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
	if err != nil {
		return nil, fmt.Errorf("build client hello spec: %w", err)
	}
	for _, ext := range spec.Extensions {
		if alpn, ok := ext.(*utls.ALPNExtension); ok {
			alpn.AlpnProtocols = []string{"http/1.1"}
		}
	}

	uConn := utls.UClient(rawConn, &utls.Config{ServerName: host}, utls.HelloCustom)
	if err := uConn.ApplyPreset(&spec); err != nil {
		return nil, fmt.Errorf("apply client hello spec: %w", err)
	}
	if err := uConn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	return uConn, nil
}

// dialThroughProxy establishes a raw (pre-TLS) TCP connection to addr, tunneling
// through an HTTP forward proxy via CONNECT if one is configured — mirroring what
// net/http.Transport does internally for HTTPS-over-proxy, since we bypass Transport
// entirely to control the TLS handshake ourselves.
func (rt *utlsRoundTripper) dialThroughProxy(ctx context.Context, addr string) (net.Conn, error) {
	if rt.proxyURL == nil {
		return rt.dialer.DialContext(ctx, "tcp", addr)
	}

	proxyConn, err := rt.dialer.DialContext(ctx, "tcp", rt.proxyURL.Host)
	if err != nil {
		return nil, fmt.Errorf("dial proxy: %w", err)
	}

	connectReq := &http.Request{
		Method: "CONNECT",
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if user := rt.proxyURL.User; user != nil {
		password, _ := user.Password()
		token := base64.StdEncoding.EncodeToString([]byte(user.Username() + ":" + password))
		connectReq.Header.Set("Proxy-Authorization", "Basic "+token)
	}
	if err := connectReq.Write(proxyConn); err != nil {
		proxyConn.Close()
		return nil, fmt.Errorf("write CONNECT: %w", err)
	}

	br := bufio.NewReader(proxyConn)
	resp, err := http.ReadResponse(br, connectReq)
	if err != nil {
		proxyConn.Close()
		return nil, fmt.Errorf("read CONNECT response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		proxyConn.Close()
		return nil, fmt.Errorf("proxy CONNECT failed: %s", resp.Status)
	}
	if br.Buffered() > 0 {
		proxyConn.Close()
		return nil, fmt.Errorf("unexpected data buffered after CONNECT response")
	}

	return proxyConn, nil
}

func addrForURL(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	return net.JoinHostPort(u.Hostname(), "443")
}

// connClosingBody closes the underlying raw connection once the response body is
// closed. We don't pool/reuse these one-shot connections, so nothing else would
// close it otherwise.
type connClosingBody struct {
	io.ReadCloser
	conn net.Conn
}

func (b *connClosingBody) Close() error {
	bodyErr := b.ReadCloser.Close()
	connErr := b.conn.Close()
	if bodyErr != nil {
		return bodyErr
	}
	return connErr
}
