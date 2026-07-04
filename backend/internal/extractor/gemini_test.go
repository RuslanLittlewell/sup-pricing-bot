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
