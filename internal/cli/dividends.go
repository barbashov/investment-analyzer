package cli

import (
	"fmt"
	"sort"
	"strings"

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
			if len(payments) == 0 {
				_, _ = fmt.Fprintln(ac.out, "no dividend payments in this range")
				return nil
			}
			// Yield needs cost-basis context from the *full* transaction history,
			// not just the date-filtered slice — cost basis builds up over time.
			allTxs, err := st.ListTransactions("", "")
			if err != nil {
				return apperr.Wrap("store_query", "list transactions for yield", 2, err)
			}
			payments = portfolio.AnnotatePayments(payments, allTxs)

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
