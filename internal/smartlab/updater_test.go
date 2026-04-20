package smartlab

import (
	"context"
	"errors"
	"testing"
	"time"

	"investment-analyzer/internal/store"
)

type stubFetcher struct {
	result []Announcement
	err    error
	calls  int
}

func (s *stubFetcher) FetchAnnouncements(_ context.Context, _ string) ([]Announcement, error) {
	s.calls++
	return s.result, s.err
}

func TestUpdater_ReplaceSemantics(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	f := &stubFetcher{
		result: []Announcement{
			{Ticker: "LKOH", Period: "4кв 2025", T2Date: "2026-05-01", ExDate: "2026-05-04", ValuePerShare: 278},
			{Ticker: "LKOH", Period: "3кв 2025", T2Date: "2026-01-09", ExDate: "2026-01-12", ValuePerShare: 397},
		},
	}
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	u := &Updater{Client: f, Store: st, Now: func() time.Time { return now }}

	// First pass: both announcements land.
	res := u.UpdateTicker(context.Background(), "LKOH", false)
	if res.Err != nil {
		t.Fatalf("first pass err: %v", res.Err)
	}
	if res.NewAnnouncements != 2 {
		t.Errorf("want 2 added, got %d", res.NewAnnouncements)
	}
	rows, _ := st.ListSmartlabDividends("LKOH")
	if len(rows) != 2 {
		t.Fatalf("want 2 rows stored, got %d", len(rows))
	}

	// Simulate shareholder meeting revising 4кв 2025 downward and cancelling 3кв 2025.
	// smart-lab will reflect this as a shorter table on the next scrape — we
	// expect the store to shrink accordingly.
	f.result = []Announcement{
		{Ticker: "LKOH", Period: "4кв 2025", T2Date: "2026-05-01", ExDate: "2026-05-04", ValuePerShare: 250},
	}
	res = u.UpdateTicker(context.Background(), "LKOH", true) // force past staleness window
	if res.Err != nil {
		t.Fatalf("second pass err: %v", res.Err)
	}
	rows, _ = st.ListSmartlabDividends("LKOH")
	if len(rows) != 1 {
		t.Fatalf("want 1 row after revision, got %d", len(rows))
	}
	if rows[0].ValuePerShare != 250 {
		t.Errorf("want revised value 250, got %v", rows[0].ValuePerShare)
	}
}

func TestUpdater_StalenessSkipsFetch(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	f := &stubFetcher{}
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	u := &Updater{Client: f, Store: st, Now: func() time.Time { return now }}

	// Prime fetch_state: last check 2 hours ago.
	_ = st.SaveFetchState(store.FetchState{
		Ticker:              "LKOH",
		AssetClass:          "stock",
		LastSmartlabCheckAt: now.Add(-2 * time.Hour),
	})

	res := u.UpdateTicker(context.Background(), "LKOH", false)
	if !res.Skipped {
		t.Errorf("want Skipped=true within 24h window")
	}
	if f.calls != 0 {
		t.Errorf("fetcher should not be called when cache is fresh; got %d calls", f.calls)
	}
}

func TestUpdater_TransientErrorPreservesData(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Seed one row directly.
	_, _, _ = st.ReplaceSmartlabDividends("LKOH", []store.SmartlabDividend{
		{Ticker: "LKOH", Period: "seed", T2Date: "2026-01-01", ExDate: "2026-01-02", ValuePerShare: 100},
	})

	f := &stubFetcher{err: ErrUnavailable}
	u := &Updater{Client: f, Store: st, Now: time.Now}

	res := u.UpdateTicker(context.Background(), "LKOH", true)
	if !errors.Is(res.Err, ErrUnavailable) {
		t.Errorf("want ErrUnavailable, got %v", res.Err)
	}

	rows, _ := st.ListSmartlabDividends("LKOH")
	if len(rows) != 1 {
		t.Fatalf("data must survive transient error; got %d rows", len(rows))
	}

	// And last_smartlab_check_at must NOT advance (we want to retry soon).
	fs, _ := st.GetFetchState("LKOH")
	if !fs.LastSmartlabCheckAt.IsZero() {
		t.Errorf("last_smartlab_check_at must not be updated on transient error; got %v", fs.LastSmartlabCheckAt)
	}
}
