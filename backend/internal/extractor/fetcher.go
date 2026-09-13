package extractor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/littlewell/price-tracker/internal/proxypool"
	"github.com/littlewell/price-tracker/internal/renderer"
	"github.com/littlewell/price-tracker/internal/scraper"
	"github.com/littlewell/price-tracker/internal/security"
	"github.com/littlewell/price-tracker/internal/shops"
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

// Fetch method labels — which tier actually produced the page body, independent of
// which extraction method later parses a price out of it. Recorded alongside
// extraction_method in price_points/stock_points so the admin dashboard can show, e.g.,
// "parsed via json_ld, fetched via cf_relay" instead of leaving fetch strategy invisible.
const (
	FetchMethodDirect   = "direct"
	FetchMethodCurlCFFI = "curl_cffi"
	FetchMethodCFRelay  = "cf_relay"
	FetchMethodRender   = "render"
	FetchMethodFirefox  = "firefox"
)

type PageFetcher struct {
	httpClient *http.Client
	renderer   *renderer.Renderer
	cookies    []scraper.Cookie
	cfRelay    *CloudflareRelayFetcher
	curlCFFI   *CurlCFFIFetcher
	firefox    *FirefoxFetcher
	proxies    *proxypool.Store
	proxyURL   string
}

// NewPageFetcher builds a fetcher. proxies may be nil (no pool wired up) — direct fetches
// then only ever use cookiesFile/proxyURL's static config, with no per-request rotation
// or per-URL fingerprint memory (see proxies field doc on httpFetch/fetchDirect).
func NewPageFetcher(r *renderer.Renderer, cookiesFile, proxyURL string, proxies *proxypool.Store) *PageFetcher {
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
		cfRelay:  NewCloudflareRelay(),
		curlCFFI: NewCurlCFFI(),
		firefox:  NewFirefox(),
		proxies:  proxies,
		proxyURL: proxyURL,
	}
}

// Fetch returns the page body plus which tier produced it (FetchMethodDirect/CFRelay/
// Render) — see the constants' doc comment for why that's tracked separately from
// extraction_method.
func (f *PageFetcher) Fetch(url string) ([]byte, string, error) {
	if err := security.ValidateURL(url); err != nil {
		return nil, "", fmt.Errorf("url validation failed: %w", err)
	}
	// Zara historically worked through a browser renderer but currently rejects the
	// Chromium fingerprint. Try an isolated Firefox engine before the shared HTTP chain.
	if body, ok := f.fetchZaraViaFirefox(url); ok {
		return body, FetchMethodFirefox, nil
	}

	if f.renderer != nil && shouldRenderFirst(url) {
		rendered, err := f.renderer.Render(nil, url, 6*time.Second)
		if err == nil && !isBotChallenge([]byte(rendered)) {
			return []byte(rendered), FetchMethodRender, nil
		}
	}

	body, fp, err := f.fetchDirect(url)
	if err != nil {
		if !shouldRenderFallback(err) {
			return nil, "", err
		}
		if impersonated, impersonateErr := f.fetchViaCurlCFFI(url); impersonateErr == nil && usableBody(url, impersonated) {
			return impersonated, FetchMethodCurlCFFI, nil
		}
		if relayed, relayErr := f.fetchViaRelay(url); relayErr == nil && usableBody(url, relayed) {
			return relayed, FetchMethodCFRelay, nil
		}
		if f.renderer == nil {
			return nil, "", err
		}
		rendered, renderErr := f.renderer.Render(nil, url, 6*time.Second)
		if renderErr != nil {
			return nil, "", fmt.Errorf("http fetch failed: %w; renderer fallback failed: %w", err, renderErr)
		}
		if isBotChallenge([]byte(rendered)) || !usableBody(url, []byte(rendered)) {
			if retried, ok := f.fetchZaraViaFirefox(url); ok {
				return retried, FetchMethodFirefox, nil
			}
			return nil, "", errBotBlocked
		}
		return []byte(rendered), FetchMethodRender, nil
	}

	if isBotChallenge(body) || !usableBody(url, body) {
		if impersonated, impersonateErr := f.fetchViaCurlCFFI(url); impersonateErr == nil && usableBody(url, impersonated) {
			return impersonated, FetchMethodCurlCFFI, nil
		}
		if relayed, relayErr := f.fetchViaRelay(url); relayErr == nil && usableBody(url, relayed) {
			return relayed, FetchMethodCFRelay, nil
		}
		if f.renderer == nil {
			return nil, "", errBotBlocked
		}
		rendered, err := f.renderer.Render(nil, url, 3*time.Second)
		if err != nil {
			return nil, "", fmt.Errorf("renderer fallback failed: %w", err)
		}
		if isBotChallenge([]byte(rendered)) || !usableBody(url, []byte(rendered)) {
			if retried, ok := f.fetchZaraViaFirefox(url); ok {
				return retried, FetchMethodFirefox, nil
			}
			return nil, "", errBotBlocked
		}
		return []byte(rendered), FetchMethodRender, nil
	}

	if f.proxies != nil {
		_ = f.proxies.SaveFingerprint(context.Background(), url, fp)
	}
	return body, FetchMethodDirect, nil
}

// fetchZaraViaFirefox returns a Zara page body only when the isolated Firefox engine
// produced a real product page. Firefox is the only tier that currently gets through Zara's
// Akamai edge, so it's tried first and — via usableBody's rejection of the other tiers'
// output — retried once at the end rather than letting the chain settle for a body that
// isn't the product. ok is false when Zara isn't the target, the engine isn't configured,
// the fetch failed, or what came back isn't a PDP.
func (f *PageFetcher) fetchZaraViaFirefox(url string) ([]byte, bool) {
	if !isZaraURL(url) || f.firefox == nil {
		return nil, false
	}
	body, err := f.fetchViaFirefox(url)
	if err != nil || !usableBody(url, body) {
		return nil, false
	}
	return body, true
}

// usableBody reports whether body is the page that was actually asked for. A fallback tier
// can return a lookalike (an Akamai interstitial rendered into product-free HTML) that
// carries no bot-challenge marker, so isBotChallenge passes it through and the parsers treat
// it as the real page. Sites we can positively identify get checked here instead; everything
// else keeps the previous behaviour of trusting any non-challenge body.
func usableBody(rawURL string, body []byte) bool {
	if isZaraURL(rawURL) {
		return shops.IsZaraProductPage(body)
	}
	// H&M's Akamai edge sometimes returns a small HTML interstitial with HTTP 200 and
	// wording that is not covered by isBotChallenge. Treat only a real server-rendered
	// product page (identified by its size picker) as usable, so a soft block from direct,
	// curl-cffi, or the relay cannot stop the fallback chain before the next tier is tried.
	if shops.IsHMURL(rawURL) {
		return shops.HasHMSizePicker(body)
	}
	return true
}

func isZaraURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	return host == "zara.com" || strings.HasSuffix(host, ".zara.com")
}

func (f *PageFetcher) fetchViaFirefox(targetURL string) ([]byte, error) {
	proxyURL := f.proxyURL
	if f.proxies != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		picked, ok := f.proxies.PickResidentialForURL(ctx, targetURL)
		cancel()
		if ok {
			proxyURL = picked.HTTPURL()
		}
	}
	headers := map[string]string{
		"Accept-Language": "pl-PL,pl;q=0.9,en-US;q=0.8,en;q=0.7",
		"Cache-Control":   "no-cache",
	}
	if cookieHeader := scraper.HeaderForURL(f.cookies, targetURL); cookieHeader != "" {
		headers["Cookie"] = cookieHeader
	}
	return f.firefox.Fetch(targetURL, proxyURL, headers)
}

func (f *PageFetcher) fetchViaCurlCFFI(targetURL string) ([]byte, error) {
	if f.curlCFFI == nil {
		return nil, fmt.Errorf("curl-cffi relay not configured")
	}
	proxyURL := ""
	if f.proxies != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		picked, ok := f.proxies.PickResidentialForURL(ctx, targetURL)
		cancel()
		if ok {
			proxyURL = picked.HTTPURL()
		}
	}
	headers := map[string]string{
		"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"Accept-Language": "pl-PL,pl;q=0.9,en-US;q=0.8,en;q=0.7",
		"Cache-Control":   "no-cache",
		"Pragma":          "no-cache",
	}
	if cookieHeader := scraper.HeaderForURL(f.cookies, targetURL); cookieHeader != "" {
		headers["Cookie"] = cookieHeader
	}
	return f.curlCFFI.Fetch(targetURL, proxyURL, headers)
}

// fetchDirect tries the (User-Agent, proxy) pairing that last worked for this exact URL
// first, if one is remembered — cheaper than a fresh random gamble, and the whole point
// of remembering it. Falls through to random attempts when there's nothing saved, or the
// saved pairing no longer works (the proxy could have gone dead, or the site could have
// started blocking it since).
func (f *PageFetcher) fetchDirect(url string) ([]byte, proxypool.FetchFingerprint, error) {
	if f.proxies != nil && !proxypool.RequiresRussianProxy(url) {
		if saved, ok := f.proxies.GetFingerprint(context.Background(), url); ok {
			if body, fp, err := f.httpFetch(url, &saved, "saved"); err == nil && !isBotChallenge(body) {
				return body, fp, nil
			}
		}
	}
	return f.httpFetchWithRetry(url)
}

// fetchViaRelay tries the Cloudflare Worker relay (see cloudflare-relay/) — a different
// network origin for a plain fetch, cheap and fast next to spinning up a headless
// browser, so it's worth trying right after our own direct fetch gets blocked and before
// paying for a render. Returns an error (never challenge-page bytes) when the relay isn't
// configured, unreachable, or itself got blocked, so callers can fall through cleanly.
func (f *PageFetcher) fetchViaRelay(url string) ([]byte, error) {
	if f.cfRelay == nil {
		return nil, fmt.Errorf("cloudflare relay not configured")
	}
	body, err := f.cfRelay.Fetch(url)
	if err != nil {
		return nil, err
	}
	if isBotChallenge(body) {
		return nil, errBotBlocked
	}
	return body, nil
}

// httpFetchWithRetry retries transient-looking failures (timeouts, and status codes
// that could be a momentary rate-limit rather than a hard block) a couple of times with
// jittered backoff before giving up. This is cheap relative to falling back to the
// headless renderer, and some bot-detection systems only trip on request bursts. Each
// attempt gets a fresh random (User-Agent, proxy) pick (see pickForAttempt) — a retry
// that reused the same identity that just failed would be pointless.
func (f *PageFetcher) httpFetchWithRetry(url string) ([]byte, proxypool.FetchFingerprint, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		tier := "direct"
		if attempt == 2 {
			tier = "non_residential"
		}
		if attempt == 3 {
			tier = "residential"
		}
		body, fp, err := f.httpFetch(url, nil, tier)
		if err == nil {
			return body, fp, nil
		}
		lastErr = err
		if attempt == maxAttempts || !shouldRenderFallback(err) {
			break
		}
		backoff := time.Duration(300*attempt)*time.Millisecond + time.Duration(rand.Intn(300))*time.Millisecond
		time.Sleep(backoff)
	}
	return nil, proxypool.FetchFingerprint{}, lastErr
}

// pickForAttempt returns the browser profile and proxy to use for one fetch attempt.
// forced replays a previously-successful pairing verbatim (falling back to a random
// profile only if that exact User-Agent is no longer in the pool, e.g. after a deploy
// trims it); nil forced means pick fresh — a random profile, and a random alive proxy
// from the pool if one is wired up and available.
func (f *PageFetcher) pickForAttempt(targetURL, tier string, forced *proxypool.FetchFingerprint) (useragent.Profile, proxypool.PickedProxy) {
	if forced != nil {
		profile, ok := useragent.ByUserAgent(forced.UserAgent)
		if !ok {
			profile = useragent.Random()
		}
		return profile, forced.Proxy
	}

	profile := useragent.Random()
	var proxy proxypool.PickedProxy
	if f.proxies != nil && tier != "direct" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var picked proxypool.PickedProxy
		var ok bool
		if tier == "residential" {
			picked, ok = f.proxies.PickResidentialForURL(ctx, targetURL)
		} else {
			picked, ok = f.proxies.PickNonResidential(ctx)
		}
		if ok {
			proxy = picked
		}
	}
	return profile, proxy
}

// clientFor returns the http.Client to issue the request through: a fresh uTLS client
// dialing via proxy's http:// CONNECT endpoint (see PickedProxy.HTTPURL's doc comment for
// why http:// rather than socks5:// even though the pool stores them as SOCKS5), or the
// statically-configured client from NewPageFetcher when no per-request proxy was picked.
func (f *PageFetcher) clientFor(proxy proxypool.PickedProxy) *http.Client {
	if proxy.Address == "" {
		return f.httpClient
	}
	proxyURL, err := url.Parse(proxy.HTTPURL())
	if err != nil {
		return f.httpClient
	}
	return &http.Client{
		Timeout:       requestTimeout,
		Transport:     newUTLSRoundTripper(proxyURL),
		CheckRedirect: f.httpClient.CheckRedirect,
	}
}

func (f *PageFetcher) httpFetch(url string, forced *proxypool.FetchFingerprint, tier string) ([]byte, proxypool.FetchFingerprint, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, proxypool.FetchFingerprint{}, fmt.Errorf("create request: %w", err)
	}

	profile, proxy := f.pickForAttempt(url, tier, forced)
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

	resp, err := f.clientFor(proxy).Do(req)
	if err != nil {
		return nil, proxypool.FetchFingerprint{}, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, proxypool.FetchFingerprint{}, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	reader := io.LimitReader(resp.Body, maxBodySize)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, proxypool.FetchFingerprint{}, fmt.Errorf("read body: %w", err)
	}

	return body, proxypool.FetchFingerprint{UserAgent: profile.UserAgent, Proxy: proxy}, nil
}

func isBotChallenge(body []byte) bool {
	s := string(body)
	lower := strings.ToLower(s)
	indicators := []string{
		// DNS-Shop's Qrator response contains only a JS challenge, even after rendering.
		`/__qrator/qauth_`,
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
		// Lamoda's SPSN challenge is a generated JavaScript page rather than product
		// HTML. Without this marker it reaches the price parsers as a successful 200.
		`get_cookie_spsn()`,
		`get_cookie_spid()`,
		// Wordfence rate-limit/IP block — confirmed against supermaluch.com, which serves
		// it intermittently once a burst of checks trips the limiter. This link is
		// hardcoded in the plugin's WAF block templates (503.php, 503-lockout.php), so it
		// survives localisation; the page's visible wording does not, and the one we
		// actually hit was Polish. Matching the wording alone would have missed it.
		`wordfence.com/help/?query=locked-out`,
		// The older lib/wf503.php block view has no hardcoded link, so fall back to its
		// English wording for that variant.
		`Your access to this site has been limited by the site owner`,
		`Generated by Wordfence at`,
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
	// Some Akamai configurations intentionally return HTTP 200 for their deny page.
	// Adidas currently serves this variant, so status-code checks alone cannot distinguish
	// it from a product page.
	if strings.Contains(lower, "access denied") &&
		(strings.Contains(lower, "reference #") || strings.Contains(lower, "errors.edgesuite.net")) {
		return true
	}
	return false
}

func shouldRenderFallback(err error) bool {
	msg := err.Error()
	// An SSRF-guard rejection (security.SafeDialControl refusing a private/rebound target
	// IP) must never escalate to the relay or, especially, the headless renderer: chromium
	// re-resolves the host itself and has no such guard, so falling through would reach the
	// internal address the direct dialer just refused. It arrives wrapped as "utls dial:
	// blocked dial to ...", which would otherwise match the "utls dial:" indicator below, so
	// short-circuit it here as a hard, non-retryable failure.
	if strings.Contains(msg, "blocked dial to") {
		return false
	}
	indicators := []string{
		"context deadline exceeded",
		"Client.Timeout exceeded",
		"timeout awaiting response headers",
		// DNS-Shop uses 401 for its Qrator challenge on public product pages.
		"unexpected status code: 401",
		"unexpected status code: 403",
		"unexpected status code: 429",
		"unexpected status code: 503",
		"unexpected status code: 520",
		"unexpected status code: 521",
		"unexpected status code: 522",
		// A dead/unreachable proxy (or its CONNECT tunnel dying mid-handshake) fails
		// here, not with an HTTP status — utls_transport.go wraps every such failure
		// with one of these two prefixes regardless of the underlying OS error text
		// (i/o timeout, connection refused, unexpected EOF, ...), so matching the
		// prefix catches all of them without having to enumerate each one. Without
		// this, a single bad proxy pick would abort the whole fetch on attempt one
		// instead of retrying with a different proxy (see httpFetchWithRetry) or
		// falling through to the cf_relay/render tiers.
		"utls dial:",
		"utls handshake:",
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
