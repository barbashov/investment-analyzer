// Package moex is a thin client for the MOEX ISS public API (https://iss.moex.com/iss/).
//
// We rely on a tiny subset:
//   - GET /iss/securities/{secid}.json — security metadata (ISIN, board, market)
//   - GET /iss/securities/{secid}/dividends.json — announced dividends
//   - GET /iss/history/engines/{eng}/markets/{mkt}/securities/{secid}.json — daily history
//
// All endpoints are public and return JSON in the "metadata/columns/data" envelope.
package moex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/time/rate"
)

const baseURL = "https://iss.moex.com/iss"

// Client is a polite, rate-limited MOEX ISS client.
type Client struct {
	HTTP    *http.Client
	limiter *rate.Limiter
	BaseURL string
}

// NewClient returns a Client with sensible defaults: 10 s timeout, 5 req/s rate limit.
func NewClient() *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 10 * time.Second},
		limiter: rate.NewLimiter(rate.Limit(5), 1),
		BaseURL: baseURL,
	}
}

func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}
	u := c.BaseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "investment-analyzer/0.1 (+local)")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("MOEX returned %d for %s: %s", resp.StatusCode, u, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Block is the standard MOEX ISS payload section ("metadata/columns/data").
type Block struct {
	Columns []string  `json:"columns"`
	Data    [][]any   `json:"data"`
	Meta    any       `json:"metadata,omitempty"`
}

// columnIndex returns the index of the named column or -1 if absent.
func (b Block) columnIndex(name string) int {
	for i, c := range b.Columns {
		if c == name {
			return i
		}
	}
	return -1
}

// stringAt safely extracts a string from row[idx]; returns "" for nil/missing.
func stringAt(row []any, idx int) string {
	if idx < 0 || idx >= len(row) || row[idx] == nil {
		return ""
	}
	if s, ok := row[idx].(string); ok {
		return s
	}
	return fmt.Sprintf("%v", row[idx])
}

// floatAt safely extracts a float from row[idx]; returns (0, false) when missing/null.
func floatAt(row []any, idx int) (float64, bool) {
	if idx < 0 || idx >= len(row) || row[idx] == nil {
		return 0, false
	}
	switch v := row[idx].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}
