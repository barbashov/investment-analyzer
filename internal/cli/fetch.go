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
		Short: "Fetch MOEX + smart-lab data for one ticker (low-level; use `invest update` for everyday refresh)",
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

			t := strings.ToUpper(ticker)
			if refresh {
				// Reset the per-ticker fetch state so we re-pull from scratch.
				if err := st.ResetFetchState(t); err != nil {
					return apperr.Wrap("store_write", "reset fetch_state", 2, err)
				}
			}

			ctx := context.Background()
			refreshers := buildRefreshers(st)
			_, _ = fmt.Fprintf(ac.out, "%s:\n", t)

			var failed int
			for _, r := range refreshers {
				class := assetClassForRouting(st, t)
				if !r.AppliesTo(class) {
					continue
				}
				res := r.RefreshTicker(ctx, t, refresh)
				printRefreshResult(ac, res)
				if res.Err != nil {
					failed++
				}
			}
			if failed > 0 {
				return apperr.New("fetch_partial", fmt.Sprintf("%d source(s) failed", failed), 3)
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
		Short: "Refresh MOEX prices/dividends and smart-lab projections for all current holdings",
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

			ctx := context.Background()
			refreshers := buildRefreshers(st)
			var failed int
			for i, t := range tickers {
				_, _ = fmt.Fprintf(ac.out, "[%d/%d] %s\n", i+1, len(tickers), t)
				for _, r := range refreshers {
					class := assetClassForRouting(st, t)
					if !r.AppliesTo(class) {
						continue
					}
					// force=true bypasses per-source staleness windows on user-initiated update.
					res := r.RefreshTicker(ctx, t, true)
					printRefreshResult(ac, res)
					if res.Err != nil {
						failed++
					}
				}
			}
			if failed > 0 {
				return apperr.New("update_partial", fmt.Sprintf("%d source(s) failed", failed), 3)
			}
			return nil
		},
	}
	return cmd
}
