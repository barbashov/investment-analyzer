package moex

import (
	"context"
	"strings"
)

// Dividend is one announced dividend record on MOEX.
type Dividend struct {
	Ticker        string
	ISIN          string
	RegistryDate  string // YYYY-MM-DD as MOEX returns it
	Value         float64
	Currency      string
}

// FetchDividends returns all announced dividends for a security.
// Returns an empty slice (no error) when MOEX has no entries.
func (c *Client) FetchDividends(ctx context.Context, secid string) ([]Dividend, error) {
	var resp struct {
		Dividends Block `json:"dividends"`
	}
	if err := c.get(ctx, "/securities/"+strings.ToUpper(secid)+"/dividends.json", nil, &resp); err != nil {
		return nil, err
	}
	cols := resp.Dividends
	iSec := cols.columnIndex("secid")
	iISIN := cols.columnIndex("isin")
	iDate := cols.columnIndex("registryclosedate")
	iVal := cols.columnIndex("value")
	iCur := cols.columnIndex("currencyid")

	out := make([]Dividend, 0, len(cols.Data))
	for _, row := range cols.Data {
		d := Dividend{
			Ticker:       stringAt(row, iSec),
			ISIN:         stringAt(row, iISIN),
			RegistryDate: stringAt(row, iDate),
			Currency:     stringAt(row, iCur),
		}
		if v, ok := floatAt(row, iVal); ok {
			d.Value = v
		}
		if d.Ticker == "" {
			d.Ticker = strings.ToUpper(secid)
		}
		out = append(out, d)
	}
	return out, nil
}
