package extractor

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const cfRelayTimeout = 20 * time.Second

// CloudflareRelayFetcher fetches a URL through a small Cloudflare Worker (see
// cloudflare-relay/worker.js) that does the request server-side from Cloudflare's edge
// network. It's a plain HTTP fetch relay, not a SOCKS5/CONNECT proxy — it gives a
// different network origin for a request our own IP got blocked on, but it doesn't
// execute JavaScript, so a JS-challenge page still comes back as a challenge page.
type CloudflareRelayFetcher struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewCloudflareRelay returns nil when CF_RELAY_URL or CF_RELAY_TOKEN is unconfigured, so
// callers can treat this tier as simply absent (same pattern as NewOpenSERP).
func NewCloudflareRelay() *CloudflareRelayFetcher {
	baseURL := strings.TrimRight(os.Getenv("CF_RELAY_URL"), "/")
	token := os.Getenv("CF_RELAY_TOKEN")
	if baseURL == "" || token == "" {
		return nil
	}
	return &CloudflareRelayFetcher{
		baseURL:    baseURL,
		token:      token,
		httpClient: &http.Client{Timeout: cfRelayTimeout},
	}
}

func (f *CloudflareRelayFetcher) Fetch(targetURL string) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("cloudflare relay not configured")
	}

	endpoint, err := url.Parse(f.baseURL)
	if err != nil {
		return nil, fmt.Errorf("build relay URL: %w", err)
	}
	q := endpoint.Query()
	q.Set("url", targetURL)
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build relay request: %w", err)
	}
	req.Header.Set("X-Relay-Token", f.token)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("relay request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("relay returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("read relay response: %w", err)
	}
	return body, nil
}
