package extractor

import (
	"reflect"
	"testing"
)

func TestParseGeminiModels(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want []string
	}{
		{"empty falls back to default", "", defaultGeminiModels},
		{"whitespace only falls back", "  ,  , ", defaultGeminiModels},
		{"single override", "gemini-3.1-flash-lite", []string{"gemini-3.1-flash-lite"}},
		{"comma list trimmed", " a , b ,c ", []string{"a", "b", "c"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseGeminiModels(c.env); !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseGeminiModels(%q) = %v, want %v", c.env, got, c.want)
			}
		})
	}
}

func TestGeminiCandidatesMultiVariant(t *testing.T) {
	// Reproduces the notino multi-size bug: the model reports the cheapest variant (36.50)
	// as its primary "price", but the tracked variant (103.44) is present in "prices".
	// candidates() must surface both so the caller's reference-price match can lock onto
	// 103.44 instead of flapping to 36.50.
	text := `{"found": true, "price": 36.50, "prices": [36.50, 103.44, 30.60, 36.50], "currency": "PLN", "title": "Shampoo", "in_stock": true}`
	parsed, ok := parseGeminiResponse(text)
	if !ok {
		t.Fatal("parse failed")
	}
	cands := parsed.candidates("https://example.com/p")

	// Deduped: 36.50, 103.44, 30.60 → 3 candidates.
	if len(cands) != 3 {
		t.Fatalf("got %d candidates, want 3: %+v", len(cands), cands)
	}
	if cands[0].Price != "36.5" || cands[0].Confidence != 0.8 {
		t.Errorf("primary candidate = %s/%.2f, want 36.5/0.80", cands[0].Price, cands[0].Confidence)
	}
	found := false
	for _, c := range cands {
		if c.Price == "103.44" {
			found = true
			if c.Confidence != 0.6 {
				t.Errorf("alternate 103.44 confidence = %.2f, want 0.60", c.Confidence)
			}
		}
		if c.Currency != "PLN" || c.SourceURL != "https://example.com/p" {
			t.Errorf("candidate has wrong currency/source: %+v", c)
		}
	}
	if !found {
		t.Error("tracked variant price 103.44 not present in candidates")
	}
}

func TestParseGeminiResponse(t *testing.T) {
	// The prose-narration-then-fenced-JSON-then-bare-JSON shape below is the actual
	// output gemini-2.5-flash produced for an H&M product page during development —
	// parsing must survive it, taking the final clean object.
	messy := "\nThe product page successfully loaded.\n- Price: \"27,99 PLN\"\n\nNow the JSON:\n\n" +
		"```json\n{\"found\": true, \"price\": 27.99, \"currency\": \"PLN\", \"title\": \"Dress\", \"in_stock\": true}\n" +
		"```{\"found\": true, \"price\": 27.99, \"currency\": \"PLN\", \"title\": \"Dress\", \"in_stock\": true}"

	cases := []struct {
		name       string
		text       string
		wantOK     bool
		wantFound  bool
		wantPrice  float64
		wantCur    string
		wantStock  string
		priceIsNil bool
	}{
		{
			name:      "messy prose-wrapped output",
			text:      messy,
			wantOK:    true,
			wantFound: true,
			wantPrice: 27.99,
			wantCur:   "PLN",
			wantStock: "in_stock",
		},
		{
			name:      "clean single object",
			text:      `{"found": true, "price": 149.9, "currency": "EUR", "title": "X", "in_stock": false}`,
			wantOK:    true,
			wantFound: true,
			wantPrice: 149.9,
			wantCur:   "EUR",
			wantStock: "out_of_stock",
		},
		{
			name:       "not found",
			text:       `Sorry. {"found": false, "price": null, "currency": null, "title": null, "in_stock": null}`,
			wantOK:     true,
			wantFound:  false,
			priceIsNil: true,
			wantStock:  "unknown",
		},
		{
			name:   "no json at all",
			text:   "I could not retrieve the page.",
			wantOK: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseGeminiResponse(c.text)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if got.Found != c.wantFound {
				t.Errorf("Found = %v, want %v", got.Found, c.wantFound)
			}
			if c.priceIsNil {
				if got.Price != nil {
					t.Errorf("Price = %v, want nil", *got.Price)
				}
			} else {
				if got.Price == nil {
					t.Fatalf("Price = nil, want %v", c.wantPrice)
				}
				if *got.Price != c.wantPrice {
					t.Errorf("Price = %v, want %v", *got.Price, c.wantPrice)
				}
				if got.Currency != c.wantCur {
					t.Errorf("Currency = %q, want %q", got.Currency, c.wantCur)
				}
			}
			if got.stockStatus() != c.wantStock {
				t.Errorf("stockStatus() = %q, want %q", got.stockStatus(), c.wantStock)
			}
		})
	}
}
