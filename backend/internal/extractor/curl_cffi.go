package extractor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const curlCFFITimeout = 30 * time.Second

type CurlCFFIFetcher struct {
	baseURL string
	client  *http.Client
}

type curlCFFIRequest struct {
	URL     string            `json:"url"`
	Proxy   string            `json:"proxy,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

func NewCurlCFFI() *CurlCFFIFetcher {
	baseURL := strings.TrimRight(os.Getenv("CURL_CFFI_URL"), "/")
	if baseURL == "" {
		return nil
	}
	return &CurlCFFIFetcher{baseURL: baseURL, client: &http.Client{Timeout: curlCFFITimeout}}
}

func (f *CurlCFFIFetcher) Fetch(targetURL, proxy string, headers map[string]string) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("curl-cffi relay not configured")
	}
	payload, err := json.Marshal(curlCFFIRequest{URL: targetURL, Proxy: proxy, Headers: headers})
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
		return nil, fmt.Errorf("curl-cffi request failed: %w", err)
	}
	defer resp.Body.Close()
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
		return nil, fmt.Errorf("curl-cffi upstream status %d", upstreamStatus)
	}
	if isBotChallenge(body) {
		return nil, errBotBlocked
	}
	return body, nil
}
