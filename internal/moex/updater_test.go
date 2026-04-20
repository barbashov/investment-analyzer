package moex

import (
	"context"
	"net/http"
	"testing"
	"time"

	"investment-analyzer/internal/assets"
	"investment-analyzer/internal/store"
)

// TestUpdateTickerCachesClassifyResult verifies that UpdateTicker, on a
// first-ever fetch, calls the MOEX classifier and persists the result into
// fetch_state so the class question is never asked twice. Without this,
// assets.Classify's ClassStock default is the only answer ever seen —
// misclassifying non-FinEx ETFs, novel bonds, etc.
func TestUpdateTickerCachesClassifyResult(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Dead-URL client so FetchPrices/FetchDividends error out fast; we only
	// care that the class-resolution branch runs and saves state.
	client := &Client{
		HTTP:    &http.Client{Timeout: 100 * time.Millisecond},
		BaseURL: "http://127.0.0.1:1/iss",
		limiter: NewClient().limiter,
	}

	var classifyCalls int
	u := &Updater{
		Client: client,
		Store:  st,
		Now:    func() time.Time { return time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC) },
		Classify: func(ctx context.Context, ticker string) (assets.Class, error) {
			classifyCalls++
			return assets.ClassBond, nil
		},
	}

	_ = u.UpdateTicker(context.Background(), "NOVELBOND", false)

	if classifyCalls != 1 {
		t.Errorf("Classify was called %d times, want 1", classifyCalls)
	}

	state, err := st.GetFetchState("NOVELBOND")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.AssetClass != string(assets.ClassBond) {
		t.Errorf("cached AssetClass = %q, want %q (MOEX classifier result should be persisted)",
			state.AssetClass, assets.ClassBond)
	}

	// Second call must not re-ask the classifier — the cached class wins.
	_ = u.UpdateTicker(context.Background(), "NOVELBOND", false)
	if classifyCalls != 1 {
		t.Errorf("Classify called again (%d total) — cached class should short-circuit", classifyCalls)
	}
}

// TestUpdateTickerFallsBackToHeuristic verifies that when the MOEX classifier
// errors out (network down, unknown secid), the offline heuristic in
// assets.Classify is used instead.
func TestUpdateTickerFallsBackToHeuristic(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer func() { _ = st.Close() }()

	client := &Client{
		HTTP:    &http.Client{Timeout: 100 * time.Millisecond},
		BaseURL: "http://127.0.0.1:1/iss",
		limiter: NewClient().limiter,
	}

	u := &Updater{
		Client: client,
		Store:  st,
		Now:    func() time.Time { return time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC) },
		Classify: func(ctx context.Context, ticker string) (assets.Class, error) {
			return assets.ClassUnknown, context.DeadlineExceeded
		},
	}

	_ = u.UpdateTicker(context.Background(), "SBER", false)

	state, err := st.GetFetchState("SBER")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	// assets.Classify("SBER") returns ClassStock — that's what should be cached.
	if state.AssetClass != string(assets.ClassStock) {
		t.Errorf("cached AssetClass = %q, want %q (should fall back to heuristic)",
			state.AssetClass, assets.ClassStock)
	}
}
