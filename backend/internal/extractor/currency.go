package extractor

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	currencyTokenPattern = `(PLN|EUR|USD|GBP|RUB|zł|€|\$|£|₽)`
	// A page price number: thousands groups separated by a space, dot or comma, plus an
	// optional 1–2 digit fraction ("1 299,99", "1,007.00", "34.49").
	pagePriceNumberPattern = `(\d+(?:[ .,]\d{3})*(?:[.,]\d{1,2})?)`
	// Currency markers are often wrapped in their own element ("<span>zł</span>"), so a
	// few tags may sit between the number and the marker.
	priceCurrencyGapPattern = `(?:\s*<[^<>]*>){0,4}\s*`
)

var (
	currencyBeforePriceRE = regexp.MustCompile(currencyTokenPattern + priceCurrencyGapPattern + pagePriceNumberPattern)
	priceBeforeCurrencyRE = regexp.MustCompile(pagePriceNumberPattern + priceCurrencyGapPattern + currencyTokenPattern)
	priceNumberRE         = regexp.MustCompile(`\d[\d\s.,]*`)
	nbspReplacer          = strings.NewReplacer("&nbsp;", " ", "&#160;", " ", "&#xa0;", " ", "&#xA0;", " ", "\u00a0", " ", "\u202f", " ", "\u2009", " ")
)

// FillMissingCurrency sets Currency on candidates extracted without one (DOM attribute,
// CSS selector and regex readings return bare numbers). The currency comes from the
// candidate's own price text, or else from the currency marker the page shows right next
// to that same price — e.g. Amazon renders "EUR 30.54" when it localizes prices for the
// visitor's country. Candidates whose price has no adjacent currency on the page are left
// empty so callers keep their own fallback.
func FillMissingCurrency(result *ExtractionResult, htmlContent []byte) {
	if result == nil {
		return
	}
	var pageCurrencies map[int64]string
	for i := range result.Candidates {
		candidate := &result.Candidates[i]
		if candidate.Currency != "" {
			continue
		}
		if _, currency, ok := extractCurrencyPrice(candidate.Price); ok {
			candidate.Currency = currency
			continue
		}
		cents, ok := priceTextCents(candidate.Price)
		if !ok {
			continue
		}
		if pageCurrencies == nil {
			pageCurrencies = currenciesByPagePrice(string(htmlContent))
		}
		candidate.Currency = pageCurrencies[cents]
	}
}

// currenciesByPagePrice maps every price shown on the page (in cents) to the currency
// most often displayed next to it; ties go to the currency seen first.
func currenciesByPagePrice(htmlContent string) map[int64]string {
	text := nbspReplacer.Replace(htmlContent)
	type tally struct {
		votes map[string]int
		order []string
	}
	tallies := map[int64]*tally{}
	add := func(number, token string) {
		cents, ok := priceTextCents(number)
		if !ok {
			return
		}
		t := tallies[cents]
		if t == nil {
			t = &tally{votes: map[string]int{}}
			tallies[cents] = t
		}
		currency := normalizeCurrencySymbol(token)
		if t.votes[currency] == 0 {
			t.order = append(t.order, currency)
		}
		t.votes[currency]++
	}
	for _, match := range currencyBeforePriceRE.FindAllStringSubmatch(text, -1) {
		add(match[2], match[1])
	}
	for _, match := range priceBeforeCurrencyRE.FindAllStringSubmatch(text, -1) {
		add(match[1], match[2])
	}

	currencies := make(map[int64]string, len(tallies))
	for cents, t := range tallies {
		best := ""
		for _, currency := range t.order {
			if best == "" || t.votes[currency] > t.votes[best] {
				best = currency
			}
		}
		currencies[cents] = best
	}
	return currencies
}

// priceTextCents parses the first number in text using the same separator rules as the
// Telegram price input: with both separators the last one is decimal, a lone comma is
// decimal, and spaces are thousands separators.
func priceTextCents(text string) (int64, bool) {
	number := priceNumberRE.FindString(nbspReplacer.Replace(text))
	number = strings.Join(strings.Fields(number), "")
	if number == "" {
		return 0, false
	}
	switch {
	case strings.Contains(number, ",") && strings.Contains(number, "."):
		if strings.LastIndex(number, ",") > strings.LastIndex(number, ".") {
			number = strings.ReplaceAll(number, ".", "")
			number = strings.ReplaceAll(number, ",", ".")
		} else {
			number = strings.ReplaceAll(number, ",", "")
		}
	case strings.Count(number, ",") == 1:
		number = strings.ReplaceAll(number, ",", ".")
	default:
		number = strings.ReplaceAll(number, ",", "")
	}
	price, err := strconv.ParseFloat(strings.TrimRight(number, "."), 64)
	if err != nil || price <= 0 {
		return 0, false
	}
	cents := math.Round(price * 100)
	// "34.495" is not a cents-precise price; rounding it would pass it off as 34.50.
	if math.Abs(price*100-cents) > 1e-6 {
		return 0, false
	}
	return int64(cents), true
}
