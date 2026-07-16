// Package cost provides a small built-in model pricing table so runcode can
// estimate the cost of a session without the user supplying per-token prices.
//
// The prices here are approximate public list prices and may drift over time.
// They are only a convenience default: an explicit price (via --input-price /
// --output-price, the matching environment variables, or config) always takes
// precedence, and an unknown model simply stays unpriced. For billing-grade
// accuracy, set explicit prices.
package cost

import "strings"

// Price is the per-million-token price for a model, in US dollars.
type Price struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

// Estimate returns the dollar cost of the given token counts at this price.
func (p Price) Estimate(inputTokens, outputTokens int) float64 {
	return float64(inputTokens)/1e6*p.InputPerMTok + float64(outputTokens)/1e6*p.OutputPerMTok
}

// tableEntry maps a normalized model-name prefix to a price. Entries are matched
// case-insensitively; the longest matching prefix wins so that, e.g.,
// "gpt-4o-mini" does not fall through to the "gpt-4o" price.
type tableEntry struct {
	prefix string
	price  Price
}

// table holds approximate list prices (USD per million tokens). Keep entries as
// prefixes so dated model IDs (e.g. claude-opus-4-8-20251231) still match. These
// figures are a best effort and meant to be overridden with explicit prices.
var table = []tableEntry{
	// Anthropic Claude 4.x family.
	{"claude-opus-4", Price{InputPerMTok: 15, OutputPerMTok: 75}},
	{"claude-sonnet-4", Price{InputPerMTok: 3, OutputPerMTok: 15}},
	{"claude-haiku-4", Price{InputPerMTok: 1, OutputPerMTok: 5}},
	// Anthropic Claude 3.x (still referenced by some endpoints).
	{"claude-3-opus", Price{InputPerMTok: 15, OutputPerMTok: 75}},
	{"claude-3-5-sonnet", Price{InputPerMTok: 3, OutputPerMTok: 15}},
	{"claude-3-7-sonnet", Price{InputPerMTok: 3, OutputPerMTok: 15}},
	{"claude-3-5-haiku", Price{InputPerMTok: 0.8, OutputPerMTok: 4}},
	{"claude-3-haiku", Price{InputPerMTok: 0.25, OutputPerMTok: 1.25}},
	// OpenAI (common chat models). The -mini entries must out-rank their bases,
	// which longest-prefix matching guarantees.
	{"gpt-4o-mini", Price{InputPerMTok: 0.15, OutputPerMTok: 0.6}},
	{"gpt-4o", Price{InputPerMTok: 2.5, OutputPerMTok: 10}},
	{"gpt-4.1-mini", Price{InputPerMTok: 0.4, OutputPerMTok: 1.6}},
	{"gpt-4.1", Price{InputPerMTok: 2, OutputPerMTok: 8}},
	{"o4-mini", Price{InputPerMTok: 1.1, OutputPerMTok: 4.4}},
}

// Lookup returns the built-in price for a model name. Matching is
// case-insensitive on a normalized name, preferring the longest matching prefix.
// ok is false when no entry matches (the model is unknown / unpriced).
func Lookup(model string) (Price, bool) {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		return Price{}, false
	}
	best := -1
	var bestPrice Price
	for _, entry := range table {
		if strings.HasPrefix(name, entry.prefix) && len(entry.prefix) > best {
			best = len(entry.prefix)
			bestPrice = entry.price
		}
	}
	if best < 0 {
		return Price{}, false
	}
	return bestPrice, true
}
