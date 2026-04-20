package smartlab

import (
	"context"
	"errors"
	"time"

	"investment-analyzer/internal/store"
)

// StaleAfter controls how often we re-poll smart-lab for a ticker. Board
// announcements change faster than MOEX registrations (which are immutable
// once set), so we poll daily by default.
const StaleAfter = 24 * time.Hour

// Result summarizes one ticker's smart-lab refresh.
type Result struct {
	Ticker           string
	NewAnnouncements int
	Removed          int
	Skipped          bool // true when cache is fresh within StaleAfter
	Err              error
}

// Fetcher abstracts the scraper so tests can inject a stub.
type Fetcher interface {
	FetchAnnouncements(ctx context.Context, ticker string) ([]Announcement, error)
}

// Updater brings smart-lab's cached projections up to date for one ticker.
// It writes through ReplaceSmartlabDividends (full per-ticker replace) on a
// successful fetch so revisions and cancellations take effect. On ErrUnavailable
// we leave the DB untouched — the scraper must not be a data shredder.
type Updater struct {
	Client Fetcher
	Store  *store.Store
	Now    func() time.Time
}

func NewUpdater(c Fetcher, st *store.Store) *Updater {
	return &Updater{Client: c, Store: st, Now: time.Now}
}

// UpdateTicker refreshes smart-lab announcements for `ticker`. `force` bypasses
// the staleness window.
func (u *Updater) UpdateTicker(ctx context.Context, ticker string, force bool) Result {
	res := Result{Ticker: ticker}

	state, err := u.Store.GetFetchState(ticker)
	if err != nil {
		res.Err = err
		return res
	}

	if !force && !state.LastSmartlabCheckAt.IsZero() &&
		u.Now().Sub(state.LastSmartlabCheckAt) < StaleAfter {
		res.Skipped = true
		return res
	}

	announcements, err := u.Client.FetchAnnouncements(ctx, ticker)
	if err != nil {
		res.Err = err
		if errors.Is(err, ErrUnavailable) {
			// Don't poison the fetch_state on a transient error — we want to
			// retry next time, and we definitely don't want to clobber cached
			// rows. Return early without updating last_smartlab_check_at.
			return res
		}
		// Non-transient error (e.g. HTTP 404 on a delisted ticker): still don't
		// write, but the caller will surface the error.
		return res
	}

	rows := make([]store.SmartlabDividend, 0, len(announcements))
	fetchedAt := u.Now().UTC()
	for _, a := range announcements {
		rows = append(rows, store.SmartlabDividend{
			Ticker:        a.Ticker,
			Period:        a.Period,
			T2Date:        a.T2Date,
			ExDate:        a.ExDate,
			ValuePerShare: a.ValuePerShare,
			FetchedAt:     fetchedAt,
		})
	}

	added, removed, err := u.Store.ReplaceSmartlabDividends(ticker, rows)
	if err != nil {
		res.Err = err
		return res
	}
	res.NewAnnouncements = added
	res.Removed = removed

	state.LastSmartlabCheckAt = u.Now()
	if state.AssetClass == "" {
		state.AssetClass = "stock"
	}
	if err := u.Store.SaveFetchState(state); err != nil {
		res.Err = err
	}
	return res
}
