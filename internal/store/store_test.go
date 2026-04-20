package store

import (
	"testing"
	"time"
)

func TestOpenAndMigrate(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	for _, table := range []string{"transactions", "moex_dividends", "moex_prices", "moex_fx", "fetch_state", "schema_migrations"} {
		var name string
		err := s.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("expected table %q to exist: %v", table, err)
		}
	}
}

func TestInsertTransactionDedup(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	qty := 10.0
	price := 285.50
	tx := &Transaction{
		Source:    SourceManual,
		Date:      "2026-04-15",
		OpType:    OpBuy,
		OpLabelRU: "Покупка актива",
		Ticker:    "SBER",
		Account:   "КлФ-TEST001",
		Amount:    2855.00,
		Currency:  "RUB",
		Quantity:  &qty,
		UnitPrice: &price,
	}

	res, err := s.InsertTransaction(tx)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if res != InsertedNew {
		t.Errorf("first insert: want InsertedNew, got %v", res)
	}
	if tx.TradeHash == "" {
		t.Errorf("trade hash should be populated after insert")
	}

	// Re-insert with a fresh struct (simulating re-import) — must dedupe.
	tx2 := &Transaction{
		Source:    SourceCSV, // different source on purpose
		SourceRef: "data.csv:1",
		Date:      "2026-04-15",
		OpType:    OpBuy,
		OpLabelRU: "Покупка актива",
		Ticker:    "SBER",
		Account:   "КлФ-TEST001",
		Amount:    2855.00,
		Currency:  "RUB",
		Quantity:  &qty,
		UnitPrice: &price,
	}
	res, err = s.InsertTransaction(tx2)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if res != InsertedDuplicate {
		t.Errorf("second insert: want InsertedDuplicate, got %v", res)
	}

	if tx.TradeHash != tx2.TradeHash {
		t.Errorf("hashes differ across sources: %s vs %s", tx.TradeHash, tx2.TradeHash)
	}

	got, err := s.CountTransactions()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if got != 1 {
		t.Errorf("count: want 1, got %d", got)
	}
}

func TestResetFetchState(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	pre := FetchState{
		Ticker:              "SBER",
		AssetClass:          "STOCK",
		LastPriceDate:       "2025-01-31",
		LastDividendCheckAt: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := s.SaveFetchState(pre); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := s.ResetFetchState("SBER"); err != nil {
		t.Fatalf("reset: %v", err)
	}

	got, err := s.GetFetchState("SBER")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LastPriceDate != "" {
		t.Errorf("LastPriceDate should be cleared, got %q", got.LastPriceDate)
	}
	if !got.LastDividendCheckAt.IsZero() {
		t.Errorf("LastDividendCheckAt should be zero, got %v", got.LastDividendCheckAt)
	}
	if got.AssetClass != "" {
		t.Errorf("AssetClass should be cleared, got %q", got.AssetClass)
	}
}

func TestUpsertMOEXPricesIdempotency(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	base := []MOEXPrice{
		{Ticker: "SBER", Date: "2024-01-03", Close: 271.5},
		{Ticker: "SBER", Date: "2024-01-04", Close: 273.2},
	}
	added, err := s.UpsertMOEXPrices(base, false)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if added != 2 {
		t.Errorf("first upsert: added=%d, want 2", added)
	}

	// Second pass: same rows plus one new one. Existing keys must be ignored.
	overlap := append([]MOEXPrice{}, base...)
	overlap = append(overlap, MOEXPrice{Ticker: "SBER", Date: "2024-01-05", Close: 270.0})
	added, err = s.UpsertMOEXPrices(overlap, false)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if added != 1 {
		t.Errorf("second upsert: added=%d, want 1 (only the new row)", added)
	}

	// Existing 2024-01-03 close must be unchanged — INSERT OR IGNORE never overwrites.
	lp, err := s.PriceAsOf("SBER", "2024-01-03", false)
	if err != nil {
		t.Fatalf("PriceAsOf: %v", err)
	}
	if lp.Close != 271.5 {
		t.Errorf("close was overwritten: got %v, want 271.5", lp.Close)
	}
}

func TestUpsertMOEXPricesRoutesCurrencyToFX(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// currencyMarket=true → moex_fx table (column "pair"/"rate"), not moex_prices.
	fx := []MOEXPrice{{Ticker: "CNYRUB_TOM", Date: "2024-05-01", Close: 12.34}}
	if _, err := s.UpsertMOEXPrices(fx, true); err != nil {
		t.Fatalf("upsert fx: %v", err)
	}

	// Must be retrievable with currencyMarket=true (moex_fx) ...
	p, err := s.LastPriceFor("CNYRUB_TOM", true)
	if err != nil {
		t.Fatalf("LastPriceFor fx: %v", err)
	}
	if p.Date != "2024-05-01" || p.Close != 12.34 {
		t.Errorf("fx price: got %+v", p)
	}

	// ... and must NOT leak into moex_prices (stocks/bonds table).
	if _, err := s.LastPriceFor("CNYRUB_TOM", false); err == nil {
		t.Error("currency price leaked into moex_prices table")
	}
}

func TestUpsertMOEXDividendsIdempotency(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	first := []MOEXDividend{
		{Ticker: "SBER", RegistryDate: "2024-07-11", Value: 33.3, Currency: "RUB"},
		{Ticker: "SBER", RegistryDate: "2023-05-11", Value: 25.0, Currency: "RUB"},
	}
	added, err := s.UpsertMOEXDividends(first)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if added != 2 {
		t.Errorf("first: added=%d, want 2", added)
	}

	// Re-upsert same rows + one new one.
	second := append([]MOEXDividend{}, first...)
	second = append(second, MOEXDividend{Ticker: "SBER", RegistryDate: "2025-07-10", Value: 36.5, Currency: "RUB"})
	added, err = s.UpsertMOEXDividends(second)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if added != 1 {
		t.Errorf("second: added=%d, want 1", added)
	}

	all, err := s.ListMOEXDividends("SBER")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("total rows: got %d, want 3", len(all))
	}
	// Ordering must be ascending by registry_date (per ListMOEXDividends contract).
	if all[0].RegistryDate != "2023-05-11" || all[2].RegistryDate != "2025-07-10" {
		t.Errorf("ordering wrong: %+v", all)
	}
}

func TestPriceAsOfBoundaries(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	if _, err := s.UpsertMOEXPrices([]MOEXPrice{
		{Ticker: "LKOH", Date: "2024-01-10", Close: 7100},
		{Ticker: "LKOH", Date: "2024-01-15", Close: 7250},
		{Ticker: "LKOH", Date: "2024-01-20", Close: 7180},
	}, false); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cases := []struct {
		name      string
		asOf      string
		wantDate  string
		wantClose float64
		wantErr   bool
	}{
		{"exact_match", "2024-01-15", "2024-01-15", 7250, false},
		{"in_between_picks_older", "2024-01-17", "2024-01-15", 7250, false},
		{"after_latest_picks_latest", "2024-02-01", "2024-01-20", 7180, false},
		{"before_earliest_errors", "2024-01-01", "", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := s.PriceAsOf("LKOH", c.asOf, false)
			if c.wantErr {
				if err == nil {
					t.Errorf("want ErrNotFound, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Date != c.wantDate || got.Close != c.wantClose {
				t.Errorf("got (%s, %v), want (%s, %v)", got.Date, got.Close, c.wantDate, c.wantClose)
			}
		})
	}
}

func TestPriceAsOfUnknownTicker(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	if _, err := s.PriceAsOf("NEVERSEEN", "2024-01-01", false); err == nil {
		t.Error("expected ErrNotFound for unknown ticker")
	}
	if _, err := s.LastPriceFor("NEVERSEEN", false); err == nil {
		t.Error("expected ErrNotFound from LastPriceFor for unknown ticker")
	}
}

func TestComputeHashStable(t *testing.T) {
	qty := 10.0
	price := 285.50
	mk := func() *Transaction {
		return &Transaction{
			Date:      "2026-04-15",
			Time:      "",
			OpType:    OpBuy,
			Ticker:    "SBER",
			Account:   "КлФ-TEST001",
			Amount:    2855.00,
			Currency:  "RUB",
			Quantity:  &qty,
			UnitPrice: &price,
			Comment:   "",
		}
	}
	a := ComputeHash(mk())
	b := ComputeHash(mk())
	if a != b {
		t.Errorf("hash not deterministic: %s vs %s", a, b)
	}

	// Differing comment → different hash.
	t2 := mk()
	t2.Comment = "split fill 2"
	if c := ComputeHash(t2); c == a {
		t.Errorf("hash should change when comment changes")
	}
}
