package cli

import (
	"testing"

	"investment-analyzer/internal/store"
)

func ptr(v float64) *float64 { return &v }

// TestROISnapshotFullHistory: when every BUY is funded by a recorded DEPOSIT,
// CashBalance stays non-negative, PhantomInvested is 0, and the
// Invested+Earned identity matches Equity.
func TestROISnapshotFullHistory(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer func() { _ = st.Close() }()

	txs := []*store.Transaction{
		{Date: "2024-01-01", OpType: store.OpDeposit, Amount: 10000, Currency: "RUB"},
		{Date: "2024-01-02", OpType: store.OpBuy, Ticker: "SBER", Quantity: ptr(10), UnitPrice: ptr(500), Amount: 5000, Currency: "RUB"},
		{Date: "2024-06-01", OpType: store.OpSell, Ticker: "SBER", Quantity: ptr(4), UnitPrice: ptr(600), Amount: 2400, Currency: "RUB"},
	}

	snap := computeROISnapshot(st, txs, "")

	if snap.PhantomInvested != 0 {
		t.Errorf("PhantomInvested should be 0 with full history, got %v", snap.PhantomInvested)
	}
	// Deposits 10 000 − BUY 5 000 + SELL 2 400 = 7 400 cash left.
	if snap.CashBalance != 7400 {
		t.Errorf("CashBalance: want 7400, got %v", snap.CashBalance)
	}
	if snap.RealizedPnL != 400 { // (600−500)*4
		t.Errorf("RealizedPnL: want 400, got %v", snap.RealizedPnL)
	}
	// 6 shares @ 500 cost still open → BookValue 3 000. No MOEX price cached, so
	// MarketValue falls back to BookValue → UnrealizedPnL = 0.
	if snap.BookValue != 3000 {
		t.Errorf("BookValue: want 3000, got %v", snap.BookValue)
	}
	// Equity = CashBalance + MarketValue = 7400 + 3000 = 10 400.
	want := 10400.0
	if got := snap.Equity(); got != want {
		t.Errorf("Equity: want %v, got %v", want, got)
	}
	// Sanity: Invested (10 000) + Earned (400 realized, 0 unrealized) = 10 400 — matches.
	if inv, earn := snap.Invested(), snap.Earned(); inv+earn != want {
		t.Errorf("Invested+Earned: want %v, got %v (inv=%v earn=%v)", want, inv+earn, inv, earn)
	}
}

// TestROISnapshotPartialHistory: when a BUY appears before the first DEPOSIT
// (typical Finam export that starts mid-history), PhantomInvested captures the
// shortfall and Equity reports the real market-plus-cash value — not the tiny
// "Invested+Earned" residue that drove the original bug report.
func TestROISnapshotPartialHistory(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer func() { _ = st.Close() }()

	txs := []*store.Transaction{
		// Pre-history BUY — no matching DEPOSIT. This drives CashBalance to -50 000.
		{Date: "2020-03-01", OpType: store.OpBuy, Ticker: "GAZP", Quantity: ptr(500), UnitPrice: ptr(100), Amount: 50000, Currency: "RUB"},
		// Small later deposit, covered by later activity.
		{Date: "2024-01-01", OpType: store.OpDeposit, Amount: 1000, Currency: "RUB"},
	}

	snap := computeROISnapshot(st, txs, "")

	// Min cash hit -50 000 at the BUY, so PhantomInvested should cover that.
	if snap.PhantomInvested != 50000 {
		t.Errorf("PhantomInvested: want 50000, got %v", snap.PhantomInvested)
	}
	if snap.CashBalance != -49000 { // -50 000 + 1 000 deposit
		t.Errorf("CashBalance: want -49000, got %v", snap.CashBalance)
	}
	// BookValue from open position (no MOEX prices cached → MarketValue == BookValue).
	if snap.BookValue != 50000 {
		t.Errorf("BookValue: want 50000, got %v", snap.BookValue)
	}
	// Equity = -49 000 + 50 000 + 50 000 = 51 000 — i.e. holdings value plus the
	// small residual cash, NOT 1 000 (deposits-minus-withdrawals alone).
	want := 51000.0
	if got := snap.Equity(); got != want {
		t.Errorf("Equity: want %v, got %v — regression of the partial-history bug", want, got)
	}
	// Invested must include the phantom so ROI%/CAGR use the true principal.
	if inv := snap.Invested(); inv != 51000 {
		t.Errorf("Invested: want 51000 (1000 deposit + 50000 phantom), got %v", inv)
	}
}
