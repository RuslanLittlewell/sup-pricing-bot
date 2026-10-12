package extractor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const curlCFFITimeout = 30 * time.Second

// errRelayUnavailable means the curl-cffi relay itself couldn't be reached (container
// down, restarting), as opposed to the relay reporting on the target. PageFetcher falls
// back to its in-process uTLS client on this, rather than giving up on plain HTTP.
var errRelayUnavailable = errors.New("curl-cffi relay unavailable")

type CurlCFFIFetcher struct {
	baseURL string
	client  *http.Client
}

type curlCFFIRequest struct {
	URL     string            `json:"url"`
	Proxy   string            `json:"proxy,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Timeout (seconds) caps the upstream request below the relay's own limit.
	Timeout float64 `json:"timeout,omitempty"`
}

func NewCurlCFFI() *CurlCFFIFetcher {
	baseURL := strings.TrimRight(os.Getenv("CURL_CFFI_URL"), "/")
	if baseURL == "" {
		return nil
	}
	return &CurlCFFIFetcher{baseURL: baseURL, client: &http.Client{Timeout: curlCFFITimeout}}
}

func (f *CurlCFFIFetcher) Fetch(targetURL, proxy string, headers map[string]string, timeout time.Duration) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("curl-cffi relay not configured")
	}
	payload, err := json.Marshal(curlCFFIRequest{URL: targetURL, Proxy: proxy, Headers: headers, Timeout: timeout.Seconds()})
	if err != nil {
		return nil, fmt.Errorf("encode curl-cffi request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, f.baseURL+"/fetch", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build curl-cffi request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errRelayUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadGateway {
		return nil, fmt.Errorf("curl-cffi upstream fetch failed via %s", proxyLabel(proxy))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("curl-cffi relay returned status %d", resp.StatusCode)
	}
	var upstreamStatus int
	if _, err := fmt.Sscanf(resp.Header.Get("X-Upstream-Status"), "%d", &upstreamStatus); err != nil {
		return nil, fmt.Errorf("curl-cffi relay omitted upstream status")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("read curl-cffi response: %w", err)
	}
	if upstreamStatus < 200 || upstreamStatus >= 400 {
		// Same wording as the uTLS tier, so shouldRenderFallback treats a 403 from either
		// as "try the next exit" and a 404 as final.
		return nil, fmt.Errorf("unexpected status code: %d", upstreamStatus)
	}
	if isBotChallenge(body) {
		return nil, errBotBlocked
	}
	return body, nil
}

// proxyLabel names an exit for error messages without leaking its credentials.
func proxyLabel(proxy string) string {
	if proxy == "" {
		return "direct"
	}
	if at := strings.LastIndex(proxy, "@"); at >= 0 {
		scheme, _, _ := strings.Cut(proxy, "://")
		return scheme + "://" + proxy[at+1:]
	}
	return proxy
}
