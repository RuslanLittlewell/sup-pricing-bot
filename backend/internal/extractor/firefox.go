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

type FirefoxFetcher struct {
	baseURL string
	client  *http.Client
}

func NewFirefox() *FirefoxFetcher {
	baseURL := strings.TrimRight(os.Getenv("FIREFOX_RENDERER_URL"), "/")
	if baseURL == "" {
		return nil
	}
	return &FirefoxFetcher{baseURL: baseURL, client: &http.Client{Timeout: 40 * time.Second}}
}

func (f *FirefoxFetcher) Fetch(targetURL, proxy string, headers map[string]string) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("firefox renderer not configured")
	}
	payload, err := json.Marshal(curlCFFIRequest{URL: targetURL, Proxy: proxy, Headers: headers})
	if err != nil {
		return nil, fmt.Errorf("encode firefox request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, f.baseURL+"/render", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build firefox request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("firefox request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("firefox renderer returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("read firefox response: %w", err)
	}
	if isBotChallenge(body) {
		return nil, errBotBlocked
	}
	return body, nil
}
