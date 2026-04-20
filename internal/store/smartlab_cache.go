package store

import (
	"time"
)

// SmartlabDividend is a board-recommended / shareholder-meeting-approved dividend
// scraped from smart-lab.ru. Unlike moex_dividends (immutable once published by MOEX),
// these rows are *projections* that can be revised, cancelled, or superseded —
// so writes go through ReplaceSmartlabDividends (full per-ticker replace), never
// INSERT OR IGNORE.
type SmartlabDividend struct {
	Ticker        string
	Period        string // e.g. "4кв 2025"
	T2Date        string // settlement date (registry proxy), YYYY-MM-DD
	ExDate        string // ex-dividend date, YYYY-MM-DD
	ValuePerShare float64
	FetchedAt     time.Time
}

// ReplaceSmartlabDividends atomically replaces all rows for one ticker.
// Callers must only invoke this after a successful scrape — on network/HTTP
// errors, leave storage untouched so a transient failure doesn't wipe local data.
// Returns (added, removed) row counts for reporting. Both values reflect the delta
// vs. the prior state.
func (s *Store) ReplaceSmartlabDividends(ticker string, rows []SmartlabDividend) (added, removed int, err error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var prior int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM smartlab_dividends WHERE ticker = ?`, ticker).Scan(&prior); err != nil {
		return 0, 0, err
	}

	if _, err := tx.Exec(`DELETE FROM smartlab_dividends WHERE ticker = ?`, ticker); err != nil {
		return 0, 0, err
	}

	stmt, err := tx.Prepare(`INSERT INTO smartlab_dividends(ticker, period, t2_date, ex_date, value_per_share, fetched_at)
	                         VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = stmt.Close() }()

	for _, r := range rows {
		fetched := r.FetchedAt
		if fetched.IsZero() {
			fetched = time.Now().UTC()
		}
		if _, err := stmt.Exec(r.Ticker, r.Period, r.T2Date, r.ExDate, r.ValuePerShare, fetched.Format(time.RFC3339)); err != nil {
			return 0, 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	inserted := len(rows)
	if inserted >= prior {
		return inserted - prior, 0, nil
	}
	return 0, prior - inserted, nil
}

// ListSmartlabDividends returns all cached smart-lab announcements for a ticker,
// ordered by ex-date ascending.
func (s *Store) ListSmartlabDividends(ticker string) ([]SmartlabDividend, error) {
	rows, err := s.DB.Query(`SELECT ticker, period, t2_date, ex_date, value_per_share, fetched_at
	                         FROM smartlab_dividends WHERE ticker = ? ORDER BY ex_date ASC`, ticker)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanSmartlabRows(rows)
}

// ListAllSmartlabDividends returns every cached announcement across all tickers,
// ordered by ex-date. Used by calendar/payouts views that scan the whole table.
func (s *Store) ListAllSmartlabDividends() ([]SmartlabDividend, error) {
	rows, err := s.DB.Query(`SELECT ticker, period, t2_date, ex_date, value_per_share, fetched_at
	                         FROM smartlab_dividends ORDER BY ex_date ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanSmartlabRows(rows)
}

func scanSmartlabRows(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]SmartlabDividend, error) {
	var out []SmartlabDividend
	for rows.Next() {
		var d SmartlabDividend
		var fetched string
		if err := rows.Scan(&d.Ticker, &d.Period, &d.T2Date, &d.ExDate, &d.ValuePerShare, &fetched); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339, fetched); err == nil {
			d.FetchedAt = t
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
