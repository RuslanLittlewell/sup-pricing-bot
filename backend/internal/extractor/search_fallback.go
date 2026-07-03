package extractor

import (
	"fmt"

	"github.com/littlewell/price-tracker/internal/proxypool"
)

// NewSearchFallback returns the search-backed fallback chain. OpenSERP is tried first,
// Serper second, and SerpAPI is kept as the final fallback. proxies may be nil (no pool
// wired up) — OpenSERP then just uses its own IP, same as before the pool existed.
func NewSearchFallback(proxies *proxypool.Store) Extractor {
	var chain fallbackChain
	if openserp := NewOpenSERP(proxies); openserp != nil {
		chain.extractors = append(chain.extractors, openserp)
	}
	if serper := NewSerper(); serper != nil {
		chain.extractors = append(chain.extractors, serper)
	}
	if serp := NewSerpAPI(); serp != nil {
		chain.extractors = append(chain.extractors, serp)
	}
	if len(chain.extractors) == 0 {
		return nil
	}
	return chain
}

type fallbackChain struct {
	extractors []Extractor
}

func (c fallbackChain) Domain() string { return "*search_fallback" }

func (c fallbackChain) Extract(html []byte, pageURL string) (*ExtractionResult, error) {
	empty := &ExtractionResult{Candidates: []PriceCandidate{}}
	var lastErr error
	for _, extractor := range c.extractors {
		result, err := extractor.Extract(html, pageURL)
		if err != nil {
			lastErr = err
			continue
		}
		if result != nil && len(result.Candidates) > 0 {
			return result, nil
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("search fallback failed: %w", lastErr)
	}
	return empty, nil
}
