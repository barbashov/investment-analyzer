package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"investment-analyzer/internal/apperr"
	"investment-analyzer/internal/portfolio"
	"investment-analyzer/internal/store"
	"investment-analyzer/internal/tui/payouts"
	"investment-analyzer/internal/ui"
)

func newDividendsCmd(ac *appContext) *cobra.Command {
	var (
		groupBy string
		gross   bool
	)
	cmd := &cobra.Command{
		Use:   "dividends",
		Short: "Show received dividends grouped by ticker, year, or month",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(ac.opts.DBPath)
			if err != nil {
				return apperr.Wrap("store_open", "open database", 2, err)
			}
			defer func() { _ = st.Close() }()

			txs, err := st.ListTransactions(ac.opts.From, ac.opts.To)
			if err != nil {
				return apperr.Wrap("store_query", "list transactions", 2, err)
			}

			resolver := portfolio.MapTickerResolver{ByISIN: portfolio.DefaultISINTickerMap}
			payments := portfolio.ExtractDividends(txs, resolver)

			var keyFn func(portfolio.DividendPayment) string
			var headerLabel string
			switch strings.ToLower(groupBy) {
			case "ticker", "":
				keyFn = portfolio.ByTicker
				headerLabel = "TICKER"
			case "year":
				keyFn = portfolio.ByYear
				headerLabel = "YEAR"
			case "month":
				keyFn = portfolio.ByMonth
				headerLabel = "MONTH"
			default:
				return apperr.New("validation", fmt.Sprintf("--by must be ticker|year|month, got %q", groupBy), 2)
			}

			// The projection path and the yield calculation both need cost-basis
			// context from the full transaction history (not just the date-filtered
			// slice), so fetch once up front.
			allTxs, err := st.ListTransactions("", "")
			if err != nil {
				return apperr.Wrap("store_query", "list transactions for yield", 2, err)
			}

			if len(payments) > 0 {
				payments = portfolio.AnnotatePayments(payments, allTxs)

				buckets := portfolio.GroupBy(payments, keyFn)
				if g := strings.ToLower(groupBy); g == "year" || g == "month" {
					sort.SliceStable(buckets, func(i, j int) bool { return buckets[i].Key < buckets[j].Key })
				}

				rangeFrom, rangeTo := payments[0].Date, payments[len(payments)-1].Date
				_, _ = fmt.Fprintln(ac.out, ui.NewHumanUI(ac.out).Title(
					fmt.Sprintf("Dividends — %s → %s", rangeFrom, rangeTo),
				))
				_, _ = fmt.Fprintln(ac.out)

				var headers []string
				if gross {
					headers = []string{headerLabel, "PAYMENTS", "GROSS", "YIELD"}
				} else {
					headers = []string{headerLabel, "PAYMENTS", "GROSS", "TAX", "NET", "YIELD"}
				}

				rows := make([][]string, 0, len(buckets)+1)
				var totGross, totTax, totNet, totBookValue float64
				var totPayments int
				for _, b := range buckets {
					row := []string{b.Key, fmt.Sprintf("%d", b.Payments), ui.FormatRUB(b.Gross)}
					if !gross {
						row = append(row, ui.FormatRUB(b.Tax), ui.FormatRUB(b.Net))
					}
					row = append(row, formatYield(b.Yield))
					rows = append(rows, row)
					totGross += b.Gross
					totTax += b.Tax
					totNet += b.Net
					totBookValue += b.BookValueSum
					totPayments += b.Payments
				}
				var totYield float64
				if totBookValue > 0 {
					totYield = totGross / totBookValue
				}
				totalRow := []string{"TOTAL", fmt.Sprintf("%d", totPayments), ui.FormatRUB(totGross)}
				if !gross {
					totalRow = append(totalRow, ui.FormatRUB(totTax), ui.FormatRUB(totNet))
				}
				totalRow = append(totalRow, formatYield(totYield))
				rows = append(rows, totalRow)

				ui.PrintTable(ac.out, headers, rows)
			}

			// Projected section: synthesize upcoming payouts from smart-lab
			// announcements (dedup'd against MOEX confirmations) and render
			// them in the same grouping as the historical table, dimmed.
			projected := printProjectedSection(ac, st, allTxs, keyFn, headerLabel, groupBy, gross)

			if len(payments) == 0 && !projected {
				_, _ = fmt.Fprintln(ac.out, "no dividend payments (received or projected) in this range")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&groupBy, "by", "ticker", "Grouping: ticker|year|month")
	cmd.Flags().BoolVar(&gross, "gross", false, "Show only gross totals (hide tax & net)")

	cmd.AddCommand(newDividendsPayoutsCmd(ac))
	return cmd
}

// formatYield renders a fractional yield (0.124 → "12.4%") or "—" when book value is missing.
func formatYield(yield float64) string {
	if yield <= 0 {
		return "—"
	}
	return ui.FormatPct(yield * 100)
}

// printProjectedSection appends a "Projected (smart-lab, next 90 days)" table
// below the historical dividends. Values are best-effort: Tax uses the 13%
// Russian-resident stock heuristic (see portfolio.RUStockTaxRate). Rendered
// entirely dim to signal uncertainty. Returns true iff a table was rendered.
func printProjectedSection(
	ac *appContext,
	st *store.Store,
	txs []*store.Transaction,
	keyFn func(portfolio.DividendPayment) string,
	headerLabel, groupBy string,
	gross bool,
) bool {
	announcements, err := st.ListAllSmartlabDividends()
	if err != nil || len(announcements) == 0 {
		return false
	}
	moexByTicker := map[string][]store.MOEXDividend{}
	seen := map[string]bool{}
	for _, a := range announcements {
		if seen[a.Ticker] {
			continue
		}
		seen[a.Ticker] = true
		if divs, err := st.ListMOEXDividends(a.Ticker); err == nil {
			moexByTicker[a.Ticker] = divs
		}
	}

	asOf := time.Now().UTC()
	projected := portfolio.ProjectPayments(announcements, moexByTicker, txs, asOf)
	if len(projected) == 0 {
		return false
	}

	buckets := portfolio.GroupBy(projected, keyFn)
	if g := strings.ToLower(groupBy); g == "year" || g == "month" {
		sort.SliceStable(buckets, func(i, j int) bool { return buckets[i].Key < buckets[j].Key })
	}

	var headers []string
	if gross {
		headers = []string{headerLabel, "PAYMENTS", "GROSS", "YIELD"}
	} else {
		headers = []string{headerLabel, "PAYMENTS", "GROSS", "TAX", "NET", "YIELD"}
	}

	rows := make([][]string, 0, len(buckets)+1)
	dim := make([]bool, 0, len(buckets)+1)
	var totGross, totTax, totNet, totBookValue float64
	var totPayments int
	for _, b := range buckets {
		row := []string{b.Key, fmt.Sprintf("%d", b.Payments), ui.FormatRUB(b.Gross)}
		if !gross {
			row = append(row, ui.FormatRUB(b.Tax), ui.FormatRUB(b.Net))
		}
		row = append(row, formatYield(b.Yield))
		rows = append(rows, row)
		dim = append(dim, true)
		totGross += b.Gross
		totTax += b.Tax
		totNet += b.Net
		totBookValue += b.BookValueSum
		totPayments += b.Payments
	}
	var totYield float64
	if totBookValue > 0 {
		totYield = totGross / totBookValue
	}
	totalRow := []string{"TOTAL", fmt.Sprintf("%d", totPayments), ui.FormatRUB(totGross)}
	if !gross {
		totalRow = append(totalRow, ui.FormatRUB(totTax), ui.FormatRUB(totNet))
	}
	totalRow = append(totalRow, formatYield(totYield))
	rows = append(rows, totalRow)
	dim = append(dim, true)

	_, _ = fmt.Fprintln(ac.out)
	h := ui.NewHumanUI(ac.out)
	_, _ = fmt.Fprintln(ac.out, h.Title("Projected (smart-lab, upcoming)"))
	_, _ = fmt.Fprintln(ac.out, h.Muted("tax estimated at 13% (RU resident, stocks) — heuristic"))
	ui.PrintTableWithRowStyles(ac.out, headers, rows, dim)
	return true
}

func newDividendsPayoutsCmd(ac *appContext) *cobra.Command {
	return &cobra.Command{
		Use:   "payouts",
		Short: "Interactive per-payment dividend browser with MOEX cross-reference",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(ac.opts.DBPath)
			if err != nil {
				return apperr.Wrap("store_open", "open database", 2, err)
			}
			defer func() { _ = st.Close() }()
			return payouts.Run(st)
		},
	}
}
