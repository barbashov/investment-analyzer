package portfolio

import (
	"testing"

	"investment-analyzer/internal/store"
)

func ptr(v float64) *float64 { return &v }

func TestComputePositionsFIFO(t *testing.T) {
	txs := []*store.Transaction{
		{Date: "2024-01-01", OpType: store.OpBuy, Ticker: "SBER", Quantity: ptr(10), UnitPrice: ptr(100), Amount: 1000, Currency: "RUB"},
		{Date: "2024-02-01", OpType: store.OpBuy, Ticker: "SBER", Quantity: ptr(5), UnitPrice: ptr(120), Amount: 600, Currency: "RUB"},
		{Date: "2024-03-01", OpType: store.OpSell, Ticker: "SBER", Quantity: ptr(7), UnitPrice: ptr(150), Amount: 1050, Currency: "RUB"},
	}
	pos := ComputePositions(txs, "")["SBER"]
	if pos == nil {
		t.Fatal("position missing")
	}
	if pos.Quantity != 8 {
		t.Errorf("qty: want 8, got %v", pos.Quantity)
	}
	// Remaining lots: 3 @ 100, 5 @ 120 → book = 300 + 600 = 900, avg = 112.5
	if pos.BookValue != 900 {
		t.Errorf("book: want 900, got %v", pos.BookValue)
	}
	if pos.AvgCost != 112.5 {
		t.Errorf("avg: want 112.5, got %v", pos.AvgCost)
	}
	// Realized: 7 sold @ 150, all from the 100-cost lot → (150-100)*7 = 350
	if pos.RealizedPnL != 350 {
		t.Errorf("realized: want 350, got %v", pos.RealizedPnL)
	}
}

func TestComputePositionsSecurityTransfers(t *testing.T) {
	txs := []*store.Transaction{
		// Custody-transferred in — creates a lot at the stated cost basis.
		{Date: "2024-01-01", OpType: store.OpSecurityIn, Ticker: "GAZP", Quantity: ptr(100), UnitPrice: ptr(150), Amount: 15000, Currency: "RUB"},
		{Date: "2024-02-01", OpType: store.OpBuy, Ticker: "GAZP", Quantity: ptr(50), UnitPrice: ptr(160), Amount: 8000, Currency: "RUB"},
		// Outbound transfer — consumes FIFO lots but must NOT accrue realized P&L.
		{Date: "2024-03-01", OpType: store.OpSecurityOut, Ticker: "GAZP", Quantity: ptr(30), UnitPrice: ptr(200), Amount: 6000, Currency: "RUB"},
	}
	pos := ComputePositions(txs, "")["GAZP"]
	if pos == nil {
		t.Fatal("GAZP position missing")
	}
	if pos.Quantity != 120 {
		t.Errorf("qty: want 120 (100 in + 50 buy - 30 out), got %v", pos.Quantity)
	}
	// Remaining lots: 70 @ 150, 50 @ 160 → book = 10500 + 8000 = 18500
	if pos.BookValue != 18500 {
		t.Errorf("book: want 18500, got %v", pos.BookValue)
	}
	if pos.RealizedPnL != 0 {
		t.Errorf("realized P&L must stay 0 for outbound custody transfers, got %v", pos.RealizedPnL)
	}
}

func TestComputePositionsAsOf(t *testing.T) {
	txs := []*store.Transaction{
		{Date: "2024-01-01", OpType: store.OpBuy, Ticker: "SBER", Quantity: ptr(10), UnitPrice: ptr(100), Amount: 1000, Currency: "RUB"},
		{Date: "2024-06-01", OpType: store.OpBuy, Ticker: "SBER", Quantity: ptr(10), UnitPrice: ptr(200), Amount: 2000, Currency: "RUB"},
	}
	pos := ComputePositions(txs, "2024-04-01")["SBER"]
	if pos == nil || pos.Quantity != 10 {
		t.Errorf("expected 10 shares as of 2024-04-01, got %+v", pos)
	}
}

func TestExtractAndGroupDividends(t *testing.T) {
	txs := []*store.Transaction{
		{Date: "2024-05-19", OpType: store.OpDividend, AssetName: "Сбербанк", Comment: "Дивиденды по Сбербанк, АП, 003, RU0009029557 - за 2023 год Из суммы к выплате 1150.00 руб. удержан налог 150.00 руб.", Amount: 1000, DivTax: ptr(150), DivPeriod: "2023", Currency: "RUB"},
		{Date: "2024-06-15", OpType: store.OpDividend, AssetName: "ЛУКойл", Comment: "Дивиденды по ЛУКойл НК, АО, 001, RU0009024277 - за 2023 год удержан налог 300.00 руб.", Amount: 2000, DivTax: ptr(300), DivPeriod: "2023", Currency: "RUB"},
		{Date: "2025-05-19", OpType: store.OpDividend, AssetName: "Сбербанк", Comment: "Дивиденды по Сбербанк, АП, 003, RU0009029557 - за 2024 год", Amount: 1500, DivPeriod: "2024", Currency: "RUB"},
		// Non-dividend rows must be ignored.
		{Date: "2024-01-01", OpType: store.OpBuy, Ticker: "SBER", Amount: 1000, Currency: "RUB"},
	}
	resolver := MapTickerResolver{ByISIN: map[string]string{
		"RU0009029557": "SBERP",
		"RU0009024277": "LKOH",
	}}
	pays := ExtractDividends(txs, resolver)
	if got, want := len(pays), 3; got != want {
		t.Fatalf("payments: got %d want %d", got, want)
	}

	byTicker := GroupBy(pays, ByTicker)
	if len(byTicker) != 2 {
		t.Fatalf("by-ticker buckets: got %d want 2", len(byTicker))
	}
	// SBERP has the larger total net (1000 + 1500 = 2500) so it should be first.
	if byTicker[0].Key != "SBERP" || byTicker[0].Net != 2500 || byTicker[0].Payments != 2 {
		t.Errorf("SBERP bucket wrong: %+v", byTicker[0])
	}
	if byTicker[1].Key != "LKOH" || byTicker[1].Net != 2000 || byTicker[1].Gross != 2300 {
		t.Errorf("LKOH bucket wrong: %+v", byTicker[1])
	}

	byYear := GroupBy(pays, ByYear)
	if len(byYear) != 2 {
		t.Fatalf("by-year: got %d want 2", len(byYear))
	}
}
