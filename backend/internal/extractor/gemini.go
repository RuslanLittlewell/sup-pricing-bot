package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"google.golang.org/genai"
)

const geminiPerModelTimeout = 60 * time.Second

// defaultGeminiModels is the ordered list of models the Gemini tier tries, substituting
// the next whenever one errors (quota/overload/timeout) — see GeminiExtractor.Extract.
// All are confirmed working with the URLContext tool and are deliberately version-diverse
// (2.5 / 3.1 / 3.5), so a version-specific outage or per-model quota exhaustion doesn't
// take the whole tier down. Fast/cheap Flash-Lite first, more capable Flash next, a moving
// "-latest" alias last as a final safety net. Pro and 2.0 models are intentionally absent:
// they were quota-blocked on our key in testing, so including them would just burn a
// round-trip each call. Override with GEMINI_MODEL (comma-separated).
var defaultGeminiModels = []string{
	"gemini-2.5-flash-lite",
	"gemini-3.1-flash-lite",
	"gemini-2.5-flash",
	"gemini-3.5-flash",
	"gemini-flash-latest",
}

// errGeminiAllModelsFailed means every model in the tier errored (none even returned a
// response) — as opposed to a model running fine but the page having no readable price.
// It's the signal that we've genuinely exhausted this last-resort tier.
var errGeminiAllModelsFailed = errors.New("all gemini models failed")

// IsAllGeminiModelsFailed reports whether err (possibly wrapped, e.g. by the fallback
// chain) signals that every Gemini model in our last-resort tier errored — i.e. we've
// exhausted every extraction method available and can honestly tell the user we tried
// everything. See GeminiExtractor.Extract.
func IsAllGeminiModelsFailed(err error) bool {
	return errors.Is(err, errGeminiAllModelsFailed)
}

// geminiPromptTemplate asks the model to fetch the page itself (via the URLContext tool)
// and return a strict, flat JSON object we can parse. The %s is the product URL. The
// model tends to wrap the JSON in prose/markdown despite the instruction, so parsing
// (parseGeminiResponse) tolerates that rather than relying on clean output.
const geminiPromptTemplate = `Fetch the product page at this exact URL and read its data: %s

Then answer with ONLY a compact JSON object (no markdown, no code fences, no commentary) with exactly these fields:
{"found": true|false, "price": number|null, "currency": "ISO 4217 code"|null, "title": string|null, "in_stock": true|false|null}

Rules:
- "price" is the current selling price of the product as a plain number (e.g. 27.99). If the item is on sale, use the current discounted price, NOT the original/crossed-out one.
- "currency" is the 3-letter ISO code shown on the page (e.g. PLN, EUR, USD).
- If you cannot retrieve the page, or the page has no clear product price, return {"found": false, "price": null, "currency": null, "title": null, "in_stock": null}.
- Never guess or invent a price. Report only what is actually shown on the page.`

// GeminiExtractor uses Google's Gemini model with its built-in URLContext tool to fetch
// and read a product page directly. Because the fetch happens on Google's infrastructure,
// it can get past bot protection (Akamai/DataDome/Cloudflare IP-reputation blocks) that
// stops our own fetcher and even our proxy pool — so it's the strongest, last-resort
// fallback tier, tried after the cheaper search-based ones. It's also the most expensive
// (an LLM call reading the whole page), hence last.
type GeminiExtractor struct {
	client *genai.Client
	models []string
}

// NewGemini returns nil when GEMINI_API_KEY is not configured. GEMINI_MODEL overrides the
// default model list (comma-separated, tried in order — see defaultGeminiModels).
func NewGemini() *GeminiExtractor {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil
	}
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil
	}
	return &GeminiExtractor{client: client, models: parseGeminiModels(os.Getenv("GEMINI_MODEL"))}
}

// parseGeminiModels reads a comma-separated GEMINI_MODEL override, trimming blanks, and
// falls back to defaultGeminiModels when it's empty.
func parseGeminiModels(env string) []string {
	var models []string
	for _, m := range strings.Split(env, ",") {
		if m = strings.TrimSpace(m); m != "" {
			models = append(models, m)
		}
	}
	if len(models) == 0 {
		return defaultGeminiModels
	}
	return models
}

func (e *GeminiExtractor) Domain() string { return "*gemini" }

// Extract tries each configured model in turn, substituting the next whenever one errors
// (quota/overload/timeout). It returns:
//   - a result with the price, as soon as a model both reaches the page and reads a price;
//   - an empty result (no error) once a model ran successfully but the page had no readable
//     price, or reached the page and reported none — no point trying more models for a
//     genuinely price-less page;
//   - errGeminiAllModelsFailed only when EVERY model errored (none returned a response),
//     which is the "we've exhausted our last resort" signal a caller can apologize on.
func (e *GeminiExtractor) Extract(_ []byte, pageURL string) (*ExtractionResult, error) {
	empty := &ExtractionResult{Candidates: []PriceCandidate{}}
	if e == nil {
		return empty, nil
	}

	prompt := fmt.Sprintf(geminiPromptTemplate, pageURL)
	allErrored := true
	for _, model := range e.models {
		result, err := e.generate(model, prompt)
		if err != nil {
			continue // model errored — substitute the next one
		}
		allErrored = false

		// Don't trust a price the model produced without actually reaching the page — if
		// the URL retrieval failed/was blocked, anything it "read" would be a
		// hallucination. Treat that like "no price found" and stop: a different model is
		// unlikely to reach a page this one couldn't, and we don't want to invent a price.
		if !geminiRetrievedURL(result) {
			return empty, nil
		}
		parsed, ok := parseGeminiResponse(result.Text())
		if !ok || !parsed.Found || parsed.Price == nil {
			return empty, nil
		}

		rule, _ := json.Marshal(map[string]string{"type": "gemini_url_context"})
		return &ExtractionResult{
			Title:       parsed.Title,
			StockStatus: parsed.stockStatus(),
			Candidates: []PriceCandidate{{
				Price:      strconv.FormatFloat(*parsed.Price, 'f', -1, 64),
				Currency:   strings.ToUpper(parsed.Currency),
				Confidence: 0.8,
				Label:      "Gemini URL context",
				SourceURL:  pageURL,
				Rule:       rule,
			}},
		}, nil
	}

	if allErrored {
		return nil, errGeminiAllModelsFailed
	}
	return empty, nil
}

func (e *GeminiExtractor) generate(model, prompt string) (*genai.GenerateContentResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), geminiPerModelTimeout)
	defer cancel()
	return e.client.Models.GenerateContent(ctx, model, genai.Text(prompt),
		&genai.GenerateContentConfig{
			Tools: []*genai.Tool{{URLContext: &genai.URLContext{}}},
		},
	)
}

// geminiRetrievedURL reports whether the model actually fetched at least one URL
// successfully via the URLContext tool.
func geminiRetrievedURL(result *genai.GenerateContentResponse) bool {
	for _, c := range result.Candidates {
		if c.URLContextMetadata == nil {
			continue
		}
		for _, u := range c.URLContextMetadata.URLMetadata {
			if u.URLRetrievalStatus == genai.URLRetrievalStatusSuccess {
				return true
			}
		}
	}
	return false
}

type geminiPriceResponse struct {
	Found    bool     `json:"found"`
	Price    *float64 `json:"price"`
	Currency string   `json:"currency"`
	Title    string   `json:"title"`
	InStock  *bool    `json:"in_stock"`
}

func (r geminiPriceResponse) stockStatus() string {
	if r.InStock == nil {
		return "unknown"
	}
	if *r.InStock {
		return "in_stock"
	}
	return "out_of_stock"
}

// geminiJSONObjectRe matches a flat JSON object (no nested braces) — the model wraps its
// answer in prose and markdown code fences despite being told not to, and sometimes emits
// the object more than once, so we scan for candidates rather than json.Unmarshal the raw
// text.
var geminiJSONObjectRe = regexp.MustCompile(`\{[^{}]*\}`)

// parseGeminiResponse pulls the price JSON out of the model's (often prose-wrapped,
// possibly repeated) text. It returns the last object that both parses and carries a
// "found" field — "last" because when the model narrates first and answers second, the
// clean answer is at the end.
func parseGeminiResponse(text string) (geminiPriceResponse, bool) {
	matches := geminiJSONObjectRe.FindAllString(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		if !strings.Contains(matches[i], `"found"`) {
			continue
		}
		var parsed geminiPriceResponse
		if err := json.Unmarshal([]byte(matches[i]), &parsed); err == nil {
			return parsed, true
		}
	}
	return geminiPriceResponse{}, false
}
