package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type OpType string

const (
	OpBuy         OpType = "BUY"
	OpSell        OpType = "SELL"
	OpDividend    OpType = "DIVIDEND"
	OpDeposit     OpType = "DEPOSIT"
	OpTransfer    OpType = "TRANSFER" // cash transfer between accounts
	OpSecurityIn  OpType = "SECURITY_IN"  // custody transfer IN (affects holdings, not cash)
	OpSecurityOut OpType = "SECURITY_OUT" // custody transfer OUT (affects holdings, not cash)
	OpCommission  OpType = "COMMISSION"
	OpFXBuy       OpType = "FX_BUY"
	OpFXSell      OpType = "FX_SELL"
	OpWithdrawal  OpType = "WITHDRAWAL" // cash out
	OpIncome      OpType = "INCOME"     // non-dividend income (margin lending, etc.)
	OpTax         OpType = "TAX"        // НДФЛ withholdings (separate from div_tax embedded in dividend rows)
)

// Source identifies how a transaction entered the DB.
type Source string

const (
	SourceCSV    Source = "csv"
	SourceManual Source = "manual"
)

// Transaction is the canonical in-memory representation of a single broker operation.
//
// `Amount` is always >= 0, mirroring Finam's `Объем транзакции`. Cash-flow direction is
// derived from `OpType` at report time.
type Transaction struct {
	ID         int64
	TradeHash  string // computed from the other fields; populated by ComputeHash if empty.
	Source     Source
	SourceRef  string // e.g. "finam-operations-final.csv:42"
	Date       string // YYYY-MM-DD
	Time       string // HH:MM:SS, "" if unknown (still hashed as "")
	OpType     OpType
	OpLabelRU  string
	AssetName  string
	Ticker     string
	AssetClass string // resolved by the assets package; may be empty at insert time
	Account    string
	Amount     float64
	Currency   string
	Quantity   *float64
	UnitPrice  *float64
	Comment    string
	DivTax     *float64
	DivPeriod  string
	CreatedAt  time.Time
}

// ComputeHash returns the canonical trade hash for the transaction.
// Same trade entered via CSV import or `invest tx add` MUST produce the same hash.
func ComputeHash(tx *Transaction) string {
	parts := []string{
		tx.Date,
		tx.Time, // empty string if unknown — intentional
		string(tx.OpType),
		tx.Ticker,
		tx.Account,
		fmtDecimal(tx.Amount),
		tx.Currency,
		fmtDecimalPtr(tx.Quantity),
		fmtDecimalPtr(tx.UnitPrice),
		tx.Comment,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

func fmtDecimal(v float64) string  { return fmt.Sprintf("%.6f", v) }
func fmtDecimalPtr(v *float64) string {
	if v == nil {
		return ""
	}
	return fmtDecimal(*v)
}

// InsertResult tells the caller what happened: a new row was added, or it was a duplicate.
type InsertResult int

const (
	InsertedNew InsertResult = iota
	InsertedDuplicate
)

// InsertTransaction inserts a transaction, deduplicating on TradeHash.
// Returns InsertedDuplicate (no error) when the hash already exists.
func (s *Store) InsertTransaction(tx *Transaction) (InsertResult, error) {
	if tx.TradeHash == "" {
		tx.TradeHash = ComputeHash(tx)
	}
	if tx.CreatedAt.IsZero() {
		tx.CreatedAt = time.Now().UTC()
	}

	res, err := s.DB.Exec(`
		INSERT INTO transactions
			(trade_hash, source, source_ref, date, time, op_type, op_label_ru,
			 asset_name, ticker, asset_class, account, amount, currency,
			 quantity, unit_price, comment, div_tax, div_period, created_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(trade_hash) DO NOTHING
	`,
		tx.TradeHash, string(tx.Source), nullStr(tx.SourceRef),
		tx.Date, nullStr(tx.Time), string(tx.OpType), tx.OpLabelRU,
		nullStr(tx.AssetName), nullStr(tx.Ticker), nullStr(tx.AssetClass),
		tx.Account, tx.Amount, tx.Currency,
		nullFloat(tx.Quantity), nullFloat(tx.UnitPrice),
		nullStr(tx.Comment), nullFloat(tx.DivTax), nullStr(tx.DivPeriod),
		tx.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("insert transaction: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return InsertedDuplicate, nil
	}
	id, err := res.LastInsertId()
	if err == nil {
		tx.ID = id
	}
	return InsertedNew, nil
}

// CountTransactions returns the total number of stored transactions. Mainly for tests/sanity.
func (s *Store) CountTransactions() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ListTransactions returns all stored transactions, ordered by (date, time).
// `from` and `to` are inclusive YYYY-MM-DD bounds; pass "" for unbounded.
func (s *Store) ListTransactions(from, to string) ([]*Transaction, error) {
	q := `SELECT id, trade_hash, source, source_ref, date, time, op_type, op_label_ru,
	             asset_name, ticker, asset_class, account, amount, currency,
	             quantity, unit_price, comment, div_tax, div_period, created_at
	      FROM transactions WHERE 1=1`
	var args []any
	if from != "" {
		q += " AND date >= ?"
		args = append(args, from)
	}
	if to != "" {
		q += " AND date <= ?"
		args = append(args, to)
	}
	q += " ORDER BY date ASC, COALESCE(time, '') ASC, id ASC"

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*Transaction
	for rows.Next() {
		t, err := scanTx(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTransactionByHash returns a transaction by its hash (or sql.ErrNoRows).
func (s *Store) GetTransactionByHash(hash string) (*Transaction, error) {
	row := s.DB.QueryRow(`
		SELECT id, trade_hash, source, source_ref, date, time, op_type, op_label_ru,
		       asset_name, ticker, asset_class, account, amount, currency,
		       quantity, unit_price, comment, div_tax, div_period, created_at
		FROM transactions WHERE trade_hash = ?`, hash)
	return scanTx(row.Scan)
}

func scanTx(scan func(dest ...any) error) (*Transaction, error) {
	var (
		t          Transaction
		srcRef     sql.NullString
		timeS      sql.NullString
		assetName  sql.NullString
		ticker     sql.NullString
		assetClass sql.NullString
		quantity   sql.NullFloat64
		unitPrice  sql.NullFloat64
		comment    sql.NullString
		divTax     sql.NullFloat64
		divPeriod  sql.NullString
		createdAt  string
		opType     string
		source     string
	)
	if err := scan(&t.ID, &t.TradeHash, &source, &srcRef, &t.Date, &timeS, &opType, &t.OpLabelRU,
		&assetName, &ticker, &assetClass, &t.Account, &t.Amount, &t.Currency,
		&quantity, &unitPrice, &comment, &divTax, &divPeriod, &createdAt); err != nil {
		return nil, err
	}
	t.Source = Source(source)
	t.SourceRef = srcRef.String
	t.Time = timeS.String
	t.OpType = OpType(opType)
	t.AssetName = assetName.String
	t.Ticker = ticker.String
	t.AssetClass = assetClass.String
	if quantity.Valid {
		v := quantity.Float64
		t.Quantity = &v
	}
	if unitPrice.Valid {
		v := unitPrice.Float64
		t.UnitPrice = &v
	}
	t.Comment = comment.String
	if divTax.Valid {
		v := divTax.Float64
		t.DivTax = &v
	}
	t.DivPeriod = divPeriod.String
	if ts, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		t.CreatedAt = ts
	} else if ts, err := time.Parse(time.RFC3339, createdAt); err == nil {
		t.CreatedAt = ts
	}
	return &t, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// ErrNotFound is returned when a single-row lookup finds nothing.
var ErrNotFound = errors.New("not found")
