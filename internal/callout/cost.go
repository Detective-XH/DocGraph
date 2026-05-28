package callout

import (
	"sort"
	"time"
)

// ModelRate holds a hardcoded rate for one LLM/embedding model.
// Rates are intentionally approximate — purpose is order-of-magnitude awareness.
type ModelRate struct {
	Name      string
	InputPerM float64   // USD per million input tokens
	AsOf      time.Time // date rates were verified; footer warns if >180 days old
}

// CostLine is one row in the estimated cost table.
type CostLine struct {
	ModelName string
	CostUSD   float64
}

// DefaultRates returns the hardcoded rate table.
func DefaultRates() []ModelRate {
	return []ModelRate{
		{Name: "text-embedding-3-small", InputPerM: 0.02, AsOf: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)},
		{Name: "text-embedding-3-large", InputPerM: 0.13, AsOf: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)},
		{Name: "Claude Haiku 4.5", InputPerM: 0.80, AsOf: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)},
		{Name: "Claude Sonnet 4.6", InputPerM: 3.00, AsOf: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)},
		{Name: "GPT-4o mini", InputPerM: 0.15, AsOf: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)},
		{Name: "GPT-4o", InputPerM: 5.00, AsOf: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)},
		{Name: "Local LLM (Ollama)", InputPerM: 0.00, AsOf: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)},
	}
}

// EstimateCost returns one CostLine per model, sorted cheapest-first.
func EstimateCost(totalTokens int, rates []ModelRate) []CostLine {
	lines := make([]CostLine, len(rates))
	for i, r := range rates {
		lines[i] = CostLine{
			ModelName: r.Name,
			CostUSD:   float64(totalTokens) / 1_000_000 * r.InputPerM,
		}
	}
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].CostUSD != lines[j].CostUSD {
			return lines[i].CostUSD < lines[j].CostUSD
		}
		return lines[i].ModelName < lines[j].ModelName
	})
	return lines
}

// staleRateWarning returns a staleness note if any rate is older than 180 days.
func staleRateWarning(rates []ModelRate, now time.Time) string {
	for _, r := range rates {
		if now.Sub(r.AsOf) > 180*24*time.Hour {
			return "⚠️ rates may be stale — verify before budgeting"
		}
	}
	return ""
}

// mostRecentAsOf returns the newest AsOf date across all rates, formatted as YYYY-MM-DD.
func mostRecentAsOf(rates []ModelRate) string {
	var latest time.Time
	for _, r := range rates {
		if r.AsOf.After(latest) {
			latest = r.AsOf
		}
	}
	if latest.IsZero() {
		return ""
	}
	return latest.Format("2006-01-02")
}
