package shops

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// HMStockMethod is the extraction_method recorded for a "hm_size" tracker — H&M doesn't
// publish a JSON-LD variant block like Zara; per-size availability instead comes from the
// accessibility markup of its rendered size picker (see ParseHMSizes).
const HMStockMethod = "hm_aria_label"

// IsHMURL reports whether rawURL points at hm.com (any locale/subdomain, e.g. the
// www2.hm.com host most storefronts actually serve from).
func IsHMURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "hm.com" || strings.HasSuffix(host, ".hm.com")
}

// HMSizeVariant is one size option of an H&M product. Unlike Zara, H&M exposes no
// structured (JSON-LD) per-size availability — Label carries the size button's raw
// aria-label text (e.g. "Taglia XS: esaurita. Seleziona per vedere prodotti simili...")
// for the caller to run through extractor.ContainsOutOfStockPhrase, the same
// locale-aware phrase list used for whole-page detection.
type HMSizeVariant struct {
	Size  string
	Label string
}

// hmSizeButtonTestID matches the data-testid H&M puts on each size option in its picker
// (e.g. "sizeButton-0"), confirmed against a live it_it product page.
var hmSizeButtonTestID = regexp.MustCompile(`^sizeButton-\d+$`)

// ParseHMSizes reads H&M's size-picker markup and returns one entry per size. H&M
// server-renders the picker (Next.js), so this works off a plain fetched page body same as
// the other extractors here — no separate headless-render step required.
func ParseHMSizes(htmlContent []byte) ([]HMSizeVariant, error) {
	doc, err := html.Parse(strings.NewReader(string(htmlContent)))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	var variants []HMSizeVariant
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			var testID, ariaLabel string
			for _, attr := range n.Attr {
				switch attr.Key {
				case "data-testid":
					testID = attr.Val
				case "aria-label":
					ariaLabel = attr.Val
				}
			}
			if hmSizeButtonTestID.MatchString(testID) {
				size := strings.TrimSpace(strings.ReplaceAll(nodeText(n), " ", " "))
				if size != "" {
					variants = append(variants, HMSizeVariant{Size: size, Label: ariaLabel})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if len(variants) == 0 {
		return nil, fmt.Errorf("no size data found on page")
	}
	return variants, nil
}

// HasHMSizePicker reports whether body is really an H&M product page. H&M sits behind
// Akamai, which serves short "Access Denied"/interstitial pages that don't always carry a
// recognizable challenge marker. Such a page has no out-of-stock wording either, so a plain
// keyword scan reads it as in_stock and fires a false "back in stock" — the failure mode
// this guards. Every real PDP renders a size picker, so its absence means we never got the
// product page and the body carries no usable stock signal at all.
func HasHMSizePicker(body []byte) bool {
	_, err := ParseHMSizes(body)
	return err == nil
}
