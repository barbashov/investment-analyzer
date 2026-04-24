package smartlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/time/rate"
)

func TestParseHTML_LKOHFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "lkoh.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	got := ParseHTML("LKOH", string(body))
	if len(got) < 4 {
		t.Fatalf("want >=4 rows, got %d", len(got))
	}

	// First row is the upcoming 4кв 2025 dividend.
	first := got[0]
	if first.Period != "4кв 2025" {
		t.Errorf("first row Period = %q, want %q", first.Period, "4кв 2025")
	}
	if first.T2Date != "2026-05-01" {
		t.Errorf("first row T2Date = %q, want 2026-05-01", first.T2Date)
	}
	if first.ExDate != "2026-05-04" {
		t.Errorf("first row ExDate = %q, want 2026-05-04", first.ExDate)
	}
	if first.ValuePerShare != 278 {
		t.Errorf("first row value = %v, want 278", first.ValuePerShare)
	}

	// Second row (first historical): 3кв 2025, 397 RUB.
	if got[1].ValuePerShare != 397 {
		t.Errorf("second row value = %v, want 397", got[1].ValuePerShare)
	}
}

func TestParseHTML_UnknownTicker(t *testing.T) {
	body, _ := os.ReadFile(filepath.Join("testdata", "lkoh.html"))
	got := ParseHTML("ZZZZ", string(body))
	if got != nil {
		t.Errorf("want nil for unknown ticker, got %d rows", len(got))
	}
}

func TestParseHTML_SkipsCancelled(t *testing.T) {
	html := `<table><tr>
		<td>FOO</td>
		<td>01.05.2026</td>
		<td>04.05.2026</td>
		<td>4кв 2025</td>
		<td class="dividend_canceled">0</td>
		<td>100</td>
		<td>0%</td>
	</tr></table>`
	got := ParseHTML("FOO", html)
	if len(got) != 0 {
		t.Errorf("cancelled row should be skipped, got %d rows", len(got))
	}
}

func TestFetchAnnouncements_PreservesDataOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	}))
	defer srv.Close()

	c := &Client{
		HTTP:    srv.Client(),
		BaseURL: srv.URL,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	got, err := c.FetchAnnouncements(context.Background(), "LKOH")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("want ErrUnavailable on 5xx, got %v", err)
	}
	if got != nil {
		t.Errorf("want nil rows on error, got %d", len(got))
	}
}

func TestFetchAnnouncements_404IsNotErrUnavailable(t *testing.T) {
	// Delisted ticker → HTTP 404 → distinct error so the updater can treat
	// it differently from a transient 5xx (no point retrying, but also no
	// grounds to wipe cached data either).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	c := &Client{
		HTTP:    srv.Client(),
		BaseURL: srv.URL,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	_, err := c.FetchAnnouncements(context.Background(), "DEAD")
	if err == nil {
		t.Fatal("want error on 404")
	}
	if errors.Is(err, ErrUnavailable) {
		t.Errorf("404 should not be ErrUnavailable, got %v", err)
	}
}

func TestFetchAnnouncements_UnrecognizedPage(t *testing.T) {
	// A 200 response with arbitrary HTML (layout change / CAPTCHA / wrong page)
	// must not be treated as "ticker has no dividends" — that would let the
	// updater wipe cached projections. Return ErrUnavailable so the cache
	// stays intact.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><h1>Nothing to see here</h1></body></html>`))
	}))
	defer srv.Close()

	c := &Client{
		HTTP:    srv.Client(),
		BaseURL: srv.URL,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	got, err := c.FetchAnnouncements(context.Background(), "LKOH")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("want ErrUnavailable for unrecognized page, got %v", err)
	}
	if got != nil {
		t.Errorf("want nil rows on unrecognized page, got %d", len(got))
	}
}

func TestFetchAnnouncements_RecognizedPageEmptyTicker(t *testing.T) {
	// Serve the LKOH fixture (carries dividend-page markers) but ask for a
	// ticker that doesn't appear in the table. This is the legitimate
	// "no dividends known for this ticker" case — nil rows, no error.
	body, _ := os.ReadFile(filepath.Join("testdata", "lkoh.html"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := &Client{
		HTTP:    srv.Client(),
		BaseURL: srv.URL,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	got, err := c.FetchAnnouncements(context.Background(), "ZZZZ")
	if err != nil {
		t.Fatalf("want nil error for recognized page with no matching rows, got %v", err)
	}
	if got != nil {
		t.Errorf("want nil rows when ticker absent, got %d", len(got))
	}
}

func TestFetchAnnouncements_OKParses(t *testing.T) {
	body, _ := os.ReadFile(filepath.Join("testdata", "lkoh.html"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/LKOH/dividend/" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := &Client{
		HTTP:    srv.Client(),
		BaseURL: srv.URL,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	got, err := c.FetchAnnouncements(context.Background(), "LKOH")
	if err != nil {
		t.Fatalf("FetchAnnouncements: %v", err)
	}
	if len(got) < 4 {
		t.Errorf("want >=4 rows, got %d", len(got))
	}
}

func TestNormalizeTicker(t *testing.T) {
	if NormalizeTicker("SBERP") != "SBER" {
		t.Errorf("SBERP should fold to SBER, got %q", NormalizeTicker("SBERP"))
	}
	if NormalizeTicker("SBER") != "SBER" {
		t.Errorf("SBER should pass through, got %q", NormalizeTicker("SBER"))
	}
	if NormalizeTicker("LKOH") != "LKOH" {
		t.Errorf("LKOH should pass through, got %q", NormalizeTicker("LKOH"))
	}
	if NormalizeTicker("AGRO") != "RAGR" {
		t.Errorf("AGRO should alias to RAGR, got %q", NormalizeTicker("AGRO"))
	}
}

func TestParseHTML_AliasTicker(t *testing.T) {
	// Smart-lab's /q/RAGR/ page tags rows with <td>AGRO</td> during the
	// transition; after the rename, upcoming rows may switch to <td>RAGR</td>.
	// ParseHTML must pick up rows under either symbol when the caller asks
	// about AGRO (or vice-versa in the future).
	html := `<table class="simple-little-table financials dividends">
		<tr><th>Тикер</th><th>дата T-1</th><th>дата отсечки</th><th>Период</th><th>дивиденд</th><th>Цена акции</th><th>Див.доходность</th></tr>
		<tr><td>RAGR</td><td>01.06.2026</td><td>02.06.2026</td><td>2025</td><td><strong>50</strong>₽</td><td>200</td><td>25%</td></tr>
		<tr><td>AGRO</td><td>08.09.2021</td><td>10.09.2021</td><td>2кв 2021</td><td><strong>65,5</strong>₽</td><td>1205,2</td><td>5,4%</td></tr>
	</table>`
	got := ParseHTML("AGRO", html)
	if len(got) != 2 {
		t.Fatalf("want 2 rows (one under each tag), got %d", len(got))
	}
	// Reported ticker is always the caller's input, not the alias tag.
	for _, a := range got {
		if a.Ticker != "AGRO" {
			t.Errorf("row ticker = %q, want AGRO", a.Ticker)
		}
	}
}
