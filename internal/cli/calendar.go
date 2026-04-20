package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"investment-analyzer/internal/apperr"
	"investment-analyzer/internal/portfolio"
	"investment-analyzer/internal/store"
	"investment-analyzer/internal/ui"
)

func newCalendarCmd(ac *appContext) *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:   "calendar",
		Short: "Upcoming dividend ex-dates from MOEX for currently held tickers",
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
			positions := portfolio.ComputePositions(txs, "")

			now := time.Now().UTC()
			cutoff := now.AddDate(0, 0, days).Format("2006-01-02")
			today := now.Format("2006-01-02")

			type entry struct {
				date    string
				ticker  string
				perShr  float64
				qty     float64
				project float64
			}
			var rows []entry

			for ticker, pos := range positions {
				if pos.Quantity <= 0 {
					continue
				}
				divs, err := st.ListMOEXDividends(ticker)
				if err != nil {
					_, _ = fmt.Fprintf(ac.err, "warn: %s: %v\n", ticker, err)
					continue
				}
				for _, d := range divs {
					if d.RegistryDate < today || d.RegistryDate > cutoff {
						continue
					}
					rows = append(rows, entry{
						date:    d.RegistryDate,
						ticker:  ticker,
						perShr:  d.Value,
						qty:     pos.Quantity,
						project: pos.Quantity * d.Value,
					})
				}
			}

			if len(rows) == 0 {
				_, _ = fmt.Fprintf(ac.out, "no upcoming dividends within %d days (run `invest update` first if MOEX cache is empty)\n", days)
				return nil
			}

			// Sort by date asc.
			for i := 1; i < len(rows); i++ {
				for j := i; j > 0 && rows[j].date < rows[j-1].date; j-- {
					rows[j], rows[j-1] = rows[j-1], rows[j]
				}
			}

			cells := make([][]string, 0, len(rows))
			var total float64
			for _, r := range rows {
				cells = append(cells, []string{
					r.date,
					r.ticker,
					ui.FormatRUB(r.perShr),
					fmt.Sprintf("%g", r.qty),
					ui.FormatRUB(r.project),
				})
				total += r.project
			}
			ui.PrintTable(ac.out,
				[]string{"REGISTRY", "TICKER", "PER SHARE", "QTY", "PROJECTED"},
				cells,
			)
			_, _ = fmt.Fprintln(ac.out, ui.NewHumanUI(ac.out).Title(
				fmt.Sprintf("Total projected (gross, next %d days): %s", days, ui.FormatRUB(total)),
			))
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 90, "How many days ahead to look")
	return cmd
}
