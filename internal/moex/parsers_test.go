package moex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"investment-analyzer/internal/assets"
)

// fixtureServer spins up an httptest server that maps request paths to JSON
// fixture files. Each handler call consumes the next fixture registered for
// that path; this lets a single test drive paginated responses in order.
func fixtureServer(t *testing.T, routes map[string][]string) (*httptest.Server, *Client) {
	t.Helper()

	queues := map[string][]string{}
	for path, files := range routes {
		queues[path] = append(queues[path], files...)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		q, ok := queues[path]
		if !ok || len(q) == 0 {
			t.Errorf("unexpected request to %s (queue empty)", path)
			http.NotFound(w, r)
			return
		}
		fixture := q[0]
		queues[path] = q[1:]

		body, err := os.ReadFile(filepath.Join("testdata", fixture))
		if err != nil {
			t.Fatalf("read fixture %s: %v", fixture, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	// Disable rate limiting in tests — we don't want to wait between fixture requests.
	client := &Client{
		HTTP:    &http.Client{Timeout: 2 * time.Second},
		BaseURL: srv.URL,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	return srv, client
}

func TestFetchPricesShares(t *testing.T) {
	_, client := fixtureServer(t, map[string][]string{
		"/history/engines/stock/markets/shares/securities/SBER.json": {"prices_sber.json"},
	})

	candles, err := client.FetchPrices(context.Background(), "SBER", assets.ClassStock, "2024-01-01", "2024-01-10")
	if err != nil {
		t.Fatalf("FetchPrices: %v", err)
	}
	// Third row has a null CLOSE — the parser must skip it silently.
	if got, want := len(candles), 2; got != want {
		t.Fatalf("candles: got %d, want %d (null CLOSE row must be dropped)", got, want)
	}
	if candles[0] != (Candle{Date: "2024-01-03", Close: 271.5}) {
		t.Errorf("candle[0]: got %+v", candles[0])
	}
	if candles[1] != (Candle{Date: "2024-01-04", Close: 273.2}) {
		t.Errorf("candle[1]: got %+v", candles[1])
	}
}

func TestFetchPricesBondsUsesLegalClose(t *testing.T) {
	_, client := fixtureServer(t, map[string][]string{
		"/history/engines/stock/markets/bonds/securities/SU26215RMFS2.json": {"prices_bond.json"},
	})

	candles, err := client.FetchPrices(context.Background(), "SU26215RMFS2", assets.ClassBond, "2024-02-01", "2024-02-28")
	if err != nil {
		t.Fatalf("FetchPrices: %v", err)
	}
	if len(candles) != 2 {
		t.Fatalf("candles: got %d, want 2", len(candles))
	}
	// LEGALCLOSEPRICE fallback must kick in when CLOSE is absent (bonds on MOEX).
	if candles[0].Close != 98.65 || candles[1].Close != 98.72 {
		t.Errorf("expected LEGALCLOSEPRICE fallback values, got %+v", candles)
	}
}

func TestFetchPricesCurrencyEngineRouting(t *testing.T) {
	// Currency (gold) should hit engines/currency/markets/selt, not stock/shares.
	_, client := fixtureServer(t, map[string][]string{
		"/history/engines/currency/markets/selt/securities/GLDRUB_TOM.json": {"prices_sber.json"},
	})

	if _, err := client.FetchPrices(context.Background(), "GLDRUB_TOM", assets.ClassCurrency, "2024-01-01", "2024-01-10"); err != nil {
		t.Fatalf("FetchPrices routed wrong or errored: %v", err)
	}
}

func TestFetchPricesPaginationFollowsCursor(t *testing.T) {
	_, client := fixtureServer(t, map[string][]string{
		"/history/engines/stock/markets/shares/securities/GAZP.json": {
			"prices_page1.json",
			"prices_page2.json",
		},
	})

	candles, err := client.FetchPrices(context.Background(), "GAZP", assets.ClassStock, "2024-01-01", "2024-01-10")
	if err != nil {
		t.Fatalf("FetchPrices: %v", err)
	}
	if got, want := len(candles), 4; got != want {
		t.Fatalf("candles across pages: got %d, want %d", got, want)
	}
	dates := make([]string, len(candles))
	for i, c := range candles {
		dates[i] = c.Date
	}
	if strings.Join(dates, ",") != "2024-01-03,2024-01-04,2024-01-05,2024-01-08" {
		t.Errorf("pagination order wrong: %v", dates)
	}
}

func TestFetchDividends(t *testing.T) {
	_, client := fixtureServer(t, map[string][]string{
		"/securities/SBER/dividends.json": {"dividends_sber.json"},
	})

	divs, err := client.FetchDividends(context.Background(), "SBER")
	if err != nil {
		t.Fatalf("FetchDividends: %v", err)
	}
	if len(divs) != 2 {
		t.Fatalf("dividends: got %d, want 2", len(divs))
	}
	if divs[0].Ticker != "SBER" || divs[0].ISIN != "RU0009029540" {
		t.Errorf("dividend[0] metadata: %+v", divs[0])
	}
	if divs[0].RegistryDate != "2024-07-11" || divs[0].Value != 33.3 || divs[0].Currency != "RUB" {
		t.Errorf("dividend[0] values: %+v", divs[0])
	}
}

func TestFetchDividendsEmpty(t *testing.T) {
	_, client := fixtureServer(t, map[string][]string{
		"/securities/AFKS/dividends.json": {"dividends_empty.json"},
	})

	divs, err := client.FetchDividends(context.Background(), "AFKS")
	if err != nil {
		t.Fatalf("FetchDividends: %v", err)
	}
	if len(divs) != 0 {
		t.Errorf("empty payload should yield 0 dividends, got %d", len(divs))
	}
}

func TestFetchSecurityExtractsFields(t *testing.T) {
	_, client := fixtureServer(t, map[string][]string{
		"/securities/SBER.json": {"security_sber.json"},
	})

	info, err := client.FetchSecurity(context.Background(), "SBER")
	if err != nil {
		t.Fatalf("FetchSecurity: %v", err)
	}
	if info.ISIN != "RU0009029540" {
		t.Errorf("ISIN: got %q", info.ISIN)
	}
	if info.Type != "common_share" {
		t.Errorf("Type: got %q", info.Type)
	}
	if info.Group != "stock_shares" {
		t.Errorf("Group: got %q", info.Group)
	}
	if len(info.Boards) != 2 || info.Boards[0] != "TQBR" {
		t.Errorf("Boards: got %v", info.Boards)
	}
	if len(info.Markets) != 1 || info.Markets[0] != "shares" {
		t.Errorf("Markets: got %v", info.Markets)
	}
}

func TestClassifyViaMOEXTable(t *testing.T) {
	// ClassifyViaMOEX is what rescues non-FinEx ETFs / novel bonds from being
	// silently routed to the wrong market. Check that each security shape maps
	// to the right class.
	_, client := fixtureServer(t, map[string][]string{
		"/securities/SBER.json":         {"security_sber.json"},
		"/securities/GLDRUB_TOM.json":   {"security_gld.json"},
		"/securities/SU26238RMFS4.json": {"security_ofz.json"},
	})

	cases := []struct {
		secid string
		want  assets.Class
	}{
		{"SBER", assets.ClassStock},
		{"GLDRUB_TOM", assets.ClassCurrency},
		{"SU26238RMFS4", assets.ClassBond},
	}
	for _, c := range cases {
		t.Run(c.secid, func(t *testing.T) {
			got, err := client.ClassifyViaMOEX(context.Background(), c.secid)
			if err != nil {
				t.Fatalf("ClassifyViaMOEX(%s): %v", c.secid, err)
			}
			if got != c.want {
				t.Errorf("ClassifyViaMOEX(%s) = %s, want %s", c.secid, got, c.want)
			}
		})
	}
}
