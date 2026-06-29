package renderer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/littlewell/price-tracker/internal/scraper"
)

const browserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

const acceptLanguage = "pl-PL,pl;q=0.9,en-US;q=0.8,en;q=0.7"

type Renderer struct {
	allocCtx context.Context
	cancel   context.CancelFunc
	cookies  []scraper.Cookie
}

type PriceBlockCandidate struct {
	Selector   string `json:"selector"`
	Text       string `json:"text"`
	Title      string `json:"title"`
	TotalFound int    `json:"total_found"`
}

func New(cookiesFile string) (*Renderer, error) {
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

	cookies, err := scraper.LoadCookies(cookiesFile)
	if err != nil {
		return nil, err
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	return &Renderer{allocCtx: allocCtx, cancel: cancel, cookies: cookies}, nil
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
  const target = rawInput.replace(/[^\d]/g, "");
  const variants = new Set([rawInput]);
  if (/^\d+$/.test(rawInput)) {
    variants.add(rawInput + ",00");
    variants.add(rawInput + ".00");
    variants.add(rawInput + " 00");
  }
  if (target.length >= 2) variants.add(target);
  if (target.endsWith("00") && target.length > 2) variants.add(target.slice(0, -2));
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
    const lower = String(text || "").toLowerCase();
    const digits = lower.replace(/[^\d]/g, "");
    for (const variant of variants) {
      if (!variant) continue;
      if (lower.includes(variant)) return true;
      const variantDigits = String(variant).replace(/[^\d]/g, "");
      if (variantDigits.length >= 2 && digits.includes(variantDigits)) return true;
    }
    return target.length >= 2 && digits.includes(target);
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
  const blocks = [];
  const addBlock = (el) => {
    if (!el || !visible(el)) return;
    const block = bestBlock(el);
    if (!visible(block)) return;
    const rect = block.getBoundingClientRect();
    if (rect.width > window.innerWidth * 0.96 || rect.height > window.innerHeight * 0.95) return;
    if (blocks.some((b) => b === block || b.contains(block) || block.contains(b))) return;
    blocks.push(block);
  };

  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  let textNode;
  while ((textNode = walker.nextNode())) {
    const text = (textNode.nodeValue || "").trim();
    if (text && text.length <= 260 && matches(text)) addBlock(textNode.parentElement);
  }

  for (const node of document.querySelectorAll("body *")) {
    if (!visible(node)) continue;
    const text = textOf(node);
    if (!text || text.length > 700) continue;
    if (matches(text)) addBlock(node);
  }
  const block = blocks[%d];
  if (!block) return {
    selector: "",
    text: (document.body?.innerText || document.body?.textContent || "").trim().slice(0, 500),
    title: document.title || "",
    total_found: blocks.length
  };
  block.setAttribute("data-price-tracker-candidate", "selected");
  block.scrollIntoView({ block: "center", inline: "center" });
  return {
    selector: cssPath(block),
    text: (block.innerText || block.textContent || "").trim().slice(0, 500),
    title: document.title || "",
    total_found: blocks.length
  };
})()`, string(priceJSON), index)

	if err := chromedp.Run(ctx,
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
