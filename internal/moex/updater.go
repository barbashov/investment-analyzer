package moex

import (
	"context"
	"errors"
	"fmt"
	"time"

	"investment-analyzer/internal/assets"
	"investment-analyzer/internal/store"
)

// DividendStaleAfter controls how often we re-poll MOEX for the dividend list of a given ticker.
// Dividends already returned are immutable; the only reason to re-poll is to discover newly
// announced ones.
const DividendStaleAfter = 7 * 24 * time.Hour

// UpdateResult summarizes one ticker refresh.
type UpdateResult struct {
	Ticker        string
	AssetClass    assets.Class
	NewPrices     int
	NewDividends  int
	PriceErr      error
	DividendErr   error
	DividendSkip  bool // true when we used the cached list (within staleness window)
}

// Updater orchestrates "bring MOEX cache up to date for ticker T" with the incremental rules.
type Updater struct {
	Client *Client
	Store  *store.Store
	Now    func() time.Time // overridable for tests; defaults to time.Now
	// Classify resolves a ticker's asset class via MOEX metadata on first fetch.
	// Overridable for tests; defaults to Client.ClassifyViaMOEX.
	Classify func(ctx context.Context, ticker string) (assets.Class, error)
}

func NewUpdater(c *Client, st *store.Store) *Updater {
	return &Updater{
		Client:   c,
		Store:    st,
		Now:      time.Now,
		Classify: c.ClassifyViaMOEX,
	}
}

// UpdateTicker brings prices + dividends up to date for one ticker.
// `force` ignores the dividend staleness window (re-pulls the list regardless).
func (u *Updater) UpdateTicker(ctx context.Context, ticker string, force bool) UpdateResult {
	res := UpdateResult{Ticker: ticker}
	state, err := u.Store.GetFetchState(ticker)
	if err != nil {
		res.PriceErr = err
		return res
	}

	class := assets.Class(state.AssetClass)
	if class == assets.ClassUnknown {
		// First-ever fetch for this ticker: consult MOEX metadata so non-FinEx
		// ETFs, unrecognized bonds, and novel instruments are routed correctly.
		// On any error (network, unknown secid), fall back to the offline heuristic.
		if u.Classify != nil {
			if cls, err := u.Classify(ctx, ticker); err == nil && cls != assets.ClassUnknown {
				class = cls
			}
		}
		if class == assets.ClassUnknown {
			class = assets.Classify(ticker)
		}
	}
	res.AssetClass = class

	// 1. Prices (incremental)
	from := nextDateAfter(state.LastPriceDate)
	if from == "" {
		from = "2010-01-01" // hard floor; MOEX data usually starts later, no harm in asking early.
	}
	till := u.Now().UTC().Format("2006-01-02")
	if from <= till {
		candles, err := u.Client.FetchPrices(ctx, ticker, class, from, till)
		if err != nil {
			res.PriceErr = err
		} else {
			rows := make([]store.MOEXPrice, 0, len(candles))
			for _, c := range candles {
				rows = append(rows, store.MOEXPrice{Ticker: ticker, Date: c.Date, Close: c.Close})
			}
			added, err := u.Store.UpsertMOEXPrices(rows, class == assets.ClassCurrency)
			if err != nil {
				res.PriceErr = err
			}
			res.NewPrices = added
			if len(candles) > 0 {
				maxDate := state.LastPriceDate
				for _, c := range candles {
					if c.Date > maxDate {
						maxDate = c.Date
					}
				}
				state.LastPriceDate = maxDate
			}
		}
	}

	// 2. Dividends (only for share-like assets; bonds/currency don't pay dividends)
	if class == assets.ClassStock || class == assets.ClassETF {
		stale := force ||
			state.LastDividendCheckAt.IsZero() ||
			u.Now().Sub(state.LastDividendCheckAt) > DividendStaleAfter
		if !stale {
			res.DividendSkip = true
		} else {
			divs, err := u.Client.FetchDividends(ctx, ticker)
			if err != nil {
				res.DividendErr = err
			} else {
				rows := make([]store.MOEXDividend, 0, len(divs))
				for _, d := range divs {
					if d.RegistryDate == "" {
						continue
					}
					rows = append(rows, store.MOEXDividend{
						Ticker:       ticker,
						RegistryDate: d.RegistryDate,
						Value:        d.Value,
						Currency:     d.Currency,
					})
				}
				added, err := u.Store.UpsertMOEXDividends(rows)
				if err != nil {
					res.DividendErr = err
				}
				res.NewDividends = added
				state.LastDividendCheckAt = u.Now()
			}
		}
	}

	state.AssetClass = string(class)
	if err := u.Store.SaveFetchState(state); err != nil {
		// Don't overwrite a more specific error.
		if res.PriceErr == nil && res.DividendErr == nil {
			res.PriceErr = err
		}
	}
	return res
}

// CurrentHoldingsTickers returns the set of tickers with strictly positive net quantity from
// BUY-SELL across the entire transaction history, in stable insertion order.
func CurrentHoldingsTickers(s *store.Store) ([]string, error) {
	rows, err := s.DB.Query(`
		SELECT ticker,
		       COALESCE(SUM(CASE WHEN op_type IN ('BUY','FX_BUY','SECURITY_IN')   THEN quantity ELSE 0 END), 0)
		     - COALESCE(SUM(CASE WHEN op_type IN ('SELL','FX_SELL','SECURITY_OUT') THEN quantity ELSE 0 END), 0) AS net
		FROM transactions
		WHERE ticker IS NOT NULL AND ticker != '' AND op_type IN ('BUY','SELL','FX_BUY','FX_SELL','SECURITY_IN','SECURITY_OUT')
		GROUP BY ticker
		HAVING net > 0
		ORDER BY ticker ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tickers []string
	for rows.Next() {
		var t string
		var n float64
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		tickers = append(tickers, t)
	}
	return tickers, rows.Err()
}

// nextDateAfter returns the next ISO date after `date`. Empty input → empty output.
func nextDateAfter(date string) string {
	if date == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ""
	}
	return t.AddDate(0, 0, 1).Format("2006-01-02")
}

// ErrNoData signals that MOEX returned an empty payload for a known ticker.
var ErrNoData = errors.New("moex: empty result")

// SecurityClassifier wraps Client.FetchSecurity to map an unknown ticker → Class via MOEX metadata.
// Used as a fallback when the static heuristic in assets.Classify returns ClassUnknown.
func (c *Client) ClassifyViaMOEX(ctx context.Context, secid string) (assets.Class, error) {
	info, err := c.FetchSecurity(ctx, secid)
	if err != nil {
		return assets.ClassUnknown, err
	}
	for _, m := range info.Markets {
		switch m {
		case "selt":
			return assets.ClassCurrency, nil
		case "bonds":
			return assets.ClassBond, nil
		}
	}
	if info.Type == "preferred_share" || info.Type == "common_share" {
		return assets.ClassStock, nil
	}
	if info.Type == "ofz_bond" || info.Type == "corporate_bond" {
		return assets.ClassBond, nil
	}
	if info.Type == "currency" {
		return assets.ClassCurrency, nil
	}
	return assets.ClassStock, fmt.Errorf("unrecognized SECTYPE %q for %s", info.Type, secid)
}
