package renderer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/littlewell/price-tracker/internal/proxypool"
	"github.com/littlewell/price-tracker/internal/scraper"
	"github.com/littlewell/price-tracker/internal/useragent"
)

// acceptLanguages is the plain language list for Emulation.setUserAgentOverride, which
// adds the q-weights itself (producing "pl-PL,pl;q=0.9,en-US;q=0.8,en;q=0.7"). Passing an
// already-weighted header value yields "pl;q=0.9;q=0.9" and navigator.languages entries
// like "pl;q=0.9".
const acceptLanguages = "pl-PL,pl,en-US,en"

// ProxySource hands out a domain's residential exit (proxypool.Store). Every persistent
// browser is a single domain's own Chromium process, so it can carry that domain's sticky
// exit in --proxy-server.
type ProxySource interface {
	PickResidentialForURL(ctx context.Context, rawURL string) (proxypool.PickedProxy, bool)
}

type Renderer struct {
	rootCtx       context.Context
	cancel        context.CancelFunc
	cookies       []scraper.Cookie
	proxyServer   string
	proxyUser     string
	proxyPassword string
	proxies       ProxySource
	execPath      string
	// identity is the browser this renderer really is (see detectIdentity), presented by
	// every session.
	identity     useragent.Profile
	headful      bool
	profileRoot  string
	profileMu    sync.Mutex
	profileLocks map[string]*sync.Mutex
	extensionDir string
	persistent   bool
	maxSessions  int
	sessions     map[string]*browserSession
	// extraFlags is a test hook, e.g. to route loopback through a test proxy.
	extraFlags []chromedp.ExecAllocatorOption
}

type browserSession struct {
	ctx      context.Context
	cancel   context.CancelFunc
	lastUsed time.Time
	timezone string
	// The exit this Chromium was launched through. --proxy-server is fixed for the
	// process's lifetime, so proxyGen — the host's residential generation at launch — is
	// how a later render notices the host has been rotated off this IP and relaunches.
	proxyUser     string
	proxyPassword string
	residential   bool
	proxyGen      uint32
	// skipMedia is set by Render while it holds the host lock and read by the request
	// interceptor on CDP's event goroutine.
	skipMedia atomic.Bool
	// prepared records that identity and proxy auth have been applied to the tab; only
	// read/written under the host's profile lock.
	prepared bool
}

type PriceBlockCandidate struct {
	Selector           string `json:"selector"`
	ScreenshotSelector string `json:"screenshot_selector"`
	Text               string `json:"text"`
	PriceText          string `json:"price_text"`
	PriceTokenIndex    int    `json:"price_token_index"`
	Title              string `json:"title"`
	TotalFound         int    `json:"total_found"`
}

// BrowserDiagnostics exposes only non-sensitive browser state. Cookie values and
// captured request payloads are deliberately never returned to the admin API.
type BrowserDiagnostics struct {
	PersistentBrowser bool     `json:"persistentBrowser"`
	ExtensionEnabled  bool     `json:"extensionEnabled"`
	ExtensionLoaded   bool     `json:"extensionLoaded"`
	AkamaiCookies     []string `json:"akamaiCookies"`
}

func New(cookiesFile, proxyURL string) (*Renderer, error) {
	proxyServer, proxyUser, proxyPassword := normalizeProxyURL(proxyURL)
	cookies, err := scraper.LoadCookies(cookiesFile)
	if err != nil {
		return nil, err
	}
	profileRoot := os.Getenv("SCRAPER_PROFILE_DIR")
	if profileRoot == "" {
		profileRoot = filepath.Join(os.TempDir(), "pricebot-browser-profiles")
	}
	namespace := os.Getenv("RENDERER_PROFILE_NAMESPACE")
	if namespace != "" {
		profileRoot = filepath.Join(profileRoot, namespace)
	}
	if err := os.MkdirAll(profileRoot, 0700); err != nil {
		return nil, fmt.Errorf("create browser profile directory: %w", err)
	}
	maxSessions := 4
	if raw := os.Getenv("SCRAPER_MAX_BROWSER_SESSIONS"); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
			maxSessions = parsed
		}
	}
	execPath := os.Getenv("CHROME_PATH") // set by the Dockerfile; lets a local run use its own Chrome
	if execPath == "" {
		execPath = "/usr/bin/chromium"
	}
	rootCtx, cancel := context.WithCancel(context.Background())
	return &Renderer{
		rootCtx: rootCtx, cancel: cancel, cookies: cookies,
		proxyServer: proxyServer, proxyUser: proxyUser, proxyPassword: proxyPassword,
		execPath: execPath, identity: detectIdentity(execPath),
		// Headful (under the entrypoint's Xvfb) is what a real visitor runs; the extension
		// used to be the only way to get it, which tied a diagnostic tool to production.
		headful:     strings.EqualFold(os.Getenv("SCRAPER_HEADFUL"), "true"),
		profileRoot: profileRoot, profileLocks: map[string]*sync.Mutex{},
		extensionDir: os.Getenv("SCRAPER_EXTENSION_DIR"),
		persistent:   strings.EqualFold(os.Getenv("SCRAPER_PERSISTENT_BROWSER"), "true"),
		maxSessions:  maxSessions,
		sessions:     map[string]*browserSession{},
	}, nil
}

// UseProxyPool routes each domain's browser through that domain's residential exit,
// falling back to SCRAPER_PROXY_URL — or the server's own IP — while the pool has none.
// Without it every render left from the server's datacenter IP, which Akamai and DataDome
// reject on reputation alone, whatever the browser fingerprint. Call before first use.
func (r *Renderer) UseProxyPool(source ProxySource) {
	r.proxies = source
}

// detectIdentity describes the browser binary itself, so the UA and client hints name the
// version the page's JS engine actually is: a pooled "Chrome/146" UA on top of some other
// engine version is detectable through feature support alone.
func detectIdentity(execPath string) useragent.Profile {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, execPath, "--version").Output(); err == nil {
		if version, product, ok := parseBrowserVersion(string(out)); ok {
			return useragent.Native(version, product)
		}
	}
	return useragent.Native(useragent.Latest(), "")
}

var browserVersionPattern = regexp.MustCompile(`\b\d+\.\d+\.\d+\.\d+\b`)

// parseBrowserVersion reads `<binary> --version`, e.g. "Chromium 148.0.7778.96 Alpine
// Linux" or "Google Chrome 148.0.7778.97". Only Google's build carries the "Google Chrome"
// brand; a Chromium build's client hints list just Chromium and the GREASE entry.
func parseBrowserVersion(out string) (version, product string, ok bool) {
	version = browserVersionPattern.FindString(out)
	if version == "" {
		return "", "", false
	}
	if strings.Contains(out, "Google Chrome") {
		product = "Google Chrome"
	}
	return version, product, true
}

func (r *Renderer) allocatorOptions(profileDir string, headless, loadExtension bool, proxyServer string) []chromedp.ExecAllocatorOption {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(r.execPath),
		chromedp.UserDataDir(profileDir),
		chromedp.Flag("headless", headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-features", "VizDisplayCompositor,IsolateOrigins,site-per-process"),
		chromedp.Flag("window-size", "1920,1080"),
		chromedp.Flag("lang", "pl-PL"),
		// The CDP override in setupRealBrowser only covers the page target; this covers
		// workers. Same identity in both.
		chromedp.UserAgent(r.identity.UserAgent),
	)
	if loadExtension {
		opts = append(opts,
			// chromedp's defaults disable every extension. A false boolean removes
			// that switch while keeping all other safe allocator defaults.
			chromedp.Flag("disable-extensions", false),
			chromedp.Flag("disable-extensions-except", r.extensionDir),
			chromedp.Flag("load-extension", r.extensionDir),
		)
	}
	if proxyServer != "" {
		opts = append(opts,
			chromedp.ProxyServer(proxyServer),
			// WebRTC's UDP doesn't go through --proxy-server, so a page could otherwise
			// learn the server's real IP from a STUN request and compare it with the exit
			// IP its requests arrive from.
			chromedp.Flag("force-webrtc-ip-handling-policy", "disable_non_proxied_udp"),
		)
	}
	return append(opts, r.extraFlags...)
}

// exitFor picks the proxy a new browser for rawURL is launched through.
func (r *Renderer) exitFor(rawURL string) (server, user, password string, residential bool) {
	if r.proxies != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if picked, ok := r.proxies.PickResidentialForURL(ctx, rawURL); ok {
			// http:// CONNECT even for entries stored as socks5: Chromium can't answer a
			// SOCKS5 auth challenge (see proxypool.PickedProxy.HTTPURL).
			return "http://" + picked.Address, picked.Username, picked.Password, true
		}
	}
	return r.proxyServer, r.proxyUser, r.proxyPassword, false
}

// rotatedSince reports whether rawURL's host has been moved onto a new residential exit
// since session's browser was launched — see proxypool.Store.RotateResidentialSession.
func (r *Renderer) rotatedSince(session *browserSession, rawURL string) bool {
	return r.proxies != nil && proxypool.ResidentialGeneration(rawURL) != session.proxyGen
}

// timezoneFor matches the browser clock to the country its exit is pinned to (see
// proxypool's DataImpulse country targeting): a pl-PL browser on UTC — a server's zone — or
// on Warsaw time behind a Moscow IP is a cheap inconsistency to check.
func timezoneFor(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	switch {
	case strings.HasSuffix(host, ".ru"):
		return "Europe/Moscow"
	case strings.HasSuffix(host, ".by"):
		return "Europe/Minsk"
	}
	return "Europe/Warsaw"
}

func (r *Renderer) Close() {
	r.profileMu.Lock()
	for key, session := range r.sessions {
		session.cancel()
		delete(r.sessions, key)
	}
	r.profileMu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
}

var unsafeProfileChar = regexp.MustCompile(`[^a-zA-Z0-9.-]+`)

// acquire returns the browser session for rawURL's host, launching one if needed, plus
// the release func that must be called when done. The host's lock is held until release,
// so a session is only ever driven by one caller at a time. Callers run r.prepare(session)
// before navigating.
func (r *Renderer) acquire(rawURL string) (*browserSession, func(), error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, nil, fmt.Errorf("invalid profile URL")
	}
	key := unsafeProfileChar.ReplaceAllString(parsed.Hostname(), "_")
	legacy := usesLegacyRenderer(parsed.Hostname())
	persistent := r.persistent && !legacy
	r.profileMu.Lock()
	lock := r.profileLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		r.profileLocks[key] = lock
	}
	r.profileMu.Unlock()
	lock.Lock()
	profileDir := filepath.Join(r.profileRoot, key)
	if legacy {
		// Keep Zara's headless fingerprint isolated from profiles previously opened by
		// headed Chromium with the diagnostic extension. Reusing those cookies with a
		// different browser fingerprint is itself a strong anti-bot inconsistency.
		profileDir = filepath.Join(r.profileRoot, "legacy", key)
	}
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		lock.Unlock()
		return nil, nil, err
	}
	// Container hostnames change between deploys, so Chromium's singleton symlinks can
	// look like a profile owned by another computer after an unclean shutdown.
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		_ = os.Remove(filepath.Join(profileDir, name))
	}
	if persistent {
		r.profileMu.Lock()
		r.closeIdleSessionsLocked(key)
		if session := r.sessions[key]; session != nil {
			if session.ctx.Err() == nil && !r.rotatedSince(session, rawURL) {
				session.lastUsed = time.Now()
				r.profileMu.Unlock()
				return session, lock.Unlock, nil
			}
			// Either Chromium exited on its own (crash, process kill, failed startup) —
			// left in the map, its dead context would fail every later render for this
			// domain — or the domain was rotated onto a new exit since it was launched.
			session.cancel()
			delete(r.sessions, key)
		}
		r.profileMu.Unlock()
	}

	gen := proxypool.ResidentialGeneration(rawURL)
	server, user, password, residential := r.exitFor(rawURL)
	headless := legacy || !(r.headful || r.extensionDir != "")
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(r.rootCtx, r.allocatorOptions(profileDir, headless, r.extensionDir != "" && !legacy, server)...)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	// Start Chromium here, on the long-lived context. chromedp ties the browser process to
	// whatever context the *first* Run gets, and callers run with a per-render timeout —
	// so the "persistent" browser used to be killed as soon as its first render returned,
	// and every check cold-started a new one.
	if err := chromedp.Run(browserCtx); err != nil {
		cancelBrowser()
		cancelAlloc()
		lock.Unlock()
		return nil, nil, fmt.Errorf("start browser: %w", err)
	}
	session := &browserSession{ctx: browserCtx, cancel: func() {
		cancelBrowser()
		cancelAlloc()
	}, lastUsed: time.Now(), timezone: timezoneFor(parsed.Hostname()),
		proxyUser: user, proxyPassword: password, residential: residential, proxyGen: gen}
	if persistent {
		r.profileMu.Lock()
		if len(r.sessions) >= r.maxSessions {
			var oldestKey string
			var oldestTime time.Time
			for sessionKey, session := range r.sessions {
				if oldestKey == "" || session.lastUsed.Before(oldestTime) {
					oldestKey, oldestTime = sessionKey, session.lastUsed
				}
			}
			if oldest := r.sessions[oldestKey]; oldest != nil {
				oldest.cancel()
				delete(r.sessions, oldestKey)
			}
		}
		r.sessions[key] = session
		r.profileMu.Unlock()
		return session, lock.Unlock, nil
	}
	release := func() {
		closeCtx, closeCancel := context.WithTimeout(browserCtx, 3*time.Second)
		_ = chromedp.Cancel(closeCtx)
		closeCancel()
		cancelBrowser()
		cancelAlloc()
		lock.Unlock()
	}
	return session, release, nil
}

// sessionIdleTimeout closes persistent browsers nobody has rendered with for a while. Each
// one is a full Chromium process, and only bursts of checks against the same domain gain
// anything from keeping it warm; the profile directory keeps the cookies either way.
const sessionIdleTimeout = 10 * time.Minute

// closeIdleSessionsLocked must be called with profileMu held. keep is the host about to be
// used, which must not be closed out from under the caller. Renders finish well within the
// timeout, so a session idle that long isn't mid-render.
func (r *Renderer) closeIdleSessionsLocked(keep string) {
	for key, session := range r.sessions {
		if key != keep && time.Since(session.lastUsed) > sessionIdleTimeout {
			session.cancel()
			delete(r.sessions, key)
		}
	}
}

// prepare applies the identity and proxy auth to session's tab, once per Chromium. A
// persistent tab outlives every render, and redoing this per render used to stack another
// auth listener and another init script each time and re-randomize the identity mid-session
// — a new UA/platform on every check, presented alongside anti-bot cookies (Akamai _abck,
// DataDome) that were issued to the previous one.
func (r *Renderer) prepare(session *browserSession) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if session.prepared {
			return nil
		}
		if err := r.setupProxyAuth(session).Do(ctx); err != nil {
			return err
		}
		if err := setupRealBrowser(r.identity, session.timezone).Do(ctx); err != nil {
			return err
		}
		session.prepared = true
		return nil
	})
}

func usesLegacyRenderer(hostname string) bool {
	host := strings.ToLower(strings.TrimSuffix(hostname, "."))
	return host == "zara.com" || strings.HasSuffix(host, ".zara.com")
}

func (r *Renderer) Render(ctx context.Context, url string, waitTime time.Duration) (string, error) {
	session, release, err := r.acquire(url)
	if err != nil {
		return "", err
	}
	defer release()

	ctx, cancel := context.WithTimeout(session.ctx, 30*time.Second)
	defer cancel()

	// Images and video are most of a product page's bytes and none of its price, and on a
	// residential exit every byte is billed. Render only: FindPriceBlock's screenshot is
	// shown to the user and has to look like the page.
	if session.residential {
		session.skipMedia.Store(true)
		defer session.skipMedia.Store(false)
	}

	var html string
	tasks := []chromedp.Action{
		r.prepare(session),
		r.setCookiesForURL(url),
		chromedp.Navigate(url),
		chromedp.WaitReady("body", chromedp.ByQuery),
		acceptCookieBanners(),
		simulateUserActivity(),
		chromedp.Sleep(waitTime),
		chromedp.Evaluate(`document.documentElement.outerHTML`, &html),
	}

	if err := chromedp.Run(ctx, tasks...); err != nil {
		// A canceled parent means the persistent browser itself is gone. Do not cache
		// the poisoned session: the next tracker run must start a fresh Chromium.
		if errors.Is(session.ctx.Err(), context.Canceled) {
			r.evictSession(url, session.ctx)
		}
		return "", err
	}
	_ = r.saveProfileCookies(ctx, url)

	return html, nil
}

func (r *Renderer) evictSession(rawURL string, expected context.Context) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return
	}
	key := unsafeProfileChar.ReplaceAllString(parsed.Hostname(), "_")
	r.profileMu.Lock()
	defer r.profileMu.Unlock()
	if session := r.sessions[key]; session != nil && session.ctx == expected {
		session.cancel()
		delete(r.sessions, key)
	}
}

// Diagnostics inspects the persistent browser after a render. It is intentionally
// best-effort: a challenge page can time out while still leaving useful targets and
// cookies behind for troubleshooting.
func (r *Renderer) Diagnostics(rawURL string) (*BrowserDiagnostics, error) {
	session, release, err := r.acquire(rawURL)
	if err != nil {
		return nil, err
	}
	defer release()

	ctx, cancel := context.WithTimeout(session.ctx, 5*time.Second)
	defer cancel()
	out := &BrowserDiagnostics{
		PersistentBrowser: r.persistent,
		ExtensionEnabled:  r.extensionDir != "",
		AkamaiCookies:     []string{},
	}
	if targets, targetErr := chromedp.Targets(ctx); targetErr == nil {
		for _, target := range targets {
			if strings.HasPrefix(target.URL, "chrome-extension://") {
				out.ExtensionLoaded = true
				break
			}
		}
	}
	if !out.ExtensionLoaded && r.extensionDir != "" {
		parsed, _ := url.Parse(rawURL)
		key := unsafeProfileChar.ReplaceAllString(parsed.Hostname(), "_")
		out.ExtensionLoaded = extensionStarted(filepath.Join(r.profileRoot, key), r.extensionDir)
	}
	cookies, cookieErr := network.GetCookies().WithURLs([]string{rawURL}).Do(ctx)
	if cookieErr != nil {
		return out, nil
	}
	wanted := map[string]bool{"_abck": true, "ak_bmsc": true, "bm_sz": true, "bm_s": true, "bm_so": true, "bm_lso": true}
	for _, cookie := range cookies {
		if wanted[cookie.Name] {
			out.AkamaiCookies = append(out.AkamaiCookies, cookie.Name)
		}
	}
	return out, nil
}

func extensionStarted(profileDir, extensionDir string) bool {
	data, err := os.ReadFile(filepath.Join(profileDir, "Default", "Preferences"))
	if err != nil {
		return false
	}
	var prefs struct {
		Extensions struct {
			Settings map[string]struct {
				Path                    string            `json:"path"`
				DisableReasons          []json.RawMessage `json:"disable_reasons"`
				HasStartedServiceWorker bool              `json:"has_started_service_worker"`
			} `json:"settings"`
		} `json:"extensions"`
	}
	if json.Unmarshal(data, &prefs) != nil {
		return false
	}
	wanted := filepath.Clean(extensionDir)
	for _, extension := range prefs.Extensions.Settings {
		if filepath.Clean(extension.Path) == wanted && len(extension.DisableReasons) == 0 && extension.HasStartedServiceWorker {
			return true
		}
	}
	return false
}

func (r *Renderer) FindPriceBlock(ctx context.Context, url, price string, index int) (*PriceBlockCandidate, []byte, error) {
	session, release, err := r.acquire(url)
	if err != nil {
		return nil, nil, err
	}
	defer release()

	ctx, cancel := context.WithTimeout(session.ctx, 45*time.Second)
	defer cancel()

	var candidate PriceBlockCandidate
	var screenshot []byte
	priceJSON, _ := json.Marshal(price)

	waitForPriceScript := fmt.Sprintf(`(() => new Promise((resolve) => {
  const rawInput = String(%s).trim().toLowerCase();
  const normalizePrice = (value) => {
    let s = String(value || "")
      .toLowerCase()
      .replace(/\u00a0/g, " ")
      .replace(/руб\.?|р\.|pln|zł|eur|usd|gbp|rub|[€$£₽]/gi, "")
      .replace(/[^\d,.\s]/g, "")
      .trim();
    if (!/\d/.test(s)) return "";

    const spaceGroups = s.split(/\s+/).filter(Boolean);
    const hasDecimalSeparator = s.includes(",") || s.includes(".");
    if (!hasDecimalSeparator && spaceGroups.length > 1 && spaceGroups[spaceGroups.length - 1].length <= 2) {
      const fraction = spaceGroups.pop().padEnd(2, "0").slice(0, 2);
      const integer = spaceGroups.join("").replace(/^0+(?=\d)/, "") || "0";
      return integer + fraction;
    }

    s = s.replace(/\s+/g, "");
    const lastComma = s.lastIndexOf(",");
    const lastDot = s.lastIndexOf(".");
    const decimalIndex = Math.max(lastComma, lastDot);
    const decimalChar = decimalIndex >= 0 ? s[decimalIndex] : "";
    const fractionLength = decimalIndex >= 0 ? s.length - decimalIndex - 1 : 0;
    let integer = "";
    let fraction = "00";

    if (decimalChar && fractionLength > 0 && fractionLength <= 2) {
      integer = s.slice(0, decimalIndex).replace(/[^\d]/g, "");
      fraction = s.slice(decimalIndex + 1).replace(/[^\d]/g, "").padEnd(2, "0").slice(0, 2);
    } else {
      integer = s.replace(/[^\d]/g, "");
    }

    integer = integer.replace(/^0+(?=\d)/, "") || "0";
    return integer + fraction;
  };
  const target = normalizePrice(rawInput);
  const priceTokens = (text) => {
    const tokens = [];
    const re = /(?:[$€£₽]\s*)?\d[\d\s.,]*(?:\s*(?:zł|pln|eur|usd|gbp|rub|€|\$|£|₽))?/gi;
    let match;
    while ((match = re.exec(String(text || "")))) {
      const normalized = normalizePrice(match[0]);
      if (normalized) tokens.push(normalized);
    }
    return tokens;
  };
  const visible = (el) => {
    const style = window.getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    return style && style.visibility !== "hidden" && style.display !== "none" && rect.width > 0 && rect.height > 0;
  };
  const textOf = (el) => [
    el.innerText || "",
    el.textContent || "",
    el.getAttribute("aria-label") || "",
    el.getAttribute("title") || "",
    el.getAttribute("content") || "",
    el.getAttribute("data-price") || ""
  ].join(" ");
  // Sites built on web components (e.g. several major fashion retailers) render
  // price widgets inside shadow DOM, which querySelectorAll can't see into by
  // default; walk open shadow roots explicitly so those prices aren't invisible
  // to the scan.
  const deepElements = (root) => {
    const out = [];
    const stack = [root];
    while (stack.length) {
      const node = stack.pop();
      if (!node.querySelectorAll) continue;
      for (const el of node.querySelectorAll("*")) {
        out.push(el);
        if (el.shadowRoot) stack.push(el.shadowRoot);
      }
    }
    return out;
  };
  const found = () => {
    if (!target || !document.body) return false;
    for (const node of deepElements(document.body)) {
      if (!visible(node)) continue;
      const text = textOf(node);
      if (text && text.length <= 1200 && priceTokens(text).includes(target)) return true;
    }
    return false;
  };
  const started = Date.now();
  const tick = () => {
    if (found()) return resolve(true);
    if (Date.now() - started >= 18000) return resolve(false);
    setTimeout(tick, 500);
  };
  tick();
}))()`, string(priceJSON))

	script := fmt.Sprintf(`(() => {
  const rawInput = String(%s).trim().toLowerCase();
  const normalizePrice = (value) => {
    let s = String(value || "")
      .toLowerCase()
      .replace(/\u00a0/g, " ")
      .replace(/руб\.?|р\.|pln|zł|eur|usd|gbp|rub|[€$£₽]/gi, "")
      .replace(/[^\d,.\s]/g, "")
      .trim();
    if (!/\d/.test(s)) return "";

    const spaceGroups = s.split(/\s+/).filter(Boolean);
    const hasDecimalSeparator = s.includes(",") || s.includes(".");
    if (!hasDecimalSeparator && spaceGroups.length > 1 && spaceGroups[spaceGroups.length - 1].length <= 2) {
      const fraction = spaceGroups.pop().padEnd(2, "0").slice(0, 2);
      const integer = spaceGroups.join("").replace(/^0+(?=\d)/, "") || "0";
      return integer + fraction;
    }

    s = s.replace(/\s+/g, "");
    const lastComma = s.lastIndexOf(",");
    const lastDot = s.lastIndexOf(".");
    const decimalIndex = Math.max(lastComma, lastDot);
    const decimalChar = decimalIndex >= 0 ? s[decimalIndex] : "";
    const fractionLength = decimalIndex >= 0 ? s.length - decimalIndex - 1 : 0;
    let integer = "";
    let fraction = "00";

    if (decimalChar && fractionLength > 0 && fractionLength <= 2) {
      integer = s.slice(0, decimalIndex).replace(/[^\d]/g, "");
      fraction = s.slice(decimalIndex + 1).replace(/[^\d]/g, "").padEnd(2, "0").slice(0, 2);
    } else {
      integer = s.replace(/[^\d]/g, "");
    }

    integer = integer.replace(/^0+(?=\d)/, "") || "0";
    return integer + fraction;
  };
  const target = normalizePrice(rawInput);
  const priceTokens = (text) => {
    const tokens = [];
    const re = /(?:[$€£₽]\s*)?\d[\d\s.,]*(?:\s*(?:zł|pln|eur|usd|gbp|rub|€|\$|£|₽))?/gi;
    let match;
    while ((match = re.exec(String(text || "")))) {
      const normalized = normalizePrice(match[0]);
      if (normalized) tokens.push({ raw: match[0], normalized });
    }
    return tokens;
  };
  const visible = (el) => {
    const style = window.getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    return style && style.visibility !== "hidden" && style.display !== "none" && rect.width > 0 && rect.height > 0;
  };
  const textOf = (el) => [
    el.innerText || "",
    el.textContent || "",
    el.getAttribute("aria-label") || "",
    el.getAttribute("title") || "",
    el.getAttribute("content") || "",
    el.getAttribute("data-price") || "",
    el.getAttribute("data-testid") || "",
    el.getAttribute("class") || ""
  ].join(" ").replace(/\s+/g, " ").trim();
  const matches = (text) => {
    if (!target) return false;
    return priceTokens(text).some((token) => token.normalized === target);
  };
  const tokenIndex = (text) => {
    if (!target) return -1;
    return priceTokens(text).findIndex((token) => token.normalized === target);
  };
  // Builds a selector that can cross shadow-root boundaries, joining each
  // light/shadow tree's own path with " >>> " (mirrors the resolution logic in
  // TextBySelector). Needed because a plain CSS path can't be re-queried with
  // document.querySelector once it crosses into a shadow tree.
  const cssPath = (el) => {
    const segments = [];
    let current = el;
    while (current) {
      const parts = [];
      let node = current;
      while (node && node.nodeType === 1 && node !== document.body) {
        let part = node.tagName.toLowerCase();
        if (node.id) {
          part += "#" + CSS.escape(node.id);
          parts.unshift(part);
          node = null;
          break;
        }
        const cls = [...node.classList].slice(0, 2).map((c) => "." + CSS.escape(c)).join("");
        if (cls) part += cls;
        const parent = node.parentElement;
        if (parent) {
          const same = [...parent.children].filter((c) => c.tagName === node.tagName);
          if (same.length > 1) part += ":nth-of-type(" + (same.indexOf(node) + 1) + ")";
        }
        parts.unshift(part);
        node = parent;
      }
      segments.unshift(parts.join(" > "));
      const root = current.getRootNode();
      current = root && root.host ? root.host : null;
    }
    return segments.join(" >>> ");
  };
  const bestBlock = (el) => {
    let block = el;
    for (let i = 0; i < 5 && block.parentElement; i++) {
      const parent = block.parentElement;
      const parentText = textOf(parent);
      const rect = parent.getBoundingClientRect();
      if (parentText.length > 700 || rect.width > window.innerWidth * 0.96 || rect.height > window.innerHeight * 0.95) break;
      block = parent;
      if (rect.width >= 160 && rect.height >= 28) break;
    }
    return block;
  };
  const screenshotBlock = (el) => {
    const isTextAtom = (node) => {
      return ["SPAN", "P", "B", "STRONG", "EM", "SMALL"].includes(node.tagName);
    };

    const minDepth = isTextAtom(el) ? 3 : 1;
    let block = el;
    let best = null;

    for (let depth = 0; depth < 7 && block; depth++) {
      if (depth >= minDepth && visible(block)) {
        const text = textOf(block);
        const rect = block.getBoundingClientRect();
        const usable =
          text.length <= 1400 &&
          rect.width <= window.innerWidth * 0.98 &&
          rect.height <= window.innerHeight * 0.98 &&
          rect.width >= 180 &&
          rect.height >= 36;

        if (usable) {
          best = block;
          if (rect.width >= 260 && rect.height >= 64) break;
        }
      }

      block = block.parentElement;
    }

    if (best) return best;

    block = bestBlock(el);
    for (let i = 0; i < 3 && block.parentElement; i++) {
      const parent = block.parentElement;
      const parentText = textOf(parent);
      const rect = parent.getBoundingClientRect();
      if (parentText.length > 1200 || rect.width > window.innerWidth * 0.98 || rect.height > window.innerHeight * 0.98) break;
      block = parent;
      if (rect.width >= 260 && rect.height >= 72) break;
    }
    return block;
  };
  const blocks = [];
  const addBlock = (el, sourceText = "") => {
    if (!el || !visible(el)) return;
    const priceEl = el;
    const block = screenshotBlock(el);
    if (!visible(priceEl) || !visible(block)) return;
    const rect = block.getBoundingClientRect();
    if (rect.width > window.innerWidth * 0.96 || rect.height > window.innerHeight * 0.95) return;
    const priceText = (sourceText || textOf(priceEl)).replace(/\s+/g, " ").trim();
    const idx = tokenIndex(priceText);
    if (idx < 0) return;
    if (blocks.some((b) => b.priceEl === priceEl || b.block === block || b.block.contains(block) || block.contains(b.block))) return;
    blocks.push({ priceEl, block, priceText, tokenIndex: idx });
  };

  // document.body can be transiently null right after a client-side redirect
  // (e.g. a bot-challenge interstitial reloading the page) races with this
  // evaluation; bail out to "not found" instead of throwing.
  if (document.body) {
    // Walk document.body plus every open shadow root reachable from it, since
    // some sites render their price widgets inside shadow DOM, which a plain
    // TreeWalker/querySelectorAll rooted at document.body can't see into.
    const deepRoots = (root) => {
      const roots = [root];
      for (let i = 0; i < roots.length; i++) {
        for (const el of roots[i].querySelectorAll("*")) {
          if (el.shadowRoot) roots.push(el.shadowRoot);
        }
      }
      return roots;
    };

    for (const root of deepRoots(document.body)) {
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      let textNode;
      while ((textNode = walker.nextNode())) {
        const text = (textNode.nodeValue || "").trim();
        if (text && text.length <= 260 && matches(text)) addBlock(textNode.parentElement, text);
      }

      for (const node of root.querySelectorAll("*")) {
        if (!visible(node)) continue;
        const text = textOf(node);
        if (!text || text.length > 700) continue;
        if (matches(text)) addBlock(node, text);
      }
    }
  }
  const item = blocks[%d];
  if (!item) return {
    selector: "",
    screenshot_selector: "",
    text: (document.body?.innerText || document.body?.textContent || "").trim().slice(0, 500),
    price_text: "",
    price_token_index: -1,
    title: (document.querySelector("h1")?.innerText?.trim() || document.querySelector("h1")?.textContent?.trim() || document.title || "").trim(),
    total_found: blocks.length
  };
  item.priceEl.setAttribute("data-price-tracker-price-candidate", "selected");
  item.block.setAttribute("data-price-tracker-candidate", "selected");
  item.block.scrollIntoView({ block: "center", inline: "center" });
  return {
    selector: cssPath(item.priceEl),
    screenshot_selector: cssPath(item.block),
    text: (item.block.innerText || item.block.textContent || "").trim().slice(0, 500),
    price_text: item.priceText.slice(0, 500),
    price_token_index: item.tokenIndex,
    title: (document.querySelector("h1")?.innerText?.trim() || document.querySelector("h1")?.textContent?.trim() || document.title || "").trim(),
    total_found: blocks.length
  };
})()`, string(priceJSON), index)

	if err := chromedp.Run(ctx,
		r.prepare(session),
		r.setCookiesForURL(url),
		chromedp.Navigate(url),
		chromedp.WaitReady("body", chromedp.ByQuery),
		acceptCookieBanners(),
		simulateUserActivity(),
		chromedp.Evaluate(waitForPriceScript, nil),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Evaluate(script, &candidate),
	); err != nil {
		return nil, nil, err
	}
	_ = r.saveProfileCookies(ctx, url)
	if candidate.Selector == "" {
		return &candidate, nil, fmt.Errorf("price block not found; title=%q text=%q total_found=%d", candidate.Title, candidate.Text, candidate.TotalFound)
	}

	// The selected candidate can live inside a shadow root, which the plain
	// attribute-selector screenshot query below can't reach into (CDP's
	// DOM.querySelector doesn't pierce shadow boundaries). Fall back to a
	// full-page screenshot rather than failing the whole check just because
	// the preview image can't be cropped to the exact block.
	if err := chromedp.Run(ctx,
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Screenshot(`[data-price-tracker-candidate="selected"]`, &screenshot, chromedp.ByQuery),
	); err != nil {
		if err := chromedp.Run(ctx, chromedp.CaptureScreenshot(&screenshot)); err != nil {
			return nil, nil, err
		}
	}

	return &candidate, screenshot, nil
}

func (r *Renderer) TextBySelector(ctx context.Context, url, selector string) (string, error) {
	session, release, err := r.acquire(url)
	if err != nil {
		return "", err
	}
	defer release()

	ctx, cancel := context.WithTimeout(session.ctx, 45*time.Second)
	defer cancel()

	selectorJSON, _ := json.Marshal(selector)
	// A selector recorded by FindPriceBlock's cssPath may cross shadow-root
	// boundaries, joined with " >>> " — document.querySelector alone can't
	// resolve those, so walk each segment through the previous match's
	// shadowRoot. Selectors without " >>> " (the common case, and every
	// selector recorded before shadow DOM support was added) resolve exactly
	// as a plain querySelector would.
	//
	// This polls rather than reading once after a fixed sleep, since React/
	// Vue/Angular-driven price widgets can still be hydrating client-side well
	// after the initial page load — a fixed short sleep would read stale/empty
	// content on a slow render.
	script := fmt.Sprintf(`(() => new Promise((resolve) => {
  const selector = %s;
  const resolveText = () => {
    const segments = selector.split(" >>> ");
    let root = document;
    let el = null;
    for (let i = 0; i < segments.length; i++) {
      el = root.querySelector(segments[i]);
      if (!el) return "";
      if (i < segments.length - 1) {
        root = el.shadowRoot;
        if (!root) return "";
      }
    }
    return (el.innerText || el.textContent || "").trim();
  };
  const started = Date.now();
  const tick = () => {
    const text = resolveText();
    if (text) return resolve(text);
    if (Date.now() - started >= 12000) return resolve(text);
    setTimeout(tick, 500);
  };
  tick();
}))()`, string(selectorJSON))

	var text string
	if err := chromedp.Run(ctx,
		r.prepare(session),
		r.setCookiesForURL(url),
		chromedp.Navigate(url),
		chromedp.WaitReady("body", chromedp.ByQuery),
		acceptCookieBanners(),
		simulateUserActivity(),
		// The polling script returns a Promise. Without AwaitPromise CDP returns
		// the Promise object itself, which chromedp then tries (and fails) to
		// unmarshal into the string above.
		chromedp.Evaluate(script, &text, func(params *runtime.EvaluateParams) *runtime.EvaluateParams {
			return params.WithAwaitPromise(true)
		}),
	); err != nil {
		return "", err
	}
	_ = r.saveProfileCookies(ctx, url)
	if text == "" {
		return "", fmt.Errorf("selector not found: %s", selector)
	}

	return text, nil
}

func normalizeProxyURL(raw string) (server, username, password string) {
	if raw == "" {
		return "", "", ""
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw, "", ""
	}

	if parsed.User != nil {
		username = parsed.User.Username()
		password, _ = parsed.User.Password()
		parsed.User = nil
	}

	return parsed.String(), username, password
}

// setupProxyAuth answers the proxy's auth challenge with the credentials of the exit
// session was launched through, for the session's whole lifetime (not the calling
// render's timeout context — a listener tied to that would be dropped after the first
// render, while Fetch stays enabled on the persistent tab).
//
// Fetch.enable without patterns pauses *every* request until someone continues it, not
// just the ones that hit an auth challenge, so the listener has to release paused
// requests too; handling only EventAuthRequired left each page load hanging until the
// render timed out whenever SCRAPER_PROXY_URL carried credentials. Since every request
// passes through here anyway, it's also where Render's media skipping happens.
func (r *Renderer) setupProxyAuth(session *browserSession) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if session.proxyUser == "" {
			return nil
		}

		browserCtx := session.ctx
		chromedp.ListenTarget(browserCtx, func(ev interface{}) {
			switch ev := ev.(type) {
			case *fetch.EventRequestPaused:
				go func() {
					if session.skipMedia.Load() && (ev.ResourceType == network.ResourceTypeImage || ev.ResourceType == network.ResourceTypeMedia) {
						_ = chromedp.Run(browserCtx, fetch.FailRequest(ev.RequestID, network.ErrorReasonBlockedByClient))
						return
					}
					_ = chromedp.Run(browserCtx, fetch.ContinueRequest(ev.RequestID))
				}()
			case *fetch.EventAuthRequired:
				go func() {
					_ = chromedp.Run(browserCtx, fetch.ContinueWithAuth(ev.RequestID, &fetch.AuthChallengeResponse{
						Response: fetch.AuthChallengeResponseResponseProvideCredentials,
						Username: session.proxyUser,
						Password: session.proxyPassword,
					}))
				}()
			}
		})

		return fetch.Enable().WithHandleAuthRequests(true).Do(ctx)
	})
}

// setupRealBrowser applies the browser identity consistently across every layer the page
// can inspect, leaving Chromium itself to generate the request headers. In headful mode
// this restates what Chromium would report anyway; in headless mode it hides the
// "HeadlessChrome" UA and brand.
//
// The User-Agent Client Hints go in as UA metadata rather than hand-set Sec-CH-UA*
// headers: overriding the UA without metadata makes Chromium report empty
// navigator.userAgentData brands while our headers claimed "Google Chrome", and extra
// HTTP headers apply to every subresource — which is why the document-only headers that
// used to be set here (Accept: text/html, Sec-Fetch-Mode: navigate, Upgrade-Insecure-
// Requests, ...) went out on images, scripts and XHRs too, and Cache-Control/Pragma
// turned cross-origin API calls into CORS preflights. The platform override is the
// navigator.platform value ("Win32"), not the client-hint name ("Windows").
//
// No init script either: --disable-blink-features=AutomationControlled already makes
// navigator.webdriver false like a real Chrome (forcing it to undefined was itself a tell),
// a fake [1..5] plugins array is a well-known stealth-plugin signature, and accept-language
// plus the platform override cover navigator.languages and navigator.platform natively.
func setupRealBrowser(profile useragent.Profile, timezone string) chromedp.Action {
	brands := make([]*emulation.UserAgentBrandVersion, 0, len(profile.Brands))
	fullVersions := make([]*emulation.UserAgentBrandVersion, 0, len(profile.Brands))
	for _, b := range profile.Brands {
		brands = append(brands, &emulation.UserAgentBrandVersion{Brand: b.Name, Version: b.Version})
		full := b.Version + ".0.0.0"
		if strings.HasPrefix(profile.FullVersion, b.Version+".") {
			full = profile.FullVersion
		}
		fullVersions = append(fullVersions, &emulation.UserAgentBrandVersion{Brand: b.Name, Version: full})
	}

	return chromedp.Tasks{
		network.Enable(),
		emulation.SetUserAgentOverride(profile.UserAgent).
			WithAcceptLanguage(acceptLanguages).
			WithPlatform(profile.NavigatorPlatform).
			WithUserAgentMetadata(&emulation.UserAgentMetadata{
				Brands:          brands,
				FullVersionList: fullVersions,
				Platform:        profile.CDPPlatform,
				PlatformVersion: profile.CHPlatformVersion,
				Architecture:    profile.Architecture,
				Bitness:         "64",
				FormFactors:     []string{"Desktop"},
			}),
		emulation.SetLocaleOverride().WithLocale("pl-PL"),
		emulation.SetTimezoneOverride(timezone),
	}
}

func (r *Renderer) setCookies() chromedp.Action {
	return r.setCookieList(r.cookies)
}

func (r *Renderer) setCookiesForURL(rawURL string) chromedp.Action {
	cookies := append([]scraper.Cookie{}, r.cookies...)
	if data, err := os.ReadFile(r.profileCookiePath(rawURL)); err == nil {
		var jar scraper.CookieJar
		if json.Unmarshal(data, &jar) == nil {
			cookies = append(cookies, jar.Cookies...)
		}
	}
	return r.setCookieList(cookies)
}

func (r *Renderer) setCookieList(cookies []scraper.Cookie) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if len(cookies) == 0 {
			return nil
		}

		params := make([]*network.CookieParam, 0, len(cookies))
		for _, cookie := range cookies {
			param := &network.CookieParam{
				Name:     cookie.Name,
				Value:    cookie.Value,
				Domain:   cookie.Domain,
				Path:     cookie.Path,
				Secure:   cookie.Secure,
				HTTPOnly: cookie.HTTPOnly,
			}
			if !cookie.Session && cookie.ExpirationDate > 0 {
				expires := cdp.TimeSinceEpoch(time.Unix(int64(cookie.ExpirationDate), 0))
				param.Expires = &expires
			}
			switch cookie.SameSite {
			case "lax":
				param.SameSite = network.CookieSameSiteLax
			case "strict":
				param.SameSite = network.CookieSameSiteStrict
			case "no_restriction", "none":
				param.SameSite = network.CookieSameSiteNone
			}
			params = append(params, param)
		}

		if err := network.SetCookies(params).Do(ctx); err != nil {
			return errors.New("set scraper cookies: " + err.Error())
		}
		return nil
	})
}

func (r *Renderer) profileCookiePath(rawURL string) string {
	parsed, _ := url.Parse(rawURL)
	key := unsafeProfileChar.ReplaceAllString(parsed.Hostname(), "_")
	return filepath.Join(r.profileRoot, key, "pricebot-cookies.json")
}

func (r *Renderer) saveProfileCookies(ctx context.Context, rawURL string) error {
	current, err := network.GetCookies().WithURLs([]string{rawURL}).Do(ctx)
	if err != nil {
		return err
	}
	jar := scraper.CookieJar{Cookies: make([]scraper.Cookie, 0, len(current))}
	for _, cookie := range current {
		jar.Cookies = append(jar.Cookies, scraper.Cookie{
			Domain: cookie.Domain, ExpirationDate: cookie.Expires, HTTPOnly: cookie.HTTPOnly,
			Name: cookie.Name, Path: cookie.Path, SameSite: strings.ToLower(cookie.SameSite.String()),
			Secure: cookie.Secure, Session: cookie.Session, Value: cookie.Value,
		})
	}
	data, err := json.Marshal(jar)
	if err != nil {
		return err
	}
	return os.WriteFile(r.profileCookiePath(rawURL), data, 0600)
}

func acceptCookieBanners() chromedp.Action {
	return chromedp.Evaluate(`(() => {
  const labels = [
    "akceptuję", "akceptuje", "zaakceptuj", "zgadzam", "accept", "agree",
    "i agree", "allow all", "accept all", "przejdź do serwisu"
  ];
  const candidates = [...document.querySelectorAll("button, a, [role='button'], input[type='button'], input[type='submit']")];
  for (const el of candidates) {
    const text = ((el.innerText || el.textContent || el.value || el.getAttribute("aria-label") || "") + "").trim().toLowerCase();
    if (!text) continue;
    if (labels.some((label) => text.includes(label))) {
      el.click();
      return true;
    }
  }
  return false;
})()`, nil)
}

func simulateUserActivity() chromedp.Action {
	return chromedp.Evaluate(`new Promise((resolve) => {
  window.scrollTo({ top: Math.floor(window.innerHeight * 0.35), behavior: "smooth" });
  setTimeout(() => {
    window.scrollTo({ top: 0, behavior: "smooth" });
    resolve(true);
  }, 700);
})`, nil)
}
