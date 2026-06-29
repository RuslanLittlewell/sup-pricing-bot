package extractor

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/littlewell/price-tracker/internal/renderer"
	"github.com/littlewell/price-tracker/internal/scraper"
	"github.com/littlewell/price-tracker/internal/security"
)

const (
	maxBodySize    = 5 * 1024 * 1024 // 5MB
	requestTimeout = 30 * time.Second
)

type PageFetcher struct {
	httpClient *http.Client
	renderer   *renderer.Renderer
	cookies    []scraper.Cookie
}

func NewPageFetcher(r *renderer.Renderer, cookiesFile, proxyURL string) *PageFetcher {
	cookies, _ := scraper.LoadCookies(cookiesFile)
	transport := &http.Transport{}
	if proxyURL != "" {
		if proxy, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(proxy)
		}
	}

	return &PageFetcher{
		httpClient: &http.Client{
			Timeout:   requestTimeout,
			Transport: transport,
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
		cookies:  cookies,
	}
}

func (f *PageFetcher) Fetch(url string) ([]byte, error) {
	if err := security.ValidateURL(url); err != nil {
		return nil, fmt.Errorf("url validation failed: %w", err)
	}

	if f.renderer != nil && shouldRenderFirst(url) {
		rendered, err := f.renderer.Render(nil, url, 6*time.Second)
		if err == nil && !isBotChallenge([]byte(rendered)) {
			return []byte(rendered), nil
		}
	}

	body, err := f.httpFetch(url)
	if err != nil {
		if f.renderer == nil || !shouldRenderFallback(err) {
			return nil, err
		}
		rendered, renderErr := f.renderer.Render(nil, url, 6*time.Second)
		if renderErr != nil {
			return nil, fmt.Errorf("http fetch failed: %w; renderer fallback failed: %w", err, renderErr)
		}
		return []byte(rendered), nil
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

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "pl-PL,pl;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-CH-UA", `"Chromium";v="126", "Google Chrome";v="126", "Not-A.Brand";v="99"`)
	req.Header.Set("Sec-CH-UA-Mobile", "?0")
	req.Header.Set("Sec-CH-UA-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	if cookieHeader := scraper.HeaderForURL(f.cookies, url); cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}

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

func shouldRenderFallback(err error) bool {
	msg := err.Error()
	indicators := []string{
		"context deadline exceeded",
		"Client.Timeout exceeded",
		"timeout awaiting response headers",
		"unexpected status code: 403",
		"unexpected status code: 429",
		"unexpected status code: 503",
		"unexpected status code: 520",
		"unexpected status code: 521",
		"unexpected status code: 522",
	}
	for _, indicator := range indicators {
		if strings.Contains(msg, indicator) {
			return true
		}
	}
	return false
}

func shouldRenderFirst(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "allegro.pl" || strings.HasSuffix(host, ".allegro.pl")
}
