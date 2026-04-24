package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"investment-analyzer/internal/apperr"
	"investment-analyzer/internal/assets"
	"investment-analyzer/internal/moex"
	"investment-analyzer/internal/portfolio"
	"investment-analyzer/internal/store"
	"investment-analyzer/internal/ui"
)

func newPositionsCmd(ac *appContext) *cobra.Command {
	var noFetch bool
	cmd := &cobra.Command{
		Use:   "positions",
		Short: "Show current holdings with FIFO cost basis and dividend totals",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(ac.opts.DBPath)
			if err != nil {
				return apperr.Wrap("store_open", "open database", 2, err)
			}
			defer func() { _ = st.Close() }()

			// Positions are a cumulative walk — must see all pre-window buys.
			// --to still bounds the snapshot date via ComputePositions.
			txs, err := st.ListTransactions("", ac.opts.To)
			if err != nil {
				return apperr.Wrap("store_query", "list transactions", 2, err)
			}
			positions := portfolio.ComputePositions(txs, ac.opts.To)

			// Sum dividends per ticker (resolved via ISIN map).
			divsByTicker := map[string]float64{}
			divPays := portfolio.ExtractDividends(txs, portfolio.MapTickerResolver{ByISIN: portfolio.DefaultISINTickerMap})
			for _, p := range divPays {
				if p.Ticker != "" {
					divsByTicker[p.Ticker] += p.Net
				}
			}

			if !noFetch {
				ensurePricesCached(ac, st, positions)
			}

			rows := buildPositionsRows(st, positions, divsByTicker)
			if len(rows) == 0 {
				_, _ = fmt.Fprintln(ac.out, "no open positions")
				return nil
			}
			ui.PrintTable(ac.out,
				[]string{"TICKER", "CLASS", "QTY", "AVG COST", "BOOK", "LAST", "MARKET", "P&L %", "DIV NET"},
				rows,
			)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "Skip auto-fetching missing MOEX prices")
	return cmd
}

// ensurePricesCached fetches MOEX prices for any held ticker that has no cached price yet.
// Tickers we already have at least one row for are left alone (use `invest update` to extend).
func ensurePricesCached(ac *appContext, st *store.Store, positions map[string]*portfolio.Position) {
	updater := moex.NewUpdater(moex.NewClient(), st)
	for ticker, pos := range positions {
		if pos.Quantity <= 0 {
			continue
		}
		class := assets.Classify(ticker)
		if _, err := st.LastPriceFor(ticker, class == assets.ClassCurrency); err == nil {
			continue // already have something
		} else if !errors.Is(err, store.ErrNotFound) {
			_, _ = fmt.Fprintf(ac.err, "warn: %s lookup: %v\n", ticker, err)
			continue
		}
		res := updater.UpdateTicker(context.Background(), ticker, false)
		if res.PriceErr != nil {
			_, _ = fmt.Fprintf(ac.err, "warn: %s fetch: %v\n", ticker, res.PriceErr)
		}
	}
}

func buildPositionsRows(st *store.Store, positions map[string]*portfolio.Position, divs map[string]float64) [][]string {
	type row struct {
		ticker, class, qty, avg, book, last, market, pnl, div string
		marketVal                                              float64
	}
	var rs []row
	for ticker, pos := range positions {
		if pos.Quantity <= 0 {
			continue
		}
		class := assets.Classify(ticker)
		last, err := st.LastPriceFor(ticker, class == assets.ClassCurrency)
		marketVal := 0.0
		lastStr := "—"
		marketStr := "—"
		pnlStr := "—"
		if err == nil {
			marketVal = pos.Quantity * last.Close
			lastStr = ui.FormatRUB(last.Close)
			marketStr = ui.FormatRUB(marketVal)
			if pos.BookValue > 0 {
				pnl := (marketVal - pos.BookValue) / pos.BookValue * 100
				pnlStr = ui.FormatPct(pnl)
			}
		}
		rs = append(rs, row{
			ticker:    ticker,
			class:     string(class),
			qty:       fmt.Sprintf("%g", pos.Quantity),
			avg:       ui.FormatRUB(pos.AvgCost),
			book:      ui.FormatRUB(pos.BookValue),
			last:      lastStr,
			market:    marketStr,
			pnl:       pnlStr,
			div:       ui.FormatRUB(divs[ticker]),
			marketVal: marketVal,
		})
	}
	// Sort by market value descending so the biggest positions float to the top.
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j].marketVal > rs[j-1].marketVal; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
	out := make([][]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, []string{r.ticker, r.class, r.qty, r.avg, r.book, r.last, r.market, r.pnl, r.div})
	}
	return out
}
