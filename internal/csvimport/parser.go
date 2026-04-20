package csvimport

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"investment-analyzer/internal/store"
)

// Expected Finam CSV columns (12, 0-indexed). Order is fixed by Finam.
const (
	colDate      = 0
	colTime      = 1
	colOp        = 2
	colOpFull    = 3
	colAssetName = 4
	colTicker    = 5
	colAccount   = 6
	colAmount    = 7
	colCurrency  = 8
	colQuantity  = 9
	colPrice     = 10
	colComment   = 11
	colCount     = 12
)

// ParseError describes a single bad row that was skipped.
type ParseError struct {
	Line int
	Msg  string
	Raw  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
}

// Result describes the outcome of parsing one CSV file.
type Result struct {
	Transactions []*store.Transaction
	Errors       []*ParseError
}

// ParseFile reads a Finam-format CSV and returns parsed transactions plus per-row errors.
// File-level failures (cannot open, bad header) are returned as the second return value.
func ParseFile(path string) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ParseReader(f, filepath.Base(path))
}

// ParseReader parses a CSV from any reader. sourceRefBase is the prefix for `source_ref`
// (typically the filename); per-row source_ref becomes "{base}:{lineNumber}".
func ParseReader(r io.Reader, sourceRefBase string) (*Result, error) {
	br := newBOMTrimmer(r)

	cr := csv.NewReader(br)
	cr.Comma = ';'
	cr.FieldsPerRecord = -1 // tolerate trailing-empty-field quirks
	cr.LazyQuotes = true

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	if len(header) < colCount {
		return nil, fmt.Errorf("unexpected header (got %d cols, need %d): %q", len(header), colCount, header)
	}

	out := &Result{}
	line := 1 // header was line 1
	for {
		line++
		rec, err := cr.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			out.Errors = append(out.Errors, &ParseError{Line: line, Msg: err.Error()})
			continue
		}
		if len(rec) < colCount {
			out.Errors = append(out.Errors, &ParseError{Line: line, Msg: fmt.Sprintf("got %d cols, need %d", len(rec), colCount), Raw: strings.Join(rec, ";")})
			continue
		}
		tx, err := parseRow(rec, fmt.Sprintf("%s:%d", sourceRefBase, line))
		if err != nil {
			out.Errors = append(out.Errors, &ParseError{Line: line, Msg: err.Error(), Raw: strings.Join(rec, ";")})
			continue
		}
		out.Transactions = append(out.Transactions, tx)
	}
	return out, nil
}

func parseRow(rec []string, srcRef string) (*store.Transaction, error) {
	opLabel := strings.TrimSpace(rec[colOp])
	if opLabel == "" {
		return nil, errors.New("empty operation label")
	}
	op, ok := ClassifyOp(opLabel)
	if !ok {
		return nil, fmt.Errorf("unknown operation: %q", opLabel)
	}

	var amount float64
	rawAmount := strings.TrimSpace(rec[colAmount])
	if rawAmount != "" {
		v, err := parseDecimalComma(rawAmount)
		if err != nil {
			return nil, fmt.Errorf("amount: %w", err)
		}
		amount = v
		if amount < 0 {
			amount = -amount
		}
	} else if op != store.OpTransfer {
		return nil, fmt.Errorf("amount: empty")
	}
	// Schema stores absolute values; sign is informational only.

	currency := strings.TrimSpace(rec[colCurrency])
	if currency == "null" {
		currency = ""
	}

	tx := &store.Transaction{
		Source:    store.SourceCSV,
		SourceRef: srcRef,
		Date:      strings.TrimSpace(rec[colDate]),
		Time:      strings.TrimSpace(rec[colTime]),
		OpType:    op,
		OpLabelRU: opLabel,
		AssetName: strings.TrimSpace(rec[colAssetName]),
		Ticker:    strings.ToUpper(strings.TrimSpace(rec[colTicker])),
		Account:   strings.TrimSpace(rec[colAccount]),
		Amount:    amount,
		Currency:  currency,
		Comment:   strings.TrimSpace(rec[colComment]),
	}

	if v, ok, err := parseOptionalDecimal(rec[colQuantity]); err != nil {
		return nil, fmt.Errorf("quantity: %w", err)
	} else if ok {
		tx.Quantity = &v
	}
	if v, ok, err := parseOptionalDecimal(rec[colPrice]); err != nil {
		return nil, fmt.Errorf("price: %w", err)
	} else if ok {
		tx.UnitPrice = &v
	}

	if op == store.OpDividend {
		tx.DivTax = ParseDividendTax(tx.Comment)
		tx.DivPeriod = ParseDividendPeriod(tx.Comment)
		// Dividend rows in Finam have empty asset_name/ticker columns; pull what we can from the comment.
		if tx.AssetName == "" {
			tx.AssetName = ParseDividendAssetName(tx.Comment)
		}
	}

	return tx, nil
}

// parseDecimalComma parses "1000,00" or "+500,25" or "-105.5" into float64.
// Treats both comma and dot as the decimal separator.
func parseDecimalComma(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty")
	}
	s = strings.ReplaceAll(s, " ", "") // thin-space and friends do appear sometimes
	s = strings.ReplaceAll(s, "\u00a0", "")
	s = strings.ReplaceAll(s, ",", ".")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("non-finite number: %s", s)
	}
	return v, nil
}

func parseOptionalDecimal(s string) (float64, bool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false, nil
	}
	v, err := parseDecimalComma(s)
	if err != nil {
		return 0, false, err
	}
	return v, true, nil
}

// newBOMTrimmer wraps r so the leading UTF-8 BOM (\xef\xbb\xbf) is dropped if present.
func newBOMTrimmer(r io.Reader) io.Reader {
	const bomLen = 3
	buf := make([]byte, bomLen)
	n, _ := io.ReadFull(r, buf)
	if n == bomLen && bytes.Equal(buf, []byte{0xef, 0xbb, 0xbf}) {
		return r
	}
	return io.MultiReader(bytes.NewReader(buf[:n]), r)
}
