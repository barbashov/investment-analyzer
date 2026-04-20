// Package assets classifies tickers into MOEX asset classes (stock/bond/etf/currency).
// The classification drives MOEX engine/market routing for price history.
package assets

import "strings"

type Class string

const (
	ClassStock    Class = "STOCK"
	ClassBond     Class = "BOND"
	ClassETF      Class = "ETF"
	ClassCurrency Class = "CURRENCY"
	ClassUnknown  Class = ""
)

// Classify returns the MOEX asset class for a ticker using simple heuristics.
// Returns ClassUnknown when nothing matches — the MOEX client will then fall back to
// querying /iss/securities/{secid}.json once and cache the answer.
func Classify(ticker string) Class {
	t := strings.ToUpper(strings.TrimSpace(ticker))
	if t == "" {
		return ClassUnknown
	}
	// Currency pairs and gold use the *_TOM, *_TMS, *_TOD suffixes on the selt market.
	if hasAnySuffix(t, "_TOM", "_TMS", "_TOD", "_LTV", "_SPT") {
		return ClassCurrency
	}
	// OFZ federal bonds appear in two flavors in Finam exports: "OFZ-26215" or the SECID "SU26215RMFS2".
	if strings.HasPrefix(t, "OFZ") || strings.HasPrefix(t, "SU") && strings.Contains(t, "RMFS") {
		return ClassBond
	}
	// Generic Russian corporate bond ISIN-style: RU000A...
	if strings.HasPrefix(t, "RU000A") {
		return ClassBond
	}
	// FinEx-style ETFs (FXUS, FXTB, FXIT, ...). On MOEX they live under engines/stock/markets/shares.
	if strings.HasPrefix(t, "FX") && len(t) <= 5 {
		return ClassETF
	}
	return ClassStock
}

// MOEXEngine returns the engine portion of the MOEX ISS history URL.
func MOEXEngine(c Class) string {
	switch c {
	case ClassCurrency:
		return "currency"
	default:
		return "stock"
	}
}

// MOEXMarket returns the market portion of the MOEX ISS history URL.
func MOEXMarket(c Class) string {
	switch c {
	case ClassBond:
		return "bonds"
	case ClassCurrency:
		return "selt"
	default:
		return "shares"
	}
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, sx := range suffixes {
		if strings.HasSuffix(s, sx) {
			return true
		}
	}
	return false
}
