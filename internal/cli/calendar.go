package cli

import (
	"fmt"
	"sort"
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
		Short: "Upcoming dividend dates for current holdings (MOEX confirmed + smart-lab projected)",
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
			today := now.Format("2006-01-02")
			cutoff := now.AddDate(0, 0, days).Format("2006-01-02")

			type entry struct {
				date      string
				ticker    string
				period    string
				source    string
				perShr    float64
				qty       float64
				project   float64
				projected bool
			}
			var rows []entry

			for ticker, pos := range positions {
				if pos.Quantity <= 0 {
					continue
				}

				moexDivs, err := st.ListMOEXDividends(ticker)
				if err != nil {
					_, _ = fmt.Fprintf(ac.err, "warn: %s moex: %v\n", ticker, err)
				}
				for _, d := range moexDivs {
					if d.RegistryDate < today || d.RegistryDate > cutoff {
						continue
					}
					rows = append(rows, entry{
						date:    d.RegistryDate,
						ticker:  ticker,
						period:  "—", // MOEX doesn't carry a period label
						source:  "moex",
						perShr:  d.Value,
						qty:     pos.Quantity,
						project: pos.Quantity * d.Value,
					})
				}

				slDivs, err := st.ListSmartlabDividends(ticker)
				if err != nil {
					_, _ = fmt.Fprintf(ac.err, "warn: %s smartlab: %v\n", ticker, err)
				}
				for _, sl := range slDivs {
					if sl.ExDate < today || sl.ExDate > cutoff {
						continue
					}
					if portfolio.SupersedesByMOEX(sl, moexDivs) {
						continue
					}
					rows = append(rows, entry{
						date:      sl.ExDate,
						ticker:    ticker,
						period:    sl.Period,
						source:    "smart-lab",
						perShr:    sl.ValuePerShare,
						qty:       pos.Quantity,
						project:   pos.Quantity * sl.ValuePerShare,
						projected: true,
					})
				}
			}

			if len(rows) == 0 {
				_, _ = fmt.Fprintf(ac.out, "no upcoming dividends within %d days (run `invest update` first if caches are empty)\n", days)
				return nil
			}

			sort.SliceStable(rows, func(i, j int) bool {
				if rows[i].date != rows[j].date {
					return rows[i].date < rows[j].date
				}
				return rows[i].ticker < rows[j].ticker
			})

			cells := make([][]string, 0, len(rows))
			dim := make([]bool, 0, len(rows))
			var totalConfirmed, totalProjected float64
			for _, r := range rows {
				cells = append(cells, []string{
					r.date,
					r.ticker,
					r.period,
					r.source,
					ui.FormatRUB(r.perShr),
					fmt.Sprintf("%g", r.qty),
					ui.FormatRUB(r.project),
				})
				dim = append(dim, r.projected)
				if r.projected {
					totalProjected += r.project
				} else {
					totalConfirmed += r.project
				}
			}
			ui.PrintTableWithRowStyles(ac.out,
				[]string{"DATE", "TICKER", "PERIOD", "SOURCE", "PER SHARE", "QTY", "PROJECTED"},
				cells, dim,
			)
			h := ui.NewHumanUI(ac.out)
			_, _ = fmt.Fprintln(ac.out, h.Title(
				fmt.Sprintf("Total gross, next %d days: %s confirmed  +  %s projected",
					days, ui.FormatRUB(totalConfirmed), ui.FormatRUB(totalProjected)),
			))
			_, _ = fmt.Fprintln(ac.out, h.Muted(
				"dim rows are smart-lab projections (board-recommended, not yet MOEX-confirmed)",
			))
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 90, "How many days ahead to look")
	return cmd
}
