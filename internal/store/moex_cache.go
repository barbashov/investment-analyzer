package store

import (
	"database/sql"
	"errors"
	"time"
)

// MOEXDividend mirrors the moex_dividends row.
type MOEXDividend struct {
	Ticker       string
	RegistryDate string
	Value        float64
	Currency     string
}

// MOEXPrice mirrors the moex_prices row (or moex_fx for currency).
type MOEXPrice struct {
	Ticker string
	Date   string
	Close  float64
}

// FetchState records what we've already fetched for a ticker, so update is incremental.
type FetchState struct {
	Ticker               string
	AssetClass           string
	LastPriceDate        string // "" if never fetched
	LastDividendCheckAt  time.Time
}

// UpsertMOEXDividends inserts new MOEX dividend rows; existing PK collisions are silently ignored
// (immutable historical data — never overwritten).
func (s *Store) UpsertMOEXDividends(divs []MOEXDividend) (added int, err error) {
	if len(divs) == 0 {
		return 0, nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO moex_dividends(ticker, registry_date, value, currency) VALUES(?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stmt.Close() }()
	for _, d := range divs {
		r, err := stmt.Exec(d.Ticker, d.RegistryDate, d.Value, d.Currency)
		if err != nil {
			return 0, err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			added++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return added, nil
}

// UpsertMOEXPrices inserts daily closes (table chosen by class — currency goes into moex_fx).
func (s *Store) UpsertMOEXPrices(prices []MOEXPrice, currencyMarket bool) (added int, err error) {
	if len(prices) == 0 {
		return 0, nil
	}
	table := "moex_prices"
	col1 := "ticker"
	col3 := "close"
	if currencyMarket {
		table = "moex_fx"
		col1 = "pair"
		col3 = "rate"
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO ` + table + `(` + col1 + `, date, ` + col3 + `) VALUES(?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stmt.Close() }()
	for _, p := range prices {
		r, err := stmt.Exec(p.Ticker, p.Date, p.Close)
		if err != nil {
			return 0, err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			added++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return added, nil
}

// GetFetchState returns the saved state or a zero-value struct (with Ticker set) when missing.
func (s *Store) GetFetchState(ticker string) (FetchState, error) {
	var (
		fs       FetchState
		assetCls string
		lastDate sql.NullString
		lastChk  sql.NullString
	)
	err := s.DB.QueryRow(`SELECT ticker, asset_class, last_price_date, last_dividend_check_at
	                      FROM fetch_state WHERE ticker = ?`, ticker).
		Scan(&fs.Ticker, &assetCls, &lastDate, &lastChk)
	if errors.Is(err, sql.ErrNoRows) {
		return FetchState{Ticker: ticker}, nil
	}
	if err != nil {
		return fs, err
	}
	fs.AssetClass = assetCls
	fs.LastPriceDate = lastDate.String
	if lastChk.Valid {
		if t, err := time.Parse(time.RFC3339, lastChk.String); err == nil {
			fs.LastDividendCheckAt = t
		}
	}
	return fs, nil
}

// ResetFetchState deletes the fetch_state row for a ticker so the next update
// re-pulls history from scratch. SaveFetchState uses COALESCE to preserve old
// values on null inputs, so a plain upsert with an empty struct is a no-op —
// callers that want a true reset must go through this method.
func (s *Store) ResetFetchState(ticker string) error {
	_, err := s.DB.Exec(`DELETE FROM fetch_state WHERE ticker = ?`, ticker)
	return err
}

// SaveFetchState upserts the state row for a ticker.
func (s *Store) SaveFetchState(fs FetchState) error {
	var lastChk any
	if !fs.LastDividendCheckAt.IsZero() {
		lastChk = fs.LastDividendCheckAt.Format(time.RFC3339)
	}
	_, err := s.DB.Exec(`
		INSERT INTO fetch_state(ticker, asset_class, last_price_date, last_dividend_check_at)
		VALUES(?,?,?,?)
		ON CONFLICT(ticker) DO UPDATE SET
			asset_class = excluded.asset_class,
			last_price_date = COALESCE(excluded.last_price_date, last_price_date),
			last_dividend_check_at = COALESCE(excluded.last_dividend_check_at, last_dividend_check_at)
	`, fs.Ticker, fs.AssetClass, nullStr(fs.LastPriceDate), lastChk)
	return err
}

// LastPriceFor returns the most recent (date, close) we have for ticker — pulls from
// moex_prices for stocks/bonds/etfs and moex_fx for currency. Returns ErrNotFound if none.
func (s *Store) LastPriceFor(ticker string, currencyMarket bool) (MOEXPrice, error) {
	table := "moex_prices"
	col1 := "ticker"
	col3 := "close"
	if currencyMarket {
		table = "moex_fx"
		col1 = "pair"
		col3 = "rate"
	}
	var p MOEXPrice
	err := s.DB.QueryRow(`SELECT `+col1+`, date, `+col3+` FROM `+table+`
	                      WHERE `+col1+` = ? ORDER BY date DESC LIMIT 1`, ticker).
		Scan(&p.Ticker, &p.Date, &p.Close)
	if errors.Is(err, sql.ErrNoRows) {
		return MOEXPrice{}, ErrNotFound
	}
	return p, err
}

// PriceAsOf returns the most recent close on or before `date` for the ticker.
// Used for historical mark-to-market (e.g. equity at year-end). Returns ErrNotFound
// if no price exists at or before that date.
func (s *Store) PriceAsOf(ticker, date string, currencyMarket bool) (MOEXPrice, error) {
	table := "moex_prices"
	col1 := "ticker"
	col3 := "close"
	if currencyMarket {
		table = "moex_fx"
		col1 = "pair"
		col3 = "rate"
	}
	var p MOEXPrice
	err := s.DB.QueryRow(`SELECT `+col1+`, date, `+col3+` FROM `+table+`
	                      WHERE `+col1+` = ? AND date <= ? ORDER BY date DESC LIMIT 1`, ticker, date).
		Scan(&p.Ticker, &p.Date, &p.Close)
	if errors.Is(err, sql.ErrNoRows) {
		return MOEXPrice{}, ErrNotFound
	}
	return p, err
}

// ListMOEXDividends returns all known MOEX dividends for a ticker, ordered by registry date.
func (s *Store) ListMOEXDividends(ticker string) ([]MOEXDividend, error) {
	rows, err := s.DB.Query(`SELECT ticker, registry_date, value, currency
	                         FROM moex_dividends WHERE ticker = ? ORDER BY registry_date ASC`, ticker)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []MOEXDividend
	for rows.Next() {
		var d MOEXDividend
		if err := rows.Scan(&d.Ticker, &d.RegistryDate, &d.Value, &d.Currency); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
