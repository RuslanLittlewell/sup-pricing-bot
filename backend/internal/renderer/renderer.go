package renderer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/littlewell/price-tracker/internal/scraper"
	"github.com/littlewell/price-tracker/internal/useragent"
)

const acceptLanguage = "pl-PL,pl;q=0.9,en-US;q=0.8,en;q=0.7"

type Renderer struct {
	rootCtx       context.Context
	cancel        context.CancelFunc
	cookies       []scraper.Cookie
	proxyServer   string
	proxyUser     string
	proxyPassword string
	profileRoot   string
	profileMu     sync.Mutex
	profileLocks  map[string]*sync.Mutex
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
	rootCtx, cancel := context.WithCancel(context.Background())
	return &Renderer{rootCtx: rootCtx, cancel: cancel, cookies: cookies, proxyServer: proxyServer, proxyUser: proxyUser, proxyPassword: proxyPassword, profileRoot: profileRoot, profileLocks: map[string]*sync.Mutex{}}, nil
}

func (r *Renderer) allocatorOptions(profileDir string) []chromedp.ExecAllocatorOption {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath("/usr/bin/chromium"),
		chromedp.UserDataDir(profileDir),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-features", "VizDisplayCompositor,IsolateOrigins,site-per-process"),
		chromedp.Flag("window-size", "1920,1080"),
		chromedp.Flag("lang", "pl-PL"),
		chromedp.UserAgent(useragent.Random().UserAgent),
	)
	if r.proxyServer != "" {
		opts = append(opts, chromedp.ProxyServer(r.proxyServer))
	}

	return opts
}

func (r *Renderer) Close() {
	if r.cancel != nil {
		r.cancel()
	}
}

var unsafeProfileChar = regexp.MustCompile(`[^a-zA-Z0-9.-]+`)

func (r *Renderer) newProfileContext(rawURL string) (context.Context, context.CancelFunc, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, nil, fmt.Errorf("invalid profile URL")
	}
	key := unsafeProfileChar.ReplaceAllString(parsed.Hostname(), "_")
	r.profileMu.Lock()
	lock := r.profileLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		r.profileLocks[key] = lock
	}
	r.profileMu.Unlock()
	lock.Lock()
	profileDir := filepath.Join(r.profileRoot, key)
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		lock.Unlock()
		return nil, nil, err
	}
	// Container hostnames change between deploys, so Chromium's singleton symlinks can
	// look like a profile owned by another computer after an unclean shutdown.
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		_ = os.Remove(filepath.Join(profileDir, name))
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(r.rootCtx, r.allocatorOptions(profileDir)...)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	cancel := func() {
		closeCtx, closeCancel := context.WithTimeout(browserCtx, 3*time.Second)
		_ = chromedp.Cancel(closeCtx)
		closeCancel()
		cancelBrowser()
		cancelAlloc()
		lock.Unlock()
	}
	return browserCtx, cancel, nil
}

func (r *Renderer) Render(ctx context.Context, url string, waitTime time.Duration) (string, error) {
	ctx, cancel, err := r.newProfileContext(url)
	if err != nil {
		return "", err
	}
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var html string
	tasks := []chromedp.Action{
		r.setupProxyAuth(),
		setupRealBrowser(),
		r.setCookiesForURL(url),
		chromedp.Navigate(url),
		chromedp.WaitReady("body", chromedp.ByQuery),
		acceptCookieBanners(),
		simulateUserActivity(),
		chromedp.Sleep(waitTime),
		chromedp.Evaluate(`document.documentElement.outerHTML`, &html),
	}

	if err := chromedp.Run(ctx, tasks...); err != nil {
		return "", err
	}
	_ = r.saveProfileCookies(ctx, url)

	return html, nil
}

func (r *Renderer) FindPriceBlock(ctx context.Context, url, price string, index int) (*PriceBlockCandidate, []byte, error) {
	ctx, cancel, err := r.newProfileContext(url)
	if err != nil {
		return nil, nil, err
	}
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 45*time.Second)
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
		r.setupProxyAuth(),
		setupRealBrowser(),
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
	ctx, cancel, err := r.newProfileContext(url)
	if err != nil {
		return "", err
	}
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 45*time.Second)
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
		r.setupProxyAuth(),
		setupRealBrowser(),
		r.setCookiesForURL(url),
		chromedp.Navigate(url),
		chromedp.WaitReady("body", chromedp.ByQuery),
		acceptCookieBanners(),
		simulateUserActivity(),
		chromedp.Evaluate(script, &text),
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

func (r *Renderer) setupProxyAuth() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if r.proxyUser == "" {
			return nil
		}

		chromedp.ListenTarget(ctx, func(ev interface{}) {
			authEvent, ok := ev.(*fetch.EventAuthRequired)
			if !ok {
				return
			}
			go func() {
				_ = fetch.ContinueWithAuth(authEvent.RequestID, &fetch.AuthChallengeResponse{
					Response: fetch.AuthChallengeResponseResponseProvideCredentials,
					Username: r.proxyUser,
					Password: r.proxyPassword,
				}).Do(ctx)
			}()
		})

		return fetch.Enable().WithHandleAuthRequests(true).Do(ctx)
	})
}

// setupRealBrowser picks a random browser fingerprint profile and applies it
// consistently across every layer the page can inspect: the Sec-CH-UA* headers, the CDP
// User-Agent/platform override, and the navigator.platform JS property. A fresh profile
// is picked on every call (i.e. every Render/FindPriceBlock/TextBySelector invocation),
// so successive checks of the same page don't repeat one fixed fingerprint.
func setupRealBrowser() chromedp.Action {
	profile := useragent.Random()

	headers := network.Headers{
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
		"Accept-Language":           acceptLanguage,
		"Cache-Control":             "no-cache",
		"Pragma":                    "no-cache",
		"Sec-CH-UA":                 profile.SecCHUA,
		"Sec-CH-UA-Mobile":          "?0",
		"Sec-CH-UA-Platform":        profile.SecCHUAPlatform,
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "none",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
	}

	return chromedp.Tasks{
		network.Enable(),
		network.SetExtraHTTPHeaders(headers),
		emulation.SetUserAgentOverride(profile.UserAgent).
			WithAcceptLanguage(acceptLanguage).
			WithPlatform(profile.CDPPlatform),
		emulation.SetLocaleOverride().WithLocale("pl-PL"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			script := fmt.Sprintf(`
Object.defineProperty(navigator, "webdriver", { get: () => undefined });
Object.defineProperty(navigator, "languages", { get: () => ["pl-PL", "pl", "en-US", "en"] });
Object.defineProperty(navigator, "platform", { get: () => %q });
Object.defineProperty(navigator, "plugins", { get: () => [1, 2, 3, 4, 5] });
window.chrome = window.chrome || { runtime: {} };
const originalQuery = window.navigator.permissions && window.navigator.permissions.query;
if (originalQuery) {
  window.navigator.permissions.query = (parameters) => (
    parameters && parameters.name === "notifications"
      ? Promise.resolve({ state: Notification.permission })
      : originalQuery(parameters)
  );
}
`, profile.NavigatorPlatform)
			_, err := page.AddScriptToEvaluateOnNewDocument(script).Do(ctx)
			return err
		}),
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
