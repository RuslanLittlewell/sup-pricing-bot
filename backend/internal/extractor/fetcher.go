package extractor

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/littlewell/price-tracker/internal/renderer"
	"github.com/littlewell/price-tracker/internal/scraper"
	"github.com/littlewell/price-tracker/internal/security"
	"github.com/littlewell/price-tracker/internal/useragent"
)

const (
	maxBodySize    = 5 * 1024 * 1024 // 5MB
	requestTimeout = 30 * time.Second
)

// errBotBlocked means the page was reached but the response is a bot-detection block/
// challenge page (Akamai, DataDome, Cloudflare, ...), not the real content — including
// after falling back to the headless renderer. We deliberately don't escalate further
// (TLS/fingerprint tricks, proxy rotation) when this happens; the site is telling us,
// unambiguously, that it doesn't want automated access to this page. Surfacing this as
// a distinct, honest error lets callers tell the user "this site blocks bots" instead of
// a misleading "price not found" after silently feeding block-page HTML to the extractor.
var errBotBlocked = errors.New("page is blocked by anti-bot protection")

type PageFetcher struct {
	httpClient *http.Client
	renderer   *renderer.Renderer
	cookies    []scraper.Cookie
}

func NewPageFetcher(r *renderer.Renderer, cookiesFile, proxyURL string) *PageFetcher {
	cookies, _ := scraper.LoadCookies(cookiesFile)

	var proxy *url.URL
	if proxyURL != "" {
		if parsed, err := url.Parse(proxyURL); err == nil {
			proxy = parsed
		}
	}

	return &PageFetcher{
		httpClient: &http.Client{
			Timeout:   requestTimeout,
			Transport: newUTLSRoundTripper(proxy),
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

	body, err := f.httpFetchWithRetry(url)
	if err != nil {
		if f.renderer == nil || !shouldRenderFallback(err) {
			return nil, err
		}
		rendered, renderErr := f.renderer.Render(nil, url, 6*time.Second)
		if renderErr != nil {
			return nil, fmt.Errorf("http fetch failed: %w; renderer fallback failed: %w", err, renderErr)
		}
		if isBotChallenge([]byte(rendered)) {
			return nil, errBotBlocked
		}
		return []byte(rendered), nil
	}

	if isBotChallenge(body) {
		if f.renderer == nil {
			return nil, errBotBlocked
		}
		rendered, err := f.renderer.Render(nil, url, 3*time.Second)
		if err != nil {
			return nil, fmt.Errorf("renderer fallback failed: %w", err)
		}
		if isBotChallenge([]byte(rendered)) {
			return nil, errBotBlocked
		}
		return []byte(rendered), nil
	}

	return body, nil
}

// httpFetchWithRetry retries transient-looking failures (timeouts, and status codes
// that could be a momentary rate-limit rather than a hard block) a couple of times with
// jittered backoff before giving up. This is cheap relative to falling back to the
// headless renderer, and some bot-detection systems only trip on request bursts.
func (f *PageFetcher) httpFetchWithRetry(url string) ([]byte, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		body, err := f.httpFetch(url)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if attempt == maxAttempts || !shouldRenderFallback(err) {
			break
		}
		backoff := time.Duration(300*attempt)*time.Millisecond + time.Duration(rand.Intn(300))*time.Millisecond
		time.Sleep(backoff)
	}
	return nil, lastErr
}

func (f *PageFetcher) httpFetch(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	profile := useragent.Random()
	req.Header.Set("User-Agent", profile.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "pl-PL,pl;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-CH-UA", profile.SecCHUA)
	req.Header.Set("Sec-CH-UA-Mobile", "?0")
	req.Header.Set("Sec-CH-UA-Platform", profile.SecCHUAPlatform)
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
		// Akamai edge deny — confirmed against www2.hm.com (blocks at the network/IP
		// reputation layer, before any JS challenge; the page itself is tiny).
		`You don't have permission to access`,
		// DataDome challenge/block — confirmed against allegro.pl.
		`enable JS and disable any ad blocker`,
		`Zostałeś zablokowany`,
	}
	// Akamai's edge-deny page is only ~414 bytes. A distinctive string match is
	// unambiguous regardless of body length, so check indicators unconditionally rather
	// than gating on a minimum size (that floor exists only to avoid false-positiving on
	// tiny but otherwise legitimate/empty pages, which a specific phrase match can't do).
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
