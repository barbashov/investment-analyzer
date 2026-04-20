package cli

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"investment-analyzer/internal/apperr"
	"investment-analyzer/internal/assets"
	"investment-analyzer/internal/portfolio"
	"investment-analyzer/internal/store"
	"investment-analyzer/internal/ui"
)

// roiSnapshot is the cumulative state used for ROI math at a single point in time.
type roiSnapshot struct {
	Date            string
	Deposits        float64
	Withdrawals     float64
	DividendsNet    float64
	RealizedPnL     float64
	OtherIncome     float64
	Commissions     float64
	TaxesNDFL       float64
	BookValue       float64 // book value of open positions
	MarketValue     float64 // mark-to-market at Date (uses BookValue fallback when no cached price)
	UnrealizedPnL   float64 // MarketValue - BookValue
	CashBalance     float64 // running cash balance (all cash deltas, including BUY/SELL amounts)
	PhantomInvested float64 // implied pre-history capital (max shortfall of CashBalance)
	MissingPrices   []string
}

// Invested returns the external principal contributed, including phantom capital
// inferred from pre-history BUYs that weren't funded by a recorded DEPOSIT.
func (s roiSnapshot) Invested() float64 {
	return (s.Deposits - s.Withdrawals) + s.PhantomInvested
}

// Earned is the total return: dividends + realized + unrealized + other income,
// net of commissions and taxes.
func (s roiSnapshot) Earned() float64 {
	return s.DividendsNet + s.RealizedPnL + s.UnrealizedPnL + s.OtherIncome - s.Commissions - s.TaxesNDFL
}

// Equity is what the account is worth right now: residual cash + market value
// of open positions. PhantomInvested compensates the synthetic negative cash
// carry when history starts mid-stream; for full histories it's zero and the
// identity CashBalance + MarketValue + PhantomInvested = Invested + Earned holds.
func (s roiSnapshot) Equity() float64 {
	return s.CashBalance + s.MarketValue + s.PhantomInvested
}

// computeROISnapshot walks all transactions, applying only those with date <= asOf,
// and uses st.PriceAsOf for historical mark-to-market. asOf="" means "use latest cached".
//
// Cash accounting: every cash-affecting op (including BUY/SELL amounts) moves
// CashBalance. If the minimum running balance drops below zero — which happens
// whenever a BUY appears without a preceding DEPOSIT, as in partial Finam
// exports — the magnitude is captured as PhantomInvested ("history implies at
// least this much pre-CSV capital came from outside"). This keeps
// Equity = CashBalance + MarketValue + PhantomInvested honest for both full and
// partial histories. Ticker-less BUYs (e.g. some Finam OFZ rows) are handled
// the same way: they drain cash like any BUY but contribute nothing to
// MarketValue, so the phantom-invested adjustment covers them implicitly.
func computeROISnapshot(st *store.Store, txs []*store.Transaction, asOf string) roiSnapshot {
	snap := roiSnapshot{Date: asOf}
	// Walk chronologically so CashBalance tracks the real running balance.
	sorted := append([]*store.Transaction(nil), txs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Date != sorted[j].Date {
			return sorted[i].Date < sorted[j].Date
		}
		return sorted[i].Time < sorted[j].Time
	})

	divPays := portfolio.ExtractDividends(txs, portfolio.MapTickerResolver{ByISIN: portfolio.DefaultISINTickerMap})
	// Index dividend net cash by date so we can fold it into the running balance in order.
	divByDate := map[string]float64{}
	for _, p := range divPays {
		if asOf != "" && p.Date > asOf {
			continue
		}
		snap.DividendsNet += p.Net
		divByDate[p.Date] += p.Net
	}

	minCash := 0.0
	seenDates := map[string]bool{}
	applyDivCash := func(date string) {
		if seenDates[date] {
			return
		}
		seenDates[date] = true
		if v, ok := divByDate[date]; ok {
			snap.CashBalance += v
			if snap.CashBalance < minCash {
				minCash = snap.CashBalance
			}
		}
	}

	for _, t := range sorted {
		if asOf != "" && t.Date > asOf {
			continue
		}
		applyDivCash(t.Date)
		switch t.OpType {
		case store.OpDeposit:
			snap.Deposits += t.Amount
			snap.CashBalance += t.Amount
		case store.OpWithdrawal:
			snap.Withdrawals += t.Amount
			snap.CashBalance -= t.Amount
		case store.OpCommission:
			snap.Commissions += t.Amount
			snap.CashBalance -= t.Amount
		case store.OpTax:
			snap.TaxesNDFL += t.Amount
			snap.CashBalance -= t.Amount
		case store.OpIncome:
			snap.OtherIncome += t.Amount
			snap.CashBalance += t.Amount
		case store.OpBuy, store.OpFXBuy:
			snap.CashBalance -= t.Amount
			// OpSecurityIn is deliberately excluded: custody transfer doesn't move cash.
		case store.OpSell, store.OpFXSell:
			snap.CashBalance += t.Amount
		}
		if snap.CashBalance < minCash {
			minCash = snap.CashBalance
		}
	}
	// Dividends dated after the last transaction (rare but possible in fixtures).
	for date := range divByDate {
		applyDivCash(date)
	}

	if minCash < 0 {
		snap.PhantomInvested = -minCash
	}

	positions := portfolio.ComputePositions(txs, asOf)
	for ticker, pos := range positions {
		snap.RealizedPnL += pos.RealizedPnL
		if pos.Quantity <= 0 {
			continue
		}
		snap.BookValue += pos.BookValue
		class := assets.Classify(ticker)
		var lp store.MOEXPrice
		var err error
		if asOf == "" {
			lp, err = st.LastPriceFor(ticker, class == assets.ClassCurrency)
		} else {
			lp, err = st.PriceAsOf(ticker, asOf, class == assets.ClassCurrency)
		}
		// Fall back to book value when: (a) no price cached, or (b) the cached price is
		// too stale relative to asOf — e.g. Finex ETFs frozen since Feb 2022 sanctions
		// have no post-2022 quotes, so pricing them as of Dec 2024 gives nonsense.
		if err != nil || (asOf != "" && daysBetweenDates(lp.Date, asOf) > 180) {
			snap.MarketValue += pos.BookValue
			snap.MissingPrices = append(snap.MissingPrices, ticker)
			continue
		}
		snap.MarketValue += pos.Quantity * lp.Close
	}
	snap.UnrealizedPnL = snap.MarketValue - snap.BookValue
	return snap
}

func newROICmd(ac *appContext) *cobra.Command {
	var noFetch bool
	cmd := &cobra.Command{
		Use:   "roi",
		Short: "Total return: money invested vs money earned (lifetime + yearly breakdown)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(ac.opts.DBPath)
			if err != nil {
				return apperr.Wrap("store_open", "open database", 2, err)
			}
			defer func() { _ = st.Close() }()

			txs, err := st.ListTransactions("", "")
			if err != nil {
				return apperr.Wrap("store_query", "list transactions", 2, err)
			}
			if len(txs) == 0 {
				_, _ = fmt.Fprintln(ac.out, "no transactions — nothing to report")
				return nil
			}

			var firstDeposit, firstTx, lastTx string
			for _, t := range txs {
				if firstTx == "" || t.Date < firstTx {
					firstTx = t.Date
				}
				if t.Date > lastTx {
					lastTx = t.Date
				}
				if t.OpType == store.OpDeposit {
					if firstDeposit == "" || t.Date < firstDeposit {
						firstDeposit = t.Date
					}
				}
			}

			positions := portfolio.ComputePositions(txs, "")
			if !noFetch {
				ensurePricesCached(ac, st, positions)
			}

			now := computeROISnapshot(st, txs, "")
			invested := now.Invested()
			earned := now.Earned()
			equity := now.Equity()

			var roi, cagr, years float64
			if invested > 0 {
				roi = earned / invested * 100
			}
			if firstDeposit != "" {
				if t, err := time.Parse("2006-01-02", firstDeposit); err == nil {
					years = time.Since(t).Hours() / 24 / 365.25
					if years > 0 && invested > 0 && earned > -invested {
						cagr = (math.Pow(1+earned/invested, 1/years) - 1) * 100
					}
				}
			}

			uh := ui.NewHumanUI(ac.out)
			header := "Return on Investment — lifetime"
			if firstDeposit != "" {
				header = fmt.Sprintf("Return on Investment — since %s (%.2f years)", firstDeposit, years)
			}
			_, _ = fmt.Fprintln(ac.out, uh.Title(header))
			_, _ = fmt.Fprintln(ac.out)

			ui.PrintKVBlock(ac.out, "Capital", []ui.KVField{
				{Label: "Deposits", Value: ui.FormatRUB(now.Deposits)},
				{Label: "Withdrawals", Value: ui.FormatRUB(now.Withdrawals)},
				{Label: "Net invested", Value: ui.FormatRUB(invested)},
			})
			_, _ = fmt.Fprintln(ac.out)

			ui.PrintKVBlock(ac.out, "Earnings breakdown", []ui.KVField{
				{Label: "Dividends (net)", Value: ui.FormatRUB(now.DividendsNet)},
				{Label: "Realized P&L", Value: ui.FormatRUB(now.RealizedPnL)},
				{Label: "Unrealized P&L", Value: ui.FormatRUB(now.UnrealizedPnL)},
				{Label: "Other income", Value: ui.FormatRUB(now.OtherIncome)},
				{Label: "Commissions paid", Value: "-" + ui.FormatRUB(now.Commissions)},
				{Label: "Taxes (НДФЛ, separate)", Value: "-" + ui.FormatRUB(now.TaxesNDFL)},
				{Label: "Total earned", Value: ui.FormatRUB(earned)},
			})
			_, _ = fmt.Fprintln(ac.out)

			returnRows := []ui.KVField{
				{Label: "Current equity", Value: ui.FormatRUB(equity)},
				{Label: "Market value of holdings", Value: ui.FormatRUB(now.MarketValue)},
				{Label: "Cash balance", Value: ui.FormatRUB(now.CashBalance)},
			}
			if now.PhantomInvested > 0 {
				returnRows = append(returnRows, ui.KVField{
					Label: "Pre-history capital (implied)",
					Value: ui.FormatRUB(now.PhantomInvested),
				})
			}
			returnRows = append(returnRows,
				ui.KVField{Label: "ROI", Value: ui.FormatPct(roi)},
				ui.KVField{Label: "Annualized (CAGR)", Value: ui.FormatPct(cagr)},
			)
			ui.PrintKVBlock(ac.out, "Return", returnRows)
			_, _ = fmt.Fprintln(ac.out)

			// Yearly breakdown — start from the year of the first deposit because
			// pre-deposit BUYs (Finam CSV often starts mid-history) yield meaningless
			// ROI: the model sees "stocks acquired with no cash" and equity goes negative.
			startYear := firstDeposit
			if startYear == "" {
				startYear = firstTx
			}
			if startYear != "" && lastTx != "" {
				printYearlyROI(ac, st, txs, yearOf(startYear), yearOf(lastTx))
			}

			if len(now.MissingPrices) > 0 {
				_, _ = fmt.Fprintln(ac.out)
				_, _ = fmt.Fprintf(ac.err, "note: no cached MOEX price for %d ticker(s): %v — run `invest update` to include them in unrealized P&L\n",
					len(now.MissingPrices), now.MissingPrices)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "Skip auto-fetching missing MOEX prices")
	return cmd
}

// printYearlyROI walks each calendar year from firstYear..lastYear and reports
// the return earned during that year (Δ equity − net deposits during the year).
//
// Capital base uses equity_start + net_deposits_in_year/2 — a midpoint
// approximation for deposits/withdrawals that happen partway through the year.
// First year's equity_start is 0; we use net_deposits/2 alone.
//
// Year-end equity uses the most recent MOEX close on or before Dec 31. For
// historical years where no prices were cached, market value defaults to 0
// (so ROI for those years reflects only dividends + realized + cash flows).
func printYearlyROI(ac *appContext, st *store.Store, txs []*store.Transaction, yFrom, yTo int) {
	if yFrom == 0 || yTo == 0 || yTo < yFrom {
		return
	}
	uh := ui.NewHumanUI(ac.out)
	_, _ = fmt.Fprintln(ac.out, uh.Title("Yearly ROI"))

	var rows [][]string
	// equity just before first year — captures any pre-period holdings at book value
	prev := computeROISnapshot(st, txs, fmt.Sprintf("%d-01-01", yFrom))

	for y := yFrom; y <= yTo; y++ {
		end := fmt.Sprintf("%d-12-31", y)
		snap := computeROISnapshot(st, txs, end)

		var depY, wdY float64
		for _, t := range txs {
			if t.Date < fmt.Sprintf("%d-01-01", y) || t.Date > end {
				continue
			}
			switch t.OpType {
			case store.OpDeposit:
				depY += t.Amount
			case store.OpWithdrawal:
				wdY += t.Amount
			}
		}
		netDepY := depY - wdY
		earnedY := (snap.Equity() - prev.Equity()) - netDepY
		baseY := prev.Equity() + netDepY/2
		roiCell := "n/a"
		if baseY > 0 {
			roiCell = ui.FormatPct(earnedY / baseY * 100)
		}

		rows = append(rows, []string{
			fmt.Sprintf("%d", y),
			ui.FormatRUB(netDepY),
			ui.FormatRUB(earnedY),
			ui.FormatRUB(snap.Equity()),
			roiCell,
		})
		prev = snap
	}
	ui.PrintTable(ac.out,
		[]string{"YEAR", "NET DEPOSITS", "EARNED", "EQUITY (EOY)", "ROI"},
		rows,
	)
}

// daysBetweenDates returns calendar days between two YYYY-MM-DD strings. Positive when b > a.
// Returns 0 on unparseable input.
func daysBetweenDates(a, b string) int {
	ta, errA := time.Parse("2006-01-02", a)
	tb, errB := time.Parse("2006-01-02", b)
	if errA != nil || errB != nil {
		return 0
	}
	return int(tb.Sub(ta).Hours() / 24)
}

func yearOf(date string) int {
	if t, err := time.Parse("2006-01-02", date); err == nil {
		return t.Year()
	}
	return 0
}
