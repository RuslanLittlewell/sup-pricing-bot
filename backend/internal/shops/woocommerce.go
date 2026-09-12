package shops

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// WooCommerceMethod is the extraction_method recorded for prices and stock statuses that
// came from a store's own WooCommerce Store API rather than from parsing its HTML.
const WooCommerceMethod = "woocommerce_store_api"

// wooStoreAPIPath is the Store API's products collection, relative to the WordPress REST
// API root. It is public and unauthenticated by design (the block-based cart/checkout in
// every modern WooCommerce install reads from it), which is what makes it usable here.
const wooStoreAPIPath = "wc/store/v1/products"

// wpAPIRootRe-free detection: the WordPress REST API advertises its own root through a
// <link rel="https://api.w.org/" href="..."> element in the document head. Reading the
// href rather than assuming "/wp-json/" is what makes this work on installs that live in
// a subdirectory, sit behind a custom rest_url filter, or have pretty permalinks off (in
// which case the root is "/?rest_route=/" and "/wp-json/" 404s).
const wpAPIRootRel = `https://api.w.org/`

// wooCommerceMarkers are strings WooCommerce's own frontend assets put on every
// storefront page. Any one of them is enough: they name the plugin's asset directory or
// the JS globals its cart scripts define, none of which appear on non-WooCommerce sites.
var wooCommerceMarkers = []string{
	"/wp-content/plugins/woocommerce",
	"woocommerce-product",
	"woocommerce_params",
	"wc_add_to_cart_params",
}

// WPAPIRoot returns the WordPress REST API root advertised by the page, or "" when the
// page does not advertise one (not WordPress, or the discovery link was filtered out).
// The returned root always ends in "/", so callers can join a path onto it directly.
func WPAPIRoot(body []byte) string {
	html := string(body)
	idx := strings.Index(html, wpAPIRootRel)
	if idx < 0 {
		return ""
	}
	// Walk forward to this <link>'s href. Bounded to the remainder of the element so a
	// missing href can't run on and pick up an unrelated later tag's URL.
	rest := html[idx:]
	if end := strings.Index(rest, ">"); end >= 0 {
		rest = rest[:end]
	}
	hrefIdx := strings.Index(rest, `href=`)
	if hrefIdx < 0 {
		return ""
	}
	rest = rest[hrefIdx+len(`href=`):]
	if len(rest) == 0 {
		return ""
	}
	quote := rest[0]
	if quote != '"' && quote != '\'' {
		return ""
	}
	rest = rest[1:]
	end := strings.IndexByte(rest, quote)
	if end < 0 {
		return ""
	}
	root := strings.TrimSpace(rest[:end])
	if root == "" {
		return ""
	}
	parsed, err := url.Parse(root)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	if !strings.HasSuffix(root, "/") {
		root += "/"
	}
	return root
}

// IsWooCommercePage reports whether a fetched page is a WooCommerce storefront page.
// Content-based rather than domain-based on purpose: WooCommerce powers a long tail of
// independent shops that will never be worth a hand-written parser, and this is what
// lets one Store API path serve all of them.
func IsWooCommercePage(body []byte) bool {
	html := string(body)
	for _, marker := range wooCommerceMarkers {
		if strings.Contains(html, marker) {
			return true
		}
	}
	return false
}

// WooProductSlug pulls the product slug out of a storefront URL. WooCommerce product
// permalinks end in the slug regardless of the prefix a shop chose for them ("/product/",
// "/produkt/", "/shop/...", or no prefix at all), so the last non-empty path segment is
// the portable way to read it.
func WooProductSlug(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := len(segments) - 1; i >= 0; i-- {
		segment := strings.TrimSpace(segments[i])
		if segment == "" {
			continue
		}
		// Path segments are percent-encoded; the API expects the decoded slug.
		if decoded, err := url.PathUnescape(segment); err == nil {
			segment = decoded
		}
		return segment
	}
	return ""
}

// WooStoreProductsURL builds the Store API query that looks up one product by slug,
// against the REST root the page advertised. Returns false when the page URL carries no
// usable slug or the root is not a URL we can extend.
func WooStoreProductsURL(apiRoot, pageURL string) (string, bool) {
	slug := WooProductSlug(pageURL)
	if slug == "" || apiRoot == "" {
		return "", false
	}
	parsed, err := url.Parse(apiRoot)
	if err != nil {
		return "", false
	}
	// Permalink-less installs advertise their root as "https://site/?rest_route=/", where
	// the route travels in the query string instead of the path. Extending the path there
	// would produce a URL that resolves to the site's front page rather than the API.
	if route := parsed.Query().Get("rest_route"); route != "" {
		q := parsed.Query()
		q.Set("rest_route", "/"+wooStoreAPIPath)
		q.Set("slug", slug)
		parsed.RawQuery = q.Encode()
		return parsed.String(), true
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/" + wooStoreAPIPath
	q := parsed.Query()
	q.Set("slug", slug)
	parsed.RawQuery = q.Encode()
	return parsed.String(), true
}

// WooStoreRule is the extraction_rule saved for a tracker resolved through the Store API.
// The resolved endpoint is stored rather than rebuilt per check: the REST root is only
// discoverable from the storefront HTML, and re-fetching that page every time would give
// back the very dependency the API call exists to avoid on shops that rate-limit or block
// their own product pages.
type WooStoreRule struct {
	Type   string `json:"type"`
	APIURL string `json:"api_url"`
}

// NewWooStoreRule marshals the extraction_rule for a Store API tracker.
func NewWooStoreRule(apiURL string) ([]byte, error) {
	return json.Marshal(WooStoreRule{Type: WooCommerceMethod, APIURL: apiURL})
}

// WooStoreRuleAPIURL reads the endpoint back out of a saved extraction_rule.
func WooStoreRuleAPIURL(ruleJSON []byte) string {
	var rule WooStoreRule
	if err := json.Unmarshal(ruleJSON, &rule); err != nil || rule.Type != WooCommerceMethod {
		return ""
	}
	return rule.APIURL
}

type WooProduct struct {
	ID           int64
	Name         string
	Slug         string
	Permalink    string
	Price        float64
	RegularPrice float64
	Currency     string
	InStock      bool
	Purchasable  bool
	ImageURL     string
}

type wooStorePrices struct {
	Price           string `json:"price"`
	RegularPrice    string `json:"regular_price"`
	CurrencyCode    string `json:"currency_code"`
	CurrencyMinor   *int   `json:"currency_minor_unit"`
	PriceRangeIsSet *struct {
		MinAmount string `json:"min_amount"`
	} `json:"price_range"`
}

type wooStoreProduct struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Slug        string         `json:"slug"`
	Permalink   string         `json:"permalink"`
	IsInStock   bool           `json:"is_in_stock"`
	Purchasable bool           `json:"is_purchasable"`
	Prices      wooStorePrices `json:"prices"`
	Images      []struct {
		Src string `json:"src"`
	} `json:"images"`
}

// ParseWooStoreProduct reads a Store API products response and returns the entry matching
// the page we asked about. It refuses to fall back to a different product when the slug
// query returns something else: ?slug= is a filter, not a lookup, and an unknown slug
// comes back as an empty array or (on some caching layers) an unrelated collection page,
// so taking [0] blindly would silently start tracking the wrong item's price.
func ParseWooStoreProduct(body []byte, pageURL string) (WooProduct, error) {
	var products []wooStoreProduct
	if err := json.Unmarshal(body, &products); err != nil {
		// A single-product response (…/products/<id>) is an object, not an array.
		var single wooStoreProduct
		if singleErr := json.Unmarshal(body, &single); singleErr != nil {
			return WooProduct{}, fmt.Errorf("parse woocommerce store API: %w", err)
		}
		products = []wooStoreProduct{single}
	}
	if len(products) == 0 {
		return WooProduct{}, fmt.Errorf("woocommerce store API returned no product for %s", pageURL)
	}

	wanted := WooProductSlug(pageURL)
	for _, product := range products {
		if product.Slug == "" || product.Slug != wanted {
			continue
		}
		return buildWooProduct(product)
	}
	return WooProduct{}, fmt.Errorf("woocommerce store API returned no product matching slug %q", wanted)
}

func buildWooProduct(product wooStoreProduct) (WooProduct, error) {
	out := WooProduct{
		ID:          product.ID,
		Name:        strings.TrimSpace(product.Name),
		Slug:        product.Slug,
		Permalink:   product.Permalink,
		InStock:     product.IsInStock,
		Purchasable: product.Purchasable,
		Currency:    strings.ToUpper(strings.TrimSpace(product.Prices.CurrencyCode)),
	}
	if len(product.Images) > 0 {
		out.ImageURL = product.Images[0].Src
	}

	raw := strings.TrimSpace(product.Prices.Price)
	if raw == "" && product.Prices.PriceRangeIsSet != nil {
		// Variable products price the parent as a range; its floor is the "from" price
		// the storefront itself displays.
		raw = strings.TrimSpace(product.Prices.PriceRangeIsSet.MinAmount)
	}
	if raw == "" {
		return out, fmt.Errorf("woocommerce store API returned no price for %q", product.Slug)
	}

	price, err := wooDecodeAmount(raw, product.Prices.CurrencyMinor)
	if err != nil {
		return out, err
	}
	out.Price = price

	if regular := strings.TrimSpace(product.Prices.RegularPrice); regular != "" {
		if regularPrice, err := wooDecodeAmount(regular, product.Prices.CurrencyMinor); err == nil {
			out.RegularPrice = regularPrice
		}
	}
	return out, nil
}

// wooDecodeAmount converts a Store API amount to a real one. The API sends amounts as
// integer strings scaled by currency_minor_unit — "589600" with minor unit 2 is 5896.00
// PLN, not 589 600 — and every consumer is expected to divide it back down. Reading the
// literal is what makes a price come out 100x too high, which is also how this value
// leaks into search-engine rich snippets for these shops.
func wooDecodeAmount(raw string, minorUnit *int) (float64, error) {
	amount, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse woocommerce amount %q: %w", raw, err)
	}
	// Absent currency_minor_unit the API contract defaults to 2. Trusting the literal
	// instead would be the 100x-too-high reading for every ordinary currency.
	unit := 2
	if minorUnit != nil {
		unit = *minorUnit
	}
	if unit < 0 || unit > 8 {
		return 0, fmt.Errorf("woocommerce currency_minor_unit %d out of range", unit)
	}
	return amount / math.Pow10(unit), nil
}
