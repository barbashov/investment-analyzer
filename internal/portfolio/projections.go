package portfolio

import (
	"time"

	"investment-analyzer/internal/store"
)

// RUStockTaxRate is the heuristic applied when projecting net-of-tax payouts
// for smart-lab-sourced announcements. Russian-resident individual shareholders
// pay 13% withholding on stock dividends by default; higher brackets and
// non-residents see different rates, and bonds/ETFs/foreign issuers diverge
// from this number. We surface it clearly as a heuristic in the UI.
const RUStockTaxRate = 0.13

// dedupWindowBefore / dedupWindowAfter bound the interval around a smart-lab
// T-1 date where a MOEX-confirmed registry implies supersession.
//   - 14 days before: MOEX can publish a registry a bit earlier than the
//     smart-lab-anticipated T-1.
//   - 60 days after: Russian brokers typically pay within 10-25 business days
//     of the registry; a generous window avoids matching the *next* period's
//     MOEX entry to a smart-lab projection.
const (
	dedupWindowBefore = -14 * 24 * time.Hour
	dedupWindowAfter  = 60 * 24 * time.Hour
)

// SupersedesByMOEX reports whether a MOEX-confirmed registry in `moexDivs`
// covers the period described by smart-lab announcement `a`. Once true, the
// projection is hidden — MOEX is the source of truth for confirmed dividends.
func SupersedesByMOEX(a store.SmartlabDividend, moexDivs []store.MOEXDividend) bool {
	anchor, err := time.Parse("2006-01-02", a.T2Date)
	if err != nil {
		return false
	}
	lo := anchor.Add(dedupWindowBefore).Format("2006-01-02")
	hi := anchor.Add(dedupWindowAfter).Format("2006-01-02")
	for _, m := range moexDivs {
		if m.RegistryDate >= lo && m.RegistryDate <= hi {
			return true
		}
	}
	return false
}

// ProjectPayments synthesizes forward-looking DividendPayment rows from
// smart-lab announcements. Callers provide:
//   - `announcements`: all cached smart-lab rows to consider (typically
//     filtered to current holdings).
//   - `moexByTicker`: MOEX dividends grouped by ticker, used to suppress
//     projections that MOEX has already confirmed.
//   - `txs`: the full transaction history (for cost basis / current holdings).
//   - `asOf`: the reference "today" — announcements with ex_date < asOf are
//     skipped (the payout window has closed).
//
// The returned payments have Projected=true, Gross=qty*value, Tax=13% heuristic,
// Net=Gross-Tax. BookValue/Yield are populated from the cost basis at asOf.
// Sort order: ex-date ascending (earliest payout first).
func ProjectPayments(
	announcements []store.SmartlabDividend,
	moexByTicker map[string][]store.MOEXDividend,
	txs []*store.Transaction,
	asOf time.Time,
) []DividendPayment {
	if len(announcements) == 0 {
		return nil
	}
	asOfStr := asOf.UTC().Format("2006-01-02")
	positions := ComputePositions(txs, asOfStr)

	out := make([]DividendPayment, 0, len(announcements))
	for _, a := range announcements {
		if a.ExDate < asOfStr {
			continue
		}
		pos, ok := positions[a.Ticker]
		if !ok || pos.Quantity <= 0 {
			// No holdings → no projected payout for us. Skip rather than show 0.
			continue
		}
		if SupersedesByMOEX(a, moexByTicker[a.Ticker]) {
			continue
		}
		gross := pos.Quantity * a.ValuePerShare
		tax := gross * RUStockTaxRate
		p := DividendPayment{
			Date:      a.ExDate,
			Ticker:    a.Ticker,
			Period:    a.Period,
			Currency:  "RUB", // smart-lab only covers RUB-paid dividends
			Gross:     gross,
			Tax:       tax,
			Net:       gross - tax,
			BookValue: pos.BookValue,
			Projected: true,
		}
		if pos.BookValue > 0 {
			p.Yield = gross / pos.BookValue
		}
		out = append(out, p)
	}

	// Stable sort by ex-date then ticker.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && (out[j].Date < out[j-1].Date ||
			(out[j].Date == out[j-1].Date && out[j].Ticker < out[j-1].Ticker)); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
