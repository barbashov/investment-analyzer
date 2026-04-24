// Package smartlab fetches board-recommended (and historical) dividends from
// smart-lab.ru for Russian-listed stocks. Unlike MOEX ISS, smart-lab publishes
// announcements *before* the shareholder meeting approves a registry date —
// which is the whole reason this package exists as a supplement to internal/moex.
//
// The canonical source is the visible table at
//   https://smart-lab.ru/q/{ticker}/dividend/
// We scrape the HTML directly (no JSON API is offered). The parser is
// deliberately minimal: split on <td>{TICKER}</td>, take six columns per row,
// parse DD.MM.YYYY dates and a comma-decimal value. Robust enough for a
// stable page layout, simple enough to diagnose when the layout shifts.
package smartlab

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// ErrUnavailable signals a transient fetch failure (network / HTTP 5xx / parse
// failure on a plausibly-valid page). The updater treats this as "leave cached
// data alone" rather than wiping local state — projections are mutable, but
// not at the mercy of a flaky scrape.
var ErrUnavailable = errors.New("smartlab: source unavailable")

const baseURL = "https://smart-lab.ru/q"

// Announcement is one dividend row parsed from the smart-lab table.
// T2Date is the last trading day you can buy to be on the registry
// ("дата T-1" column on the page; the name "T2" comes from the legacy
// fetcher we're porting from). ExDate is the registry close / ex-dividend date.
type Announcement struct {
	Ticker        string
	Period        string // "4кв 2025", "2023 год", ...
	T2Date        string // YYYY-MM-DD
	ExDate        string // YYYY-MM-DD
	ValuePerShare float64
}

// Client fetches smart-lab dividend pages with a polite rate limit.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	limiter *rate.Limiter
}

// NewClient returns a Client with sensible defaults (10s timeout, 1 req/s).
// smart-lab is a single-origin HTML site; 1 req/s keeps us off their radar.
func NewClient() *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 10 * time.Second},
		BaseURL: baseURL,
		limiter: rate.NewLimiter(rate.Limit(1), 1),
	}
}

// FetchAnnouncements returns all dividend rows for a ticker, future + historical.
// Callers filter by date. Returns (nil, nil) when the page has no dividend rows.
// Returns (nil, ErrUnavailable) on network or HTTP 5xx — preserve cached data.
func (c *Client) FetchAnnouncements(ctx context.Context, ticker string) ([]Announcement, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	ticker = strings.ToUpper(ticker)
	url := fmt.Sprintf("%s/%s/dividend/", c.BaseURL, NormalizeTicker(ticker))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "investment-analyzer/0.1 (+local)")
	req.Header.Set("Accept", "text/html")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("smartlab: HTTP %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2 MB cap
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	html := string(body)
	if !looksLikeDividendPage(html) {
		// 200 OK but no recognizable dividend-table markers — layout change,
		// CAPTCHA, or some other non-dividend page. Treat as transient so the
		// updater preserves cached projections instead of wiping them.
		return nil, fmt.Errorf("%w: no dividend-page markers", ErrUnavailable)
	}
	return ParseHTML(ticker, html), nil
}

// looksLikeDividendPage reports whether `html` carries the markers we expect
// on every smart-lab /q/{ticker}/dividend/ page. We require two independent
// markers so one stray substring in an error page can't masquerade as valid.
func looksLikeDividendPage(html string) bool {
	return strings.Contains(html, "financials dividends") &&
		strings.Contains(html, "дата отсечки")
}

// ParseHTML extracts dividend rows for `ticker` from a smart-lab dividend
// page body. Exported so tests can exercise the parser without HTTP. For
// redomiciled tickers (see tickerAliases) rows tagged with either the original
// or the alias symbol are both accepted — smart-lab mixes the two during the
// transition. Parsed rows are always returned under the original `ticker`.
func ParseHTML(ticker, html string) []Announcement {
	ticker = strings.ToUpper(ticker)
	var out []Announcement
	seen := map[string]struct{}{}
	for _, tag := range tableTickersFor(ticker) {
		marker := fmt.Sprintf("<td>%s</td>", tag)
		parts := strings.Split(html, marker)
		if len(parts) < 2 {
			continue
		}
		for _, part := range parts[1:] {
			a, ok := parseRow(ticker, part)
			if !ok {
				continue
			}
			// Dedup across ticker variants on the same page — an individual row
			// can only carry one tag, but the split-on-marker approach would
			// otherwise double-count if the alias tag ever appeared inside a
			// row whose leading cell already matched the primary tag.
			key := a.T2Date + "|" + a.ExDate + "|" + a.Period
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, a)
		}
	}
	return out
}

// --- internals ----------------------------------------------------------

var tagRe = regexp.MustCompile(`<[^>]*>`)

// parseRow consumes one table row fragment (text after <td>TICKER</td>).
// Returns the parsed announcement or (_, false) if the row is cancelled /
// malformed / closes early (end of the table).
func parseRow(ticker, part string) (Announcement, bool) {
	cols := strings.Split(part, "</td>")
	if len(cols) < 6 {
		return Announcement{}, false
	}

	// A cancelled dividend is marked by a class attribute on the amount <td>
	// (e.g. <td class="dividend_canceled">). Check before stripping tags.
	if strings.Contains(cols[3], "dividend_canceled") {
		return Announcement{}, false
	}

	clean := make([]string, 6)
	for i := 0; i < 6; i++ {
		s := cols[i]
		s = tagRe.ReplaceAllString(s, "")
		s = strings.ReplaceAll(s, "₽", "")
		s = strings.ReplaceAll(s, "\u00a0", " ")
		s = strings.TrimSpace(s)
		clean[i] = s
	}

	buyBy, err := parseDate(clean[0])
	if err != nil {
		return Announcement{}, false
	}
	exDate, err := parseDate(clean[1])
	if err != nil {
		return Announcement{}, false
	}
	period := clean[2]
	if period == "" {
		return Announcement{}, false
	}
	value, err := parseRuFloat(clean[3])
	if err != nil {
		return Announcement{}, false
	}

	return Announcement{
		Ticker:        ticker,
		Period:        period,
		T2Date:        buyBy,
		ExDate:        exDate,
		ValuePerShare: value,
	}, true
}

// parseDate accepts "DD.MM.YYYY" and returns "YYYY-MM-DD".
func parseDate(s string) (string, error) {
	t, err := time.Parse("02.01.2006", s)
	if err != nil {
		return "", err
	}
	return t.Format("2006-01-02"), nil
}

// parseRuFloat accepts "278" or "1 234,56" (thin-space thousands, comma decimal).
func parseRuFloat(s string) (float64, error) {
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\u2009", "")
	s = strings.ReplaceAll(s, ",", ".")
	if s == "" {
		return 0, errors.New("empty number")
	}
	return strconv.ParseFloat(s, 64)
}
