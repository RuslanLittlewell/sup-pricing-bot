package extractor

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/littlewell/price-tracker/internal/renderer"
	"github.com/littlewell/price-tracker/internal/security"
)

const (
	maxBodySize   = 5 * 1024 * 1024 // 5MB
	requestTimeout = 30 * time.Second
)

type PageFetcher struct {
	httpClient *http.Client
	renderer   *renderer.Renderer
}

func NewPageFetcher(r *renderer.Renderer) *PageFetcher {
	return &PageFetcher{
		httpClient: &http.Client{
			Timeout: requestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				if err := security.ValidateURL(req.URL.String()); err != nil {
					return fmt.Errorf("redirect blocked: %w", err)
				}
				return nil
			},
		},
		renderer: r,
	}
}

func (f *PageFetcher) Fetch(url string) ([]byte, error) {
	if err := security.ValidateURL(url); err != nil {
		return nil, fmt.Errorf("url validation failed: %w", err)
	}

	body, err := f.httpFetch(url)
	if err != nil {
		return nil, err
	}

	if isBotChallenge(body) {
		if f.renderer == nil {
			return body, nil
		}
		rendered, err := f.renderer.Render(nil, url, 3*time.Second)
		if err != nil {
			return nil, fmt.Errorf("renderer fallback failed: %w", err)
		}
		return []byte(rendered), nil
	}

	return body, nil
}

func (f *PageFetcher) httpFetch(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	reader := io.LimitReader(resp.Body, maxBodySize)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return body, nil
}

func isBotChallenge(body []byte) bool {
	s := string(body)
	indicators := []string{
		`/_sec/verify`,
		`bm-verify`,
		`checking your browser`,
		`DDoS protection`,
		`cf-browser-verification`,
		`challenge-platform`,
		`Just a moment...`,
	}
	if len(s) < 500 {
		return false
	}
	for _, ind := range indicators {
		if strings.Contains(s, ind) {
			return true
		}
	}
	return false
}
