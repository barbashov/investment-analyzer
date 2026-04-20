package moex

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"investment-analyzer/internal/assets"
)

// Candle is a single daily close (we ignore O/H/L/V for now since v1 only needs CLOSE).
type Candle struct {
	Date  string  // YYYY-MM-DD
	Close float64
}

// FetchPrices returns daily closes for [from, till] inclusive.
// `class` decides which engine/market the request hits (stocks vs bonds vs currency/selt).
// MOEX paginates large ranges via cursor; we follow it transparently.
func (c *Client) FetchPrices(ctx context.Context, secid string, class assets.Class, from, till string) ([]Candle, error) {
	engine := assets.MOEXEngine(class)
	market := assets.MOEXMarket(class)

	path := fmt.Sprintf("/history/engines/%s/markets/%s/securities/%s.json",
		engine, market, strings.ToUpper(secid))

	const pageLimit = 100
	start := 0
	out := []Candle{}
	for {
		params := url.Values{}
		if from != "" {
			params.Set("from", from)
		}
		if till != "" {
			params.Set("till", till)
		}
		params.Set("start", fmt.Sprintf("%d", start))
		params.Set("limit", fmt.Sprintf("%d", pageLimit))

		var resp struct {
			History Block `json:"history"`
			Cursor  Block `json:"history.cursor"`
		}
		if err := c.get(ctx, path, params, &resp); err != nil {
			return nil, err
		}

		iDate := resp.History.columnIndex("TRADEDATE")
		iClose := resp.History.columnIndex("CLOSE")
		// For currency/selt the close column is sometimes named differently; fall back.
		if iClose < 0 {
			iClose = resp.History.columnIndex("close")
		}
		if iClose < 0 {
			iClose = resp.History.columnIndex("LEGALCLOSEPRICE")
		}

		for _, row := range resp.History.Data {
			d := stringAt(row, iDate)
			cl, ok := floatAt(row, iClose)
			if !ok || d == "" {
				continue
			}
			out = append(out, Candle{Date: d, Close: cl})
		}

		// Use the cursor block (single row) to decide if we need another page.
		if len(resp.Cursor.Data) == 0 {
			break
		}
		iIdx := resp.Cursor.columnIndex("INDEX")
		iTotal := resp.Cursor.columnIndex("TOTAL")
		iSize := resp.Cursor.columnIndex("PAGESIZE")
		row := resp.Cursor.Data[0]
		idx, _ := floatAt(row, iIdx)
		total, _ := floatAt(row, iTotal)
		size, _ := floatAt(row, iSize)
		if size <= 0 {
			size = pageLimit
		}
		next := int(idx + size)
		if next >= int(total) {
			break
		}
		start = next
	}
	return out, nil
}
