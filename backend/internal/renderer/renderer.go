package renderer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

type Renderer struct {
	allocCtx context.Context
	cancel   context.CancelFunc
}

type PriceBlockCandidate struct {
	Selector   string `json:"selector"`
	Text       string `json:"text"`
	Title      string `json:"title"`
	TotalFound int    `json:"total_found"`
}

func New() (*Renderer, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath("/usr/bin/chromium"),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-web-security", true),
		chromedp.Flag("disable-features", "VizDisplayCompositor"),
		chromedp.Flag("window-size", "1920,1080"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	return &Renderer{allocCtx: allocCtx, cancel: cancel}, nil
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
		chromedp.Navigate(url),
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
		chromedp.Navigate(url),
		chromedp.WaitReady("body", chromedp.ByQuery),
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
		chromedp.Navigate(url),
		chromedp.Sleep(3*time.Second),
		chromedp.Text(selector, &text, chromedp.ByQuery),
	); err != nil {
		return "", err
	}

	return text, nil
}
