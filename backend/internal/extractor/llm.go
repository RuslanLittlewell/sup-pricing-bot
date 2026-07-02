package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
	"golang.org/x/net/html"
)

// Groq's free tier gives an OpenAI-compatible chat-completions API, which is what
// this tier is built against instead of a paid provider.
const (
	groqBaseURL = "https://api.groq.com/openai/v1/"
	groqModel   = "llama-3.3-70b-versatile"
)

// LLMExtractor is the last-resort tier in the extraction pipeline: URL parser →
// source resolver (PageFetcher) → extractor (JSON-LD → meta tags → CSS selectors
// [GenericExtractor] → this). It's only reached when every rule-based extractor
// already came up empty on a page we *did* successfully fetch (bot-block pages
// never get here — PageFetcher returns errBotBlocked before any extractor runs).
//
// It's the same shape as a "fetch a URL, then have a model read it" tool: the page
// text goes to the model with a forced tool call, and the tool call's arguments are
// the structured result. This exists for sites whose markup doesn't match any of our
// CSS-selector heuristics and that don't emit JSON-LD/meta price tags — rather than
// writing and maintaining a bespoke selector for every such site.
type LLMExtractor struct {
	client openai.Client
	model  string
}

// NewLLM returns nil if GROQ_API_KEY is unset, so this tier can be wired in
// unconditionally (same nil-means-disabled pattern as *renderer.Renderer elsewhere
// in this package) instead of needing a separate feature flag.
func NewLLM() *LLMExtractor {
	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		return nil
	}
	return &LLMExtractor{
		client: openai.NewClient(option.WithAPIKey(apiKey), option.WithBaseURL(groqBaseURL)),
		model:  groqModel,
	}
}

func (e *LLMExtractor) Domain() string { return "*llm" }

func (e *LLMExtractor) Extract(htmlContent []byte, url string) (*ExtractionResult, error) {
	empty := &ExtractionResult{Candidates: []PriceCandidate{}}
	if e == nil {
		return empty, nil
	}

	text := visibleText(htmlContent)
	if text == "" {
		return empty, nil
	}
	const maxChars = 12000
	if len(text) > maxChars {
		text = text[:maxChars]
	}

	tool := openai.ChatCompletionToolParam{
		Function: shared.FunctionDefinitionParam{
			Name:        "extract_product",
			Description: openai.String("Record the product's title, current price, and availability as read from the page text."),
			Parameters: shared.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"title":    map[string]any{"type": "string", "description": "Product title; empty string if not found"},
					"price":    map[string]any{"type": "string", "description": "Numeric price as shown on the page, e.g. \"129.99\"; empty string if no current price is present"},
					"currency": map[string]any{"type": "string", "description": "ISO 4217 currency code, e.g. PLN, EUR, USD; empty string if unknown"},
					"availability": map[string]any{
						"type": "string",
						"enum": []string{"in_stock", "out_of_stock", "unknown"},
					},
					"found": map[string]any{"type": "boolean", "description": "true only if you found an actual current price for the main product on this page"},
				},
				"required": []string{"title", "price", "currency", "availability", "found"},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	resp, err := e.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: e.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ChatCompletionSystemMessageParamContentUnion{
						OfString: openai.String(
							"You extract product price data from raw text scraped from an e-commerce product page. " +
								"Only report a price if it is clearly the current price of the main product on the page — " +
								"not a related or recommended product, not a crossed-out original price shown alongside a " +
								"discount, not a shipping or delivery fee. If you are not confident, set found to false.",
						),
					},
				},
			},
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfString: openai.String(fmt.Sprintf("Page URL: %s\n\nPage text:\n%s", url, text)),
					},
				},
			},
		},
		Tools: []openai.ChatCompletionToolParam{tool},
		ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{
			OfChatCompletionNamedToolChoice: &openai.ChatCompletionNamedToolChoiceParam{
				Function: openai.ChatCompletionNamedToolChoiceFunctionParam{Name: "extract_product"},
			},
		},
		MaxTokens: openai.Int(1024),
	})
	if err != nil {
		return nil, fmt.Errorf("llm extraction request: %w", err)
	}

	if len(resp.Choices) == 0 || len(resp.Choices[0].Message.ToolCalls) == 0 {
		return empty, nil
	}

	var parsed struct {
		Title        string `json:"title"`
		Price        string `json:"price"`
		Currency     string `json:"currency"`
		Availability string `json:"availability"`
		Found        bool   `json:"found"`
	}
	toolCall := resp.Choices[0].Message.ToolCalls[0]
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &parsed); err != nil {
		return nil, fmt.Errorf("parse llm tool input: %w", err)
	}

	result := &ExtractionResult{
		Title:       parsed.Title,
		StockStatus: parsed.Availability,
		Candidates:  []PriceCandidate{},
	}
	if parsed.Found && parsed.Price != "" {
		rule, _ := json.Marshal(map[string]string{"type": "llm_extraction", "model": e.model})
		result.Candidates = append(result.Candidates, PriceCandidate{
			Price:      parsed.Price,
			Currency:   parsed.Currency,
			Confidence: 0.5,
			Label:      "LLM extraction",
			Rule:       rule,
		})
	}
	return result, nil
}

// visibleText walks the parsed HTML tree and concatenates text nodes, skipping
// <script>/<style>/<noscript>/<svg> subtrees so the model sees page copy rather than
// JS/CSS/vector-icon markup that would otherwise dominate the token budget.
func visibleText(htmlContent []byte) string {
	doc, err := html.Parse(strings.NewReader(string(htmlContent)))
	if err != nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "svg":
				return
			}
		}
		if n.Type == html.TextNode {
			t := strings.TrimSpace(n.Data)
			if t != "" {
				b.WriteString(t)
				b.WriteString("\n")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return b.String()
}
