// Package pricing owns Belay's versioned, offline model price catalog.
package pricing

import "strings"

const (
	Version       = "belay.local.prices.v3"
	EffectiveDate = "2026-09-16"
	Source        = "Official Anthropic and OpenAI direct API prices, observed 2026-09-16"
)

type modelPrice struct {
	Input        float64
	Output       float64
	CacheRead    float64
	CacheWrite5M float64
	CacheWrite1H float64
	Long         *modelPrice
}

// Values are direct API-equivalent USD prices per million tokens. Belay keeps
// exact model identifiers and a small set of explicit harness aliases; it does
// not infer prices from model-name prefixes.
var catalog = map[string]modelPrice{
	// Anthropic current frontier families and common historical models.
	"claude-opus-5":              anthropicPrice(5, 25),
	"claude-opus-4-8":            anthropicPrice(5, 25),
	"claude-opus-4-7":            anthropicPrice(5, 25),
	"claude-opus-4-6":            anthropicPrice(5, 25),
	"claude-opus-4-5":            anthropicPrice(5, 25),
	"claude-opus-4-5-20251101":   anthropicPrice(5, 25),
	"claude-opus-4-1":            anthropicPrice(15, 75),
	"claude-opus-4-1-20250805":   anthropicPrice(15, 75),
	"claude-opus-4":              anthropicPrice(15, 75),
	"claude-opus-4-20250514":     anthropicPrice(15, 75),
	"claude-fable-5-1":           anthropicPrice(10, 50),
	"claude-fable-5":             anthropicPrice(10, 50),
	"claude-mythos-5-1":          anthropicPrice(15, 75),
	"claude-mythos-5":            anthropicPrice(15, 75),
	"claude-sonnet-5":            anthropicPrice(3, 15),
	"claude-sonnet-4-6":          anthropicPrice(3, 15),
	"claude-sonnet-4-5":          anthropicPrice(3, 15),
	"claude-sonnet-4-5-20250929": anthropicPrice(3, 15),
	"claude-sonnet-4":            anthropicPrice(3, 15),
	"claude-sonnet-4-20250514":   anthropicPrice(3, 15),
	"claude-3-7-sonnet-20250219": anthropicPrice(3, 15),
	"claude-3-5-sonnet-20241022": anthropicPrice(3, 15),
	"claude-3-5-sonnet-20240620": anthropicPrice(3, 15),
	"claude-haiku-4-5":           anthropicPrice(1, 5),
	"claude-haiku-4-5-20251001":  anthropicPrice(1, 5),
	"claude-3-5-haiku-20241022":  anthropicPrice(0.80, 4),
	"claude-3-haiku-20240307":    anthropicPrice(0.25, 1.25),
	"claude-3-opus-20240229":     anthropicPrice(15, 75),

	// OpenAI models commonly emitted by Codex. Sol's entries reflect the
	// currently published promotional API rates; the catalog version records
	// the effective date so a later release can reprice them deterministically.
	"openai.gpt-5.6-sol": {
		Input: 4, Output: 20, CacheRead: 0.40, CacheWrite5M: 5,
		Long: &modelPrice{
			Input: 8, Output: 30, CacheRead: 0.80, CacheWrite5M: 10,
		},
	},
	"openai.gpt-5.6-terra": {
		Input: 2, Output: 12, CacheRead: 0.20, CacheWrite5M: 2.50,
		Long: &modelPrice{
			Input: 4, Output: 18, CacheRead: 0.40, CacheWrite5M: 5,
		},
	},
	"openai.gpt-5.6-luna": {
		Input: 0.20, Output: 1.20, CacheRead: 0.02, CacheWrite5M: 0.25,
	},
	"openai.gpt-5.5": {
		Input: 5, Output: 30, CacheRead: 0.50, CacheWrite5M: 5,
	},
	"openai.gpt-5.5-mini": {
		Input: 0.75, Output: 4.50, CacheRead: 0.075, CacheWrite5M: 0.75,
	},
	"openai.gpt-5.5-nano": {
		Input: 0.20, Output: 1.25, CacheRead: 0.02, CacheWrite5M: 0.20,
	},
	"openai.gpt-5.3-codex": {
		Input: 1.75, Output: 14, CacheRead: 0.175, CacheWrite5M: 1.75,
	},
	"openai.gpt-5.4": {
		Input: 2.50, Output: 15, CacheRead: 0.25, CacheWrite5M: 2.50,
		Long: &modelPrice{
			Input: 5, Output: 22.50, CacheRead: 0.50, CacheWrite5M: 5,
		},
	},
	"openai.gpt-6-astra": {
		Input: 10, Output: 50, CacheRead: 1, CacheWrite5M: 12.50,
		Long: &modelPrice{
			Input: 20, Output: 75, CacheRead: 2, CacheWrite5M: 25,
		},
	},
}

var exactAliases = map[string]string{
	"gpt-5.6-sol":   "openai.gpt-5.6-sol",
	"gpt-5.6-terra": "openai.gpt-5.6-terra",
	"gpt-5.6-luna":  "openai.gpt-5.6-luna",
	"gpt-5.5":       "openai.gpt-5.5",
	"gpt-5.5-mini":  "openai.gpt-5.5-mini",
	"gpt-5.5-nano":  "openai.gpt-5.5-nano",
	"gpt-5.3-codex": "openai.gpt-5.3-codex",
	"gpt-5.4":       "openai.gpt-5.4",
	"gpt-6-astra":   "openai.gpt-6-astra",
}

func anthropicPrice(input, output float64) modelPrice {
	return modelPrice{
		Input:        input,
		Output:       output,
		CacheRead:    input * 0.10,
		CacheWrite5M: input * 1.25,
		CacheWrite1H: input * 2,
	}
}

// Usage is the bounded token input for pricing one harness usage record.
type Usage struct {
	InputTokens       *int64
	OutputTokens      *int64
	CacheReadTokens   *int64
	CacheWriteTokens  *int64
	CacheWrite5M      *int64
	CacheWrite1H      *int64
	InputIncludesRead bool
}

// EstimateUSD returns nil when the exact model or explicit harness alias is not
// present in the versioned catalog. It never guesses a price.
func EstimateUSD(model string, value Usage) *float64 {
	model = strings.TrimSpace(model)
	if canonical := exactAliases[model]; canonical != "" {
		model = canonical
	}
	price, ok := catalog[model]
	if !ok {
		return nil
	}
	if tokenCount(value.InputTokens) > 272_000 && price.Long != nil {
		price = *price.Long
	}
	input := tokenCount(value.InputTokens)
	cacheRead := tokenCount(value.CacheReadTokens)
	cacheWrite := tokenCount(value.CacheWriteTokens)
	cache5M := 0.0
	cache1H := 0.0
	if value.InputIncludesRead {
		input -= cacheRead + cacheWrite
		if input < 0 {
			input = 0
		}
		cache5M = cacheWrite
	} else {
		cache5M = tokenCount(value.CacheWrite5M)
		cache1H = tokenCount(value.CacheWrite1H)
		if value.CacheWrite5M == nil && value.CacheWrite1H == nil {
			cache5M = cacheWrite
		} else if remainder := cacheWrite - cache5M - cache1H; remainder > 0 {
			cache5M += remainder
		}
	}
	total := input*price.Input +
		tokenCount(value.OutputTokens)*price.Output +
		cacheRead*price.CacheRead +
		cache5M*price.CacheWrite5M +
		cache1H*price.CacheWrite1H
	cost := total / 1_000_000
	return &cost
}

func tokenCount(value *int64) float64 {
	if value == nil {
		return 0
	}
	return float64(*value)
}
