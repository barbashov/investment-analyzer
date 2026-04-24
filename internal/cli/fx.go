package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"investment-analyzer/internal/apperr"
	"investment-analyzer/internal/assets"
	"investment-analyzer/internal/portfolio"
	"investment-analyzer/internal/store"
	"investment-analyzer/internal/ui"
)

func newFXCmd(ac *appContext) *cobra.Command {
	return &cobra.Command{
		Use:   "fx",
		Short: "Currency / gold exposure summary (RUB-equivalent value of non-RUB positions)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(ac.opts.DBPath)
			if err != nil {
				return apperr.Wrap("store_open", "open database", 2, err)
			}
			defer func() { _ = st.Close() }()

			// Cumulative walk — must see all pre-window FX buys.
			txs, err := st.ListTransactions("", ac.opts.To)
			if err != nil {
				return apperr.Wrap("store_query", "list transactions", 2, err)
			}
			positions := portfolio.ComputePositions(txs, ac.opts.To)

			type row struct {
				ticker, qty, avg, last, marketRUB string
				marketVal                          float64
			}
			var rows []row
			var totalRUB float64

			for ticker, pos := range positions {
				if pos.Quantity <= 0 {
					continue
				}
				if assets.Classify(ticker) != assets.ClassCurrency {
					continue
				}
				last, err := st.LastPriceFor(ticker, true)
				marketStr := "—"
				lastStr := "—"
				marketVal := 0.0
				if err == nil {
					marketVal = pos.Quantity * last.Close
					marketStr = ui.FormatRUB(marketVal)
					lastStr = ui.FormatRUB(last.Close)
					totalRUB += marketVal
				}
				rows = append(rows, row{
					ticker:    ticker,
					qty:       fmt.Sprintf("%g", pos.Quantity),
					avg:       ui.FormatRUB(pos.AvgCost),
					last:      lastStr,
					marketRUB: marketStr,
					marketVal: marketVal,
				})
			}
			if len(rows) == 0 {
				_, _ = fmt.Fprintln(ac.out, "no FX/gold positions")
				return nil
			}

			// Sort by market value desc.
			for i := 1; i < len(rows); i++ {
				for j := i; j > 0 && rows[j].marketVal > rows[j-1].marketVal; j-- {
					rows[j], rows[j-1] = rows[j-1], rows[j]
				}
			}

			ui.PrintTable(ac.out,
				[]string{"PAIR", "QTY", "AVG COST", "LAST RATE", "MARKET RUB"},
				toCells(rows, func(r row) []string {
					return []string{r.ticker, r.qty, r.avg, r.last, r.marketRUB}
				}),
			)
			_, _ = fmt.Fprintln(ac.out, ui.NewHumanUI(ac.out).Title(
				fmt.Sprintf("Total FX exposure: %s", ui.FormatRUB(totalRUB)),
			))
			return nil
		},
	}
}

func toCells[T any](in []T, f func(T) []string) [][]string {
	out := make([][]string, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}
	return out
}
