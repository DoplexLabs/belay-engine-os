package transcript

import "github.com/DoplexLabs/belay-engine/internal/pricing"

const (
	PriceTableVersion       = pricing.Version
	PriceTableEffectiveDate = pricing.EffectiveDate
	PriceTableSource        = pricing.Source
)

// PricingUsage is the public, bounded input for evaluating one harness usage
// record against Belay's versioned in-repo price table.
type PricingUsage struct {
	InputTokens       *int64
	OutputTokens      *int64
	CacheReadTokens   *int64
	CacheWriteTokens  *int64
	InputIncludesRead bool
}

// EstimateCostUSD returns nil when the exact model or explicit harness alias is
// not present in the versioned table. It never guesses a price.
func EstimateCostUSD(model string, value PricingUsage) *float64 {
	return pricing.EstimateUSD(model, pricing.Usage{
		InputTokens:       value.InputTokens,
		OutputTokens:      value.OutputTokens,
		CacheReadTokens:   value.CacheReadTokens,
		CacheWriteTokens:  value.CacheWriteTokens,
		InputIncludesRead: value.InputIncludesRead,
	})
}

func calculateCost(model string, value usage) *float64 {
	return pricing.EstimateUSD(model, pricing.Usage{
		InputTokens:       value.InputTokens,
		OutputTokens:      value.OutputTokens,
		CacheReadTokens:   value.CacheReadTokens,
		CacheWriteTokens:  value.CacheWriteTokens,
		CacheWrite5M:      value.CacheWrite5M,
		CacheWrite1H:      value.CacheWrite1H,
		InputIncludesRead: value.InputIncludesRead,
	})
}

func count(value *int64) float64 {
	if value == nil {
		return 0
	}
	return float64(*value)
}
