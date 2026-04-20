package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"investment-analyzer/internal/apperr"
	"investment-analyzer/internal/portfolio"
	"investment-analyzer/internal/store"
	"investment-analyzer/internal/ui"
)

func newPricesCmd(ac *appContext) *cobra.Command {
	var (
		watch    bool
		interval time.Duration
	)
	cmd := &cobra.Command{
		Use:   "prices",
		Short: "Mark-to-market table for current holdings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(ac.opts.DBPath)
			if err != nil {
				return apperr.Wrap("store_open", "open database", 2, err)
			}
			defer func() { _ = st.Close() }()

			render := func() error {
				if watch {
					_, _ = fmt.Fprint(ac.out, ui.AnsiClearScreen)
				}
				txs, err := st.ListTransactions(ac.opts.From, ac.opts.To)
				if err != nil {
					return err
				}
				positions := portfolio.ComputePositions(txs, ac.opts.To)
				divs := map[string]float64{}
				for _, p := range portfolio.ExtractDividends(txs, portfolio.MapTickerResolver{ByISIN: portfolio.DefaultISINTickerMap}) {
					if p.Ticker != "" {
						divs[p.Ticker] += p.Net
					}
				}
				rows := buildPositionsRows(st, positions, divs)
				if len(rows) == 0 {
					_, _ = fmt.Fprintln(ac.out, "no open positions")
					return nil
				}
				ui.PrintTable(ac.out,
					[]string{"TICKER", "CLASS", "QTY", "AVG COST", "BOOK", "LAST", "MARKET", "P&L %", "DIV NET"},
					rows,
				)
				if watch {
					_, _ = fmt.Fprintln(ac.out, ui.NewHumanUI(ac.out).Muted(
						fmt.Sprintf("watching every %s — Ctrl-C to exit", interval),
					))
				}
				return nil
			}

			if !watch {
				return render()
			}
			ctx := context.Background()
			return ui.PollLoop(ctx, interval, render)
		},
	}
	cmd.Flags().BoolVar(&watch, "watch", false, "Refresh continuously")
	cmd.Flags().DurationVar(&interval, "interval", 30*time.Second, "Refresh interval (use with --watch)")
	return cmd
}
