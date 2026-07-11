package extractor

import "testing"

func TestLamodaSPSNPageIsBotChallenge(t *testing.T) {
	body := []byte(`<script>function get_cookie_spsn() { return "spsn=123"; }</script>`)
	if !isBotChallenge(body) {
		t.Fatal("expected Lamoda SPSN page to be detected as bot challenge")
	}
}

// Some sites embed a "price" JSON field in minor units (e.g. grosze/cents) as a
// bare integer rather than a decimal amount. Without an alternate reading, a
// bare "22900" would be trusted as literally 22900, 100x too large.
func TestExtractByRegexOffersMinorUnitsReading(t *testing.T) {
	html := `<script>var data = {"price": "22900"};</script>`
	candidates := extractByRegex(html)

	var sawLiteral, sawMinorUnits bool
	for _, c := range candidates {
		if c.Price == "22900" {
			sawLiteral = true
		}
		if c.Price == "229.00" {
			sawMinorUnits = true
		}
	}
	if !sawLiteral {
		t.Error("expected the literal 22900 reading to still be offered")
	}
	if !sawMinorUnits {
		t.Error("expected a 229.00 minor-units reading to be offered alongside it")
	}
}

// A price already expressed with a decimal point is unambiguous and shouldn't
// get a second, incorrectly-rescaled candidate.
func TestExtractByRegexSkipsMinorUnitsWhenDecimalAlreadyPresent(t *testing.T) {
	html := `<script>var data = {"price": "229.00"};</script>`
	candidates := extractByRegex(html)

	for _, c := range candidates {
		if c.Label == "Regex JSON match (minor units)" {
			t.Errorf("did not expect a minor-units candidate for an already-decimal price, got %q", c.Price)
		}
	}
}
