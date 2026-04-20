// Package payouts implements `invest dividends payouts` — an interactive per-payment browser
// with a MOEX-cross-reference detail panel.
package payouts

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"investment-analyzer/internal/portfolio"
	"investment-analyzer/internal/store"
	"investment-analyzer/internal/tui/browser"
	"investment-analyzer/internal/ui"
)

type PayoutRow struct {
	P                portfolio.DividendPayment
	MOEXVal          float64 // 0 if no match
	MOEXHit          bool
	MOEXGapDays      int  // days between payment date and matched MOEX registry date (>= 0)
	MOEXCacheEmpty   bool // true when ListMOEXDividends returned no rows for this ticker
	HoldQty          float64 // qty held on the registry/ex date (used to derive PerShare)
}

func (r PayoutRow) Cells() []string {
	perShare := "—"
	if r.HoldQty > 0 {
		perShare = ui.FormatRUB(r.P.Gross / r.HoldQty)
	}
	qty := "—"
	if r.HoldQty > 0 {
		qty = fmt.Sprintf("%g", r.HoldQty)
	}
	yield := "—"
	if r.P.Yield > 0 {
		yield = ui.FormatPct(r.P.Yield * 100)
	}
	status := "confirmed"
	if r.P.Projected {
		status = "projected"
	}
	return []string{
		r.P.Date,
		emptyDash(r.P.Ticker),
		emptyDash(r.P.Period),
		status,
		ui.FormatRUB(r.P.Gross),
		ui.FormatRUB(r.P.Tax),
		ui.FormatRUB(r.P.Net),
		qty,
		perShare,
		yield,
	}
}

func (r PayoutRow) Detail() []ui.KVField {
	fields := []ui.KVField{
		{Label: "Account", Value: emptyDash(r.P.Account)},
		{Label: "Currency", Value: r.P.Currency},
		{Label: "Asset", Value: emptyDash(r.P.AssetName)},
		{Label: "ISIN", Value: emptyDash(r.P.ISIN)},
	}
	if r.P.BookValue > 0 {
		fields = append(fields, ui.KVField{
			Label: "Yield",
			Value: fmt.Sprintf("%s on %s book value (single payment, not annualized)",
				ui.FormatPct(r.P.Yield*100), ui.FormatRUB(r.P.BookValue)),
		})
	}
	if r.P.Projected {
		fields = append(fields, ui.KVField{
			Label: "Source",
			Value: "smart-lab.ru (board-recommended, not yet MOEX-confirmed). Tax 13% heuristic.",
		})
		return fields
	}
	if r.MOEXHit {
		actual := r.P.Gross
		if r.HoldQty > 0 {
			actual = r.P.Gross / r.HoldQty
		}
		delta := actual - r.MOEXVal
		marker := "✓"
		if math.Abs(delta) > 0.01 {
			marker = "Δ"
		}
		fields = append(fields, ui.KVField{
			Label: "MOEX match",
			Value: fmt.Sprintf("%s announced %s/share, recorded ≈ %s/share (delta %s, gap %dd)",
				marker, ui.FormatRUB(r.MOEXVal), ui.FormatRUB(actual), ui.FormatRUB(delta), r.MOEXGapDays),
		})
	} else if r.P.Ticker != "" {
		msg := "MOEX has no announcement near this payment date"
		if r.MOEXCacheEmpty {
			msg = "no MOEX data cached for this ticker — run `invest update`"
		}
		fields = append(fields, ui.KVField{Label: "MOEX match", Value: msg})
	}
	return fields
}

func (r PayoutRow) Match(tokens map[string]string) bool {
	for k, v := range tokens {
		v = strings.ToLower(v)
		switch k {
		case "":
			h := strings.ToLower(r.P.Ticker + " " + r.P.AssetName + " " + r.P.Period + " " + r.P.ISIN)
			if !strings.Contains(h, v) {
				return false
			}
		case "ticker":
			if !strings.EqualFold(r.P.Ticker, v) && !strings.EqualFold(r.P.AssetName, v) {
				return false
			}
		case "account":
			if !strings.Contains(strings.ToLower(r.P.Account), v) {
				return false
			}
		case "from":
			if r.P.Date < v {
				return false
			}
		case "to":
			if r.P.Date > v {
				return false
			}
		case "period":
			if !strings.EqualFold(r.P.Period, v) {
				return false
			}
		case "isin":
			if !strings.EqualFold(r.P.ISIN, v) {
				return false
			}
		case "status":
			switch v {
			case "projected":
				if !r.P.Projected {
					return false
				}
			case "confirmed":
				if r.P.Projected {
					return false
				}
			}
		}
	}
	return true
}

// Run starts the payouts browser.
func Run(st *store.Store) error {
	txs, err := st.ListTransactions("", "")
	if err != nil {
		return err
	}
	resolver := portfolio.MapTickerResolver{ByISIN: portfolio.DefaultISINTickerMap}
	pays := portfolio.ExtractDividends(txs, resolver)
	pays = portfolio.AnnotatePayments(pays, txs)

	// Cache per-ticker MOEX dividend lists so we don't query SQLite once per payment.
	moexByTicker := map[string][]store.MOEXDividend{}
	for _, p := range pays {
		if p.Ticker == "" {
			continue
		}
		if _, ok := moexByTicker[p.Ticker]; ok {
			continue
		}
		divs, err := st.ListMOEXDividends(p.Ticker)
		if err != nil {
			continue
		}
		moexByTicker[p.Ticker] = divs
	}

	// Historical rows: one per actual DIVIDEND transaction.
	rows := make([]browser.Row, 0, len(pays))
	for _, p := range pays {
		positions := portfolio.ComputePositions(txs, p.Date)
		var holdQty float64
		if pos, ok := positions[p.Ticker]; ok {
			holdQty = pos.Quantity
		}
		row := PayoutRow{P: p, HoldQty: holdQty}
		if p.Ticker != "" {
			divs := moexByTicker[p.Ticker]
			if len(divs) == 0 {
				row.MOEXCacheEmpty = true
			} else {
				// Pick the most recent registry date on or before the payment date,
				// within a 90-day window (Russian brokers typically pay 10-25 business
				// days after registry close — 90 days is generous but rejects matches
				// to the next period's announcement).
				bestGap := 9999
				for i, md := range divs {
					if md.RegistryDate > p.Date {
						continue
					}
					gap := daysBetween(p.Date, md.RegistryDate)
					if gap >= 0 && gap < bestGap && gap <= 90 {
						bestGap = gap
						row.MOEXHit = true
						row.MOEXVal = divs[i].Value
						row.MOEXGapDays = gap
					}
				}
			}
		}
		rows = append(rows, row)
	}

	// Projected rows: synthesized from smart-lab for current holdings.
	// MOEX dividend cache is loaded lazily above (seeded only from tickers
	// that have historical payments); ensure we also cover tickers we only
	// know from smart-lab.
	if announcements, err := st.ListAllSmartlabDividends(); err == nil && len(announcements) > 0 {
		for _, a := range announcements {
			if _, ok := moexByTicker[a.Ticker]; ok {
				continue
			}
			if divs, err := st.ListMOEXDividends(a.Ticker); err == nil {
				moexByTicker[a.Ticker] = divs
			}
		}
		asOf := time.Now().UTC()
		currentPositions := portfolio.ComputePositions(txs, asOf.Format("2006-01-02"))
		projected := portfolio.ProjectPayments(announcements, moexByTicker, txs, asOf)
		for _, p := range projected {
			var holdQty float64
			if pos, ok := currentPositions[p.Ticker]; ok {
				holdQty = pos.Quantity
			}
			rows = append(rows, PayoutRow{P: p, HoldQty: holdQty})
		}
	}

	sorts := []browser.SortMode{
		{Label: "date ↓", Less: func(a, b browser.Row) bool { return a.(PayoutRow).P.Date > b.(PayoutRow).P.Date }},
		{Label: "date ↑", Less: func(a, b browser.Row) bool { return a.(PayoutRow).P.Date < b.(PayoutRow).P.Date }},
		{Label: "ticker", Less: func(a, b browser.Row) bool { return a.(PayoutRow).P.Ticker < b.(PayoutRow).P.Ticker }},
		{Label: "net ↓", Less: func(a, b browser.Row) bool { return a.(PayoutRow).P.Net > b.(PayoutRow).P.Net }},
		{Label: "yield ↓", Less: func(a, b browser.Row) bool { return a.(PayoutRow).P.Yield > b.(PayoutRow).P.Yield }},
	}

	m := browser.New("Dividend Payouts",
		[]string{"DATE", "TICKER", "PERIOD", "STATUS", "GROSS", "TAX", "NET", "QTY", "PER SHR", "YIELD"},
		rows, sorts)
	m.FilterHelp = "keys: ticker:SBER  status:projected|confirmed  period:2024  isin:RU…  from:YYYY-MM-DD  to:YYYY-MM-DD  account:…  (free text matches ticker/asset/period/isin)"

	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "244", Dark: "240"})
	m.RowStyle = func(r browser.Row) lipgloss.Style {
		if r.(PayoutRow).P.Projected {
			return dimStyle
		}
		return lipgloss.NewStyle()
	}

	m.Footer = func(visible []browser.Row) string {
		var net, projectedNet float64
		var projectedCount int
		for _, r := range visible {
			row := r.(PayoutRow)
			if row.P.Projected {
				projectedNet += row.P.Net
				projectedCount++
			} else {
				net += row.P.Net
			}
		}
		if projectedCount == 0 {
			return fmt.Sprintf("net visible: %s", ui.FormatRUB(net))
		}
		return fmt.Sprintf("net: %s received  +  %s projected", ui.FormatRUB(net), ui.FormatRUB(projectedNet))
	}
	m.OnKey = func(msg tea.KeyMsg, _ browser.Row) (tea.Cmd, bool) { return nil, false }

	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func emptyDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// daysBetween is a coarse difference in days between two YYYY-MM-DD dates.
// For our cross-reference window (90-day fuzz factor) approximate calendar math is enough.
func daysBetween(a, b string) int {
	if len(a) < 10 || len(b) < 10 {
		return 9999
	}
	ay, am, ad := atoi(a[0:4]), atoi(a[5:7]), atoi(a[8:10])
	by, bm, bd := atoi(b[0:4]), atoi(b[5:7]), atoi(b[8:10])
	return (ay-by)*365 + (am-bm)*30 + (ad - bd)
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
