package extractor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
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
// challenge page (Akamai, DataDome, Cloudflare, ...), not the real content — after every
// fetch tier (direct uTLS with proxy rotation, curl-cffi, the relay, the headless
// renderer) has been tried. Surfacing this as a distinct, honest error lets callers tell
// the user "this site blocks bots" instead of a misleading "price not found" after
// silently feeding block-page HTML to the extractor.
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
	proxies    proxyPool
	proxyURL   string
}

// proxyPool is the part of proxypool.Store the fetcher uses.
type proxyPool interface {
	GetFingerprint(ctx context.Context, url string) (proxypool.FetchFingerprint, bool)
	SaveFingerprint(ctx context.Context, url string, fp proxypool.FetchFingerprint) error
	PickNonResidential(ctx context.Context) (proxypool.PickedProxy, bool)
	PickResidentialForURL(ctx context.Context, rawURL string) (proxypool.PickedProxy, bool)
	RotateResidentialSession(rawURL string)
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

	// A nil *Store must become a nil interface, or every "f.proxies != nil" check passes.
	var pool proxyPool
	if proxies != nil {
		pool = proxies
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
		proxies:  pool,
		proxyURL: proxyURL,
	}
}

// Fetch returns the page body plus which tier produced it (FetchMethodCurlCFFI/Direct/
// CFRelay/Render/Firefox) — see the constants' doc comment for why that's tracked
// separately from extraction_method.
//
// Tiers run cheapest-and-most-browserlike first: plain HTTP with Chrome impersonation
// (fetchHTTP, itself escalating from the server's IP to a residential exit), the CF
// relay, a Chromium render on the domain's residential exit, and finally Camoufox.
func (f *PageFetcher) Fetch(url string) ([]byte, string, error) {
	if err := security.ValidateURL(url); err != nil {
		return nil, "", fmt.Errorf("url validation failed: %w", err)
	}
	// Zara historically worked through a browser renderer but currently rejects the
	// Chromium fingerprint. Try an isolated Firefox engine before the shared HTTP chain.
	if isZaraURL(url) {
		if body, err := f.fetchViaFirefox(url); err == nil {
			return body, FetchMethodFirefox, nil
		}
	}

	if f.renderer != nil && shouldRenderFirst(url) {
		rendered, err := f.renderer.Render(nil, url, 6*time.Second)
		if err == nil && !isBotChallenge([]byte(rendered)) {
			return []byte(rendered), FetchMethodRender, nil
		}
		if err == nil {
			f.rotateResidential(url)
		}
	}

	body, method, err := f.fetchHTTP(url)
	if err == nil {
		return body, method, nil
	}
	if !errors.Is(err, errBotBlocked) && !shouldRenderFallback(err) {
		return nil, "", err
	}

	if relayed, relayErr := f.fetchViaRelay(url); relayErr == nil && usableBody(url, relayed) {
		return relayed, FetchMethodCFRelay, nil
	}

	if f.renderer != nil {
		wait := 6 * time.Second
		if errors.Is(err, errBotBlocked) {
			wait = 3 * time.Second
		}
		rendered, renderErr := f.renderer.Render(nil, url, wait)
		switch {
		case renderErr != nil:
			err = fmt.Errorf("http fetch failed: %w; renderer fallback failed: %w", err, renderErr)
		case isBotChallenge([]byte(rendered)) || !usableBody(url, []byte(rendered)):
			// The renderer relaunches the domain's browser on the new exit next time.
			f.rotateResidential(url)
			err = errBotBlocked
		default:
			return []byte(rendered), FetchMethodRender, nil
		}
	}

	// Last resort for every site: Camoufox spoofs its fingerprint inside the engine rather
	// than through CDP overrides, which is what gets it past Akamai where Chromium is
	// refused. It's also the slowest tier — a fresh browser per page — so it only runs
	// once everything cheaper has failed (for Zara this is the second try).
	if body, ffErr := f.fetchViaFirefox(url); ffErr == nil {
		return body, FetchMethodFirefox, nil
	}
	return nil, "", err
}

// fetchHTTP is the plain-HTTP part of the chain: curl-cffi's Chrome impersonation (real
// h2, TLS and header order matching its UA, compression) stepping through exits from free
// to paid — see httpExits. The in-process uTLS client only takes over when the curl-cffi
// relay itself is unreachable: an HTTP/1.1-only "Chrome" is a bot signal of its own, and
// it used to go out through the sticky residential exit and get that IP flagged right
// before curl-cffi tried the very same one.
func (f *PageFetcher) fetchHTTP(url string) ([]byte, string, error) {
	if f.curlCFFI == nil {
		return f.fetchUTLS(url)
	}
	var lastErr error
	for i, exit := range f.httpExits(url) {
		proxyURL, picked, ok := exit.pick()
		if !ok {
			continue
		}
		if i > 0 && lastErr != nil {
			time.Sleep(time.Duration(300+rand.Intn(300)) * time.Millisecond)
		}
		body, err := f.fetchViaCurlCFFI(url, proxyURL, exit.timeout)
		if err == nil {
			if f.proxies != nil {
				_ = f.proxies.SaveFingerprint(context.Background(), url, proxypool.FetchFingerprint{UserAgent: curlCFFIFingerprint, Proxy: picked})
			}
			return body, FetchMethodCurlCFFI, nil
		}
		if errors.Is(err, errRelayUnavailable) {
			return f.fetchUTLS(url)
		}
		if exit.residential {
			f.rotateResidential(url)
		}
		lastErr = err
		if !errors.Is(err, errBotBlocked) && !shouldRenderFallback(err) {
			// A 404 or an SSRF rejection: no other exit changes that.
			return nil, "", err
		}
	}
	return nil, "", lastErr
}

// curlCFFIFingerprint marks scrape_fingerprints rows written by the curl-cffi tier, whose
// User-Agent is curl-cffi's own (matching its TLS), not one of ours. The uTLS fallback
// replaying such a row finds no pooled profile by that name and picks a random one.
const curlCFFIFingerprint = "curl_cffi"

// httpExit is one network exit fetchHTTP can try. pick resolves it lazily, so the pool is
// only queried (and a paid exit only counted as used) once the cheaper ones have failed.
type httpExit struct {
	residential bool
	timeout     time.Duration
	pick        func() (proxyURL string, picked proxypool.PickedProxy, ok bool)
}

// httpExits orders the exits for url: the proxy that last worked for this exact URL (if it
// was one — skipping exits already known to fail saves requests that themselves raise a
// bot score), then the server's own IP (or SCRAPER_PROXY_URL), a free public proxy, and
// finally the paid residential exit pinned to the domain's sticky session.
func (f *PageFetcher) httpExits(url string) []httpExit {
	var exits []httpExit
	if f.proxies != nil && !proxypool.RequiresRussianProxy(url) {
		if saved, ok := f.proxies.GetFingerprint(context.Background(), url); ok && saved.Proxy.Address != "" {
			exits = append(exits, httpExit{
				residential: saved.Proxy.Username != "",
				timeout:     curlCFFIExitTimeout,
				pick: func() (string, proxypool.PickedProxy, bool) {
					return saved.Proxy.ClientURL(), saved.Proxy, true
				},
			})
		}
	}
	exits = append(exits, httpExit{
		timeout: curlCFFIExitTimeout,
		pick: func() (string, proxypool.PickedProxy, bool) {
			return f.proxyURL, proxypool.PickedProxy{}, true
		},
	})
	if f.proxies == nil {
		return exits
	}
	return append(exits,
		httpExit{
			// Public proxies are mostly slow or dead; don't let one eat the whole budget.
			timeout: publicProxyExitTimeout,
			pick: func() (string, proxypool.PickedProxy, bool) {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				picked, ok := f.proxies.PickNonResidential(ctx)
				return picked.ClientURL(), picked, ok
			},
		},
		httpExit{
			residential: true,
			timeout:     curlCFFIExitTimeout,
			pick: func() (string, proxypool.PickedProxy, bool) {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				picked, ok := f.proxies.PickResidentialForURL(ctx, url)
				return picked.ClientURL(), picked, ok
			},
		},
	)
}

const (
	curlCFFIExitTimeout    = 25 * time.Second
	publicProxyExitTimeout = 12 * time.Second
)

// fetchUTLS is the in-process fallback for when the curl-cffi relay is down: the saved
// pairing, then the direct → public proxy → residential uTLS ladder.
func (f *PageFetcher) fetchUTLS(url string) ([]byte, string, error) {
	body, fp, err := f.fetchDirect(url)
	if err != nil {
		return nil, "", err
	}
	if isBotChallenge(body) || !usableBody(url, body) {
		return nil, "", errBotBlocked
	}
	if f.proxies != nil {
		_ = f.proxies.SaveFingerprint(context.Background(), url, fp)
	}
	return body, FetchMethodDirect, nil
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

// residentialProxyURL picks the residential exit for targetURL's host, or "" when the pool
// isn't wired up or has no alive residential proxy.
func (f *PageFetcher) residentialProxyURL(targetURL string) string {
	if f.proxies == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	picked, ok := f.proxies.PickResidentialForURL(ctx, targetURL)
	if !ok {
		return ""
	}
	return picked.HTTPURL()
}

// rotateResidential moves targetURL's host off its current residential exit IP after an
// attempt through it failed, so the next tier (and the next check) doesn't retry from an
// address the site has just refused — see proxypool.Store.RotateResidentialSession.
func (f *PageFetcher) rotateResidential(targetURL string) {
	if f.proxies != nil {
		f.proxies.RotateResidentialSession(targetURL)
	}
}

// fetchViaFirefox renders through the Camoufox relay (firefox-relay/) on the domain's
// residential exit. Camoufox sets its own Accept-Language and timezone from its locale
// and the exit IP, so only cookies are passed along.
func (f *PageFetcher) fetchViaFirefox(targetURL string) ([]byte, error) {
	if f.firefox == nil {
		return nil, fmt.Errorf("firefox renderer not configured")
	}
	proxyURL := f.proxyURL
	residential := f.residentialProxyURL(targetURL)
	if residential != "" {
		proxyURL = residential
	}
	headers := map[string]string{}
	if cookieHeader := scraper.HeaderForURL(f.cookies, targetURL); cookieHeader != "" {
		headers["Cookie"] = cookieHeader
	}
	body, err := f.firefox.Fetch(targetURL, proxyURL, headers)
	if err == nil && !usableBody(targetURL, body) {
		err = errBotBlocked
	}
	if err != nil {
		if residential != "" {
			f.rotateResidential(targetURL)
		}
		return nil, err
	}
	return body, nil
}

// fetchViaCurlCFFI fetches through the curl-cffi relay from proxyURL ("" = the relay's
// own IP). A challenge page or an unusable lookalike comes back as errBotBlocked.
func (f *PageFetcher) fetchViaCurlCFFI(targetURL, proxyURL string, timeout time.Duration) ([]byte, error) {
	headers := map[string]string{
		"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"Accept-Language": "pl-PL,pl;q=0.9,en-US;q=0.8,en;q=0.7",
		"Cache-Control":   "no-cache",
		"Pragma":          "no-cache",
	}
	if cookieHeader := scraper.HeaderForURL(f.cookies, targetURL); cookieHeader != "" {
		headers["Cookie"] = cookieHeader
	}
	body, err := f.curlCFFI.Fetch(targetURL, proxyURL, headers, timeout)
	if err != nil {
		return nil, err
	}
	if !usableBody(targetURL, body) {
		return nil, errBotBlocked
	}
	return body, nil
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
		if tier == "residential" && fp.Proxy.Address != "" && (err != nil || isBotChallenge(body) || !usableBody(url, body)) {
			f.rotateResidential(url)
		}
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
// statically-configured transport from NewPageFetcher when no per-request proxy was picked.
//
// Every call gets its own cookie jar. Anti-bot edges and geo/consent gates commonly answer
// the first hit with a Set-Cookie plus a redirect and expect that cookie back on the next
// hop; without a jar the redirect arrives cookie-less and gets blocked or loops until "too
// many redirects". The jar is per fetch, never shared, so cookies can't leak between
// targets, proxies, or users.
func (f *PageFetcher) clientFor(proxy proxypool.PickedProxy) *http.Client {
	client := *f.httpClient
	client.Jar, _ = cookiejar.New(nil)
	if proxy.Address == "" {
		return &client
	}
	proxyURL, err := url.Parse(proxy.HTTPURL())
	if err != nil {
		return &client
	}
	client.Transport = newUTLSRoundTripper(proxyURL)
	return &client
}

func (f *PageFetcher) httpFetch(url string, forced *proxypool.FetchFingerprint, tier string) ([]byte, proxypool.FetchFingerprint, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, proxypool.FetchFingerprint{}, fmt.Errorf("create request: %w", err)
	}

	profile, proxy := f.pickForAttempt(url, tier, forced)
	// Returned on failure too, so the caller can tell which exit the failed attempt used.
	fp := proxypool.FetchFingerprint{UserAgent: profile.UserAgent, Proxy: proxy}
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
		return nil, fp, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fp, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	reader := io.LimitReader(resp.Body, maxBodySize)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fp, fmt.Errorf("read body: %w", err)
	}

	return body, fp, nil
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
		// Cloudflare's interstitial challenge. Deliberately not the bare
		// "challenge-platform": with Bot Fight Mode / JS detections enabled, Cloudflare
		// injects /cdn-cgi/challenge-platform/scripts/jsd/main.js into ordinary pages too,
		// so that substring flagged real product pages as blocked. Only the challenge
		// page itself carries _cf_chl_opt and the /h/ orchestrate path; its visible
		// wording is localized per Accept-Language, so those are the reliable markers.
		`_cf_chl_opt`,
		`/cdn-cgi/challenge-platform/h/`,
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
		// The connection was established but the edge dropped it instead of answering —
		// how some Akamai/Cloudflare configs treat an HTTP/1.1-only "Chrome" (our uTLS
		// tier can't speak h2). curl-cffi impersonates h2 properly, so these must fall
		// through to it rather than fail the whole fetch on the first tier.
		"read response:",
		"write request:",
		"connection reset by peer",
		"EOF",
		// curl-cffi couldn't complete the request through the chosen exit (dead or slow
		// proxy, refused connection) — another exit or tier may well succeed.
		"curl-cffi upstream fetch failed",
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
