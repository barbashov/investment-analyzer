package cli

import (
	"context"
	"fmt"

	"investment-analyzer/internal/assets"
	"investment-analyzer/internal/moex"
	"investment-analyzer/internal/smartlab"
	"investment-analyzer/internal/store"
)

// tickerRefresher is the CLI-level abstraction that lets `invest update` /
// `invest fetch` iterate over multiple external data sources uniformly
// without any source-package knowing about the others. MOEX and smart-lab
// are genuinely different (MOEX provides prices + confirmed dividends, while
// smart-lab only projects upcoming dividends), so the interface is small on
// purpose: each adapter translates its domain result into a flat progress row.
type tickerRefresher interface {
	Name() string
	AppliesTo(class assets.Class) bool
	RefreshTicker(ctx context.Context, ticker string, force bool) refreshResult
}

// refreshResult is a source-agnostic per-ticker progress line suitable for
// console output. Detail carries source-specific flavour ("+3 prices, +1 dividend")
// so a human reader gets useful numbers without the CLI needing to know the
// shape of any source's native result type.
type refreshResult struct {
	Source  string
	Ticker  string
	Class   assets.Class
	Skipped bool
	Err     error
	Detail  string
}

// --- MOEX adapter --------------------------------------------------------

type moexRefresher struct{ u *moex.Updater }

func (m *moexRefresher) Name() string                       { return "moex" }
func (m *moexRefresher) AppliesTo(_ assets.Class) bool      { return true } // prices for every class
func (m *moexRefresher) RefreshTicker(ctx context.Context, ticker string, force bool) refreshResult {
	r := m.u.UpdateTicker(ctx, ticker, force)
	out := refreshResult{
		Source: "moex",
		Ticker: ticker,
		Class:  r.AssetClass,
	}
	// Combine the two sub-operations into one Detail. Errors are reported
	// jointly so the user sees whatever information is available even when
	// one sub-operation failed.
	switch {
	case r.PriceErr != nil && r.DividendErr != nil:
		out.Err = fmt.Errorf("prices: %v; dividends: %v", r.PriceErr, r.DividendErr)
	case r.PriceErr != nil:
		out.Err = fmt.Errorf("prices: %w", r.PriceErr)
	case r.DividendErr != nil:
		out.Err = fmt.Errorf("dividends: %w", r.DividendErr)
	}
	divs := fmt.Sprintf("+%d dividends", r.NewDividends)
	if r.DividendSkip {
		divs = "dividends=cached"
	}
	out.Detail = fmt.Sprintf("+%d prices, %s", r.NewPrices, divs)
	return out
}

// --- smart-lab adapter ---------------------------------------------------

type smartlabRefresher struct{ u *smartlab.Updater }

func (s *smartlabRefresher) Name() string                       { return "smart-lab" }
func (s *smartlabRefresher) AppliesTo(class assets.Class) bool  { return class == assets.ClassStock }
func (s *smartlabRefresher) RefreshTicker(ctx context.Context, ticker string, force bool) refreshResult {
	r := s.u.UpdateTicker(ctx, ticker, force)
	out := refreshResult{
		Source:  "smart-lab",
		Ticker:  ticker,
		Skipped: r.Skipped,
		Err:     r.Err,
	}
	switch {
	case r.Skipped:
		out.Detail = "cached"
	case r.Err != nil:
		out.Detail = "ERR"
	default:
		out.Detail = fmt.Sprintf("+%d announcements, -%d removed", r.NewAnnouncements, r.Removed)
	}
	return out
}

// printRefreshResult writes one progress line to ac.out.
func printRefreshResult(ac *appContext, r refreshResult) {
	if r.Err != nil {
		_, _ = fmt.Fprintf(ac.out, "  %-10s %s: ERR(%v)\n", r.Source, r.Ticker, r.Err)
		return
	}
	_, _ = fmt.Fprintf(ac.out, "  %-10s %s: %s\n", r.Source, r.Ticker, r.Detail)
}

// buildRefreshers returns the ordered list of data-source adapters. MOEX runs
// first because it populates fetch_state.asset_class — smart-lab uses that to
// decide whether the ticker is a stock it should cover.
func buildRefreshers(st *store.Store) []tickerRefresher {
	return []tickerRefresher{
		&moexRefresher{u: moex.NewUpdater(moex.NewClient(), st)},
		&smartlabRefresher{u: smartlab.NewUpdater(smartlab.NewClient(), st)},
	}
}

// assetClassForRouting returns the class we should use to decide whether a
// refresher applies — preferring the freshly-saved class from fetch_state,
// falling back to the offline heuristic. Called after each MOEX refresh.
func assetClassForRouting(st *store.Store, ticker string) assets.Class {
	if fs, err := st.GetFetchState(ticker); err == nil && fs.AssetClass != "" {
		return assets.Class(fs.AssetClass)
	}
	return assets.Classify(ticker)
}
