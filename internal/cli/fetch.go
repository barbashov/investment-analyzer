package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"investment-analyzer/internal/apperr"
	"investment-analyzer/internal/moex"
	"investment-analyzer/internal/store"
)

func newFetchCmd(ac *appContext) *cobra.Command {
	var (
		ticker  string
		refresh bool
	)
	cmd := &cobra.Command{
		Use:   "fetch",
		Short: "Fetch MOEX prices+dividends for one ticker (low-level; use `invest update` for everyday refresh)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(ticker) == "" {
				return apperr.New("validation", "--ticker is required", 2)
			}
			st, err := store.Open(ac.opts.DBPath)
			if err != nil {
				return apperr.Wrap("store_open", "open database", 2, err)
			}
			defer func() { _ = st.Close() }()

			if refresh {
				// Reset the per-ticker fetch state so we re-pull from scratch.
				if err := st.ResetFetchState(strings.ToUpper(ticker)); err != nil {
					return apperr.Wrap("store_write", "reset fetch_state", 2, err)
				}
			}

			updater := moex.NewUpdater(moex.NewClient(), st)
			res := updater.UpdateTicker(context.Background(), strings.ToUpper(ticker), refresh)
			printUpdateResult(ac, res)
			if res.PriceErr != nil {
				return apperr.Wrap("moex", "price fetch", 3, res.PriceErr)
			}
			if res.DividendErr != nil {
				return apperr.Wrap("moex", "dividend fetch", 3, res.DividendErr)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ticker, "ticker", "", "Ticker to fetch (required)")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Reset fetch_state and re-pull from scratch")
	return cmd
}

func newUpdateCmd(ac *appContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Refresh MOEX prices and dividends for all currently held tickers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(ac.opts.DBPath)
			if err != nil {
				return apperr.Wrap("store_open", "open database", 2, err)
			}
			defer func() { _ = st.Close() }()

			tickers, err := moex.CurrentHoldingsTickers(st)
			if err != nil {
				return apperr.Wrap("store_query", "list holdings", 2, err)
			}
			if len(tickers) == 0 {
				_, _ = fmt.Fprintln(ac.out, "no current holdings — nothing to update")
				return nil
			}

			updater := moex.NewUpdater(moex.NewClient(), st)
			ctx := context.Background()
			var failed int
			for i, t := range tickers {
				_, _ = fmt.Fprintf(ac.out, "[%d/%d] ", i+1, len(tickers))
				res := updater.UpdateTicker(ctx, t, true) // bypass dividend staleness on user-initiated update
				printUpdateResult(ac, res)
				if res.PriceErr != nil || res.DividendErr != nil {
					failed++
				}
			}
			if failed > 0 {
				return apperr.New("update_partial", fmt.Sprintf("%d ticker(s) failed", failed), 3)
			}
			return nil
		},
	}
	return cmd
}

func printUpdateResult(ac *appContext, r moex.UpdateResult) {
	parts := []string{fmt.Sprintf("%-12s [%s]", r.Ticker, r.AssetClass)}
	if r.PriceErr != nil {
		parts = append(parts, "prices=ERR("+r.PriceErr.Error()+")")
	} else {
		parts = append(parts, fmt.Sprintf("+%d prices", r.NewPrices))
	}
	switch {
	case r.DividendErr != nil:
		parts = append(parts, "dividends=ERR("+r.DividendErr.Error()+")")
	case r.DividendSkip:
		parts = append(parts, "dividends=cached")
	default:
		parts = append(parts, fmt.Sprintf("+%d dividends", r.NewDividends))
	}
	_, _ = fmt.Fprintln(ac.out, strings.Join(parts, "  "))
}
