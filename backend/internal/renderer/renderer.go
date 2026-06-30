package renderer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/littlewell/price-tracker/internal/scraper"
)

const browserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

const acceptLanguage = "pl-PL,pl;q=0.9,en-US;q=0.8,en;q=0.7"

type Renderer struct {
	allocCtx      context.Context
	cancel        context.CancelFunc
	cookies       []scraper.Cookie
	proxyUser     string
	proxyPassword string
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
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath("/usr/bin/chromium"),
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
		chromedp.UserAgent(browserUserAgent),
	)
	if proxyServer != "" {
		opts = append(opts, chromedp.ProxyServer(proxyServer))
	}

	cookies, err := scraper.LoadCookies(cookiesFile)
	if err != nil {
		return nil, err
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	return &Renderer{allocCtx: allocCtx, cancel: cancel, cookies: cookies, proxyUser: proxyUser, proxyPassword: proxyPassword}, nil
}

func (r *Renderer) Close() {
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *Renderer) Render(ctx context.Context, url string, waitTime time.Duration) (string, error) {
	ctx, cancel := chromedp.NewContext(r.allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var html string
	tasks := []chromedp.Action{
		r.setupProxyAuth(),
		setupRealBrowser(),
		r.setCookies(),
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

	return html, nil
}

func (r *Renderer) FindPriceBlock(ctx context.Context, url, price string, index int) (*PriceBlockCandidate, []byte, error) {
	ctx, cancel := chromedp.NewContext(r.allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	var candidate PriceBlockCandidate
	var screenshot []byte
	priceJSON, _ := json.Marshal(price)

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
  const cssPath = (el) => {
    const parts = [];
    while (el && el.nodeType === 1 && el !== document.body) {
      let part = el.tagName.toLowerCase();
      if (el.id) {
        part += "#" + CSS.escape(el.id);
        parts.unshift(part);
        break;
      }
      const cls = [...el.classList].slice(0, 2).map((c) => "." + CSS.escape(c)).join("");
      if (cls) part += cls;
      const parent = el.parentElement;
      if (parent) {
        const same = [...parent.children].filter((c) => c.tagName === el.tagName);
        if (same.length > 1) part += ":nth-of-type(" + ([...parent.children].filter((c) => c.tagName === el.tagName).indexOf(el) + 1) + ")";
      }
      parts.unshift(part);
      el = parent;
    }
    return parts.join(" > ");
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

  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  let textNode;
  while ((textNode = walker.nextNode())) {
    const text = (textNode.nodeValue || "").trim();
    if (text && text.length <= 260 && matches(text)) addBlock(textNode.parentElement, text);
  }

  for (const node of document.querySelectorAll("body *")) {
    if (!visible(node)) continue;
    const text = textOf(node);
    if (!text || text.length > 700) continue;
    if (matches(text)) addBlock(node, text);
  }
  const item = blocks[%d];
  if (!item) return {
    selector: "",
    screenshot_selector: "",
    text: (document.body?.innerText || document.body?.textContent || "").trim().slice(0, 500),
    price_text: "",
    price_token_index: -1,
    title: document.title || "",
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
    title: document.title || "",
    total_found: blocks.length
  };
})()`, string(priceJSON), index)

	if err := chromedp.Run(ctx,
		r.setupProxyAuth(),
		setupRealBrowser(),
		r.setCookies(),
		chromedp.Navigate(url),
		chromedp.WaitReady("body", chromedp.ByQuery),
		acceptCookieBanners(),
		simulateUserActivity(),
		chromedp.Sleep(6*time.Second),
		chromedp.Evaluate(script, &candidate),
	); err != nil {
		return nil, nil, err
	}
	if candidate.Selector == "" {
		return &candidate, nil, fmt.Errorf("price block not found; title=%q text=%q total_found=%d", candidate.Title, candidate.Text, candidate.TotalFound)
	}

	if err := chromedp.Run(ctx,
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Screenshot(`[data-price-tracker-candidate="selected"]`, &screenshot, chromedp.ByQuery),
	); err != nil {
		return nil, nil, err
	}

	return &candidate, screenshot, nil
}

func (r *Renderer) TextBySelector(ctx context.Context, url, selector string) (string, error) {
	ctx, cancel := chromedp.NewContext(r.allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	var text string
	if err := chromedp.Run(ctx,
		r.setupProxyAuth(),
		setupRealBrowser(),
		r.setCookies(),
		chromedp.Navigate(url),
		chromedp.WaitReady("body", chromedp.ByQuery),
		acceptCookieBanners(),
		simulateUserActivity(),
		chromedp.Sleep(3*time.Second),
		chromedp.Text(selector, &text, chromedp.ByQuery),
	); err != nil {
		return "", err
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

func setupRealBrowser() chromedp.Action {
	headers := network.Headers{
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
		"Accept-Language":           acceptLanguage,
		"Cache-Control":             "no-cache",
		"Pragma":                    "no-cache",
		"Sec-CH-UA":                 `"Chromium";v="126", "Google Chrome";v="126", "Not-A.Brand";v="99"`,
		"Sec-CH-UA-Mobile":          "?0",
		"Sec-CH-UA-Platform":        `"Windows"`,
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "none",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
	}

	return chromedp.Tasks{
		network.Enable(),
		network.SetExtraHTTPHeaders(headers),
		emulation.SetUserAgentOverride(browserUserAgent).
			WithAcceptLanguage(acceptLanguage).
			WithPlatform("Windows"),
		emulation.SetLocaleOverride().WithLocale("pl-PL"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(`
Object.defineProperty(navigator, "webdriver", { get: () => undefined });
Object.defineProperty(navigator, "languages", { get: () => ["pl-PL", "pl", "en-US", "en"] });
Object.defineProperty(navigator, "platform", { get: () => "Win32" });
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
`).Do(ctx)
			return err
		}),
	}
}

func (r *Renderer) setCookies() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if len(r.cookies) == 0 {
			return nil
		}

		params := make([]*network.CookieParam, 0, len(r.cookies))
		for _, cookie := range r.cookies {
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
