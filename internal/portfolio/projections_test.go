package portfolio

import (
	"testing"
	"time"

	"investment-analyzer/internal/store"
)

func floatPtr(v float64) *float64 { return &v }

func buyTx(date, ticker string, qty, price float64) *store.Transaction {
	return &store.Transaction{
		Date:      date,
		OpType:    store.OpBuy,
		Ticker:    ticker,
		Account:   "MAIN",
		Currency:  "RUB",
		Quantity:  floatPtr(qty),
		UnitPrice: floatPtr(price),
		Amount:    qty * price,
	}
}

func TestProjectPayments_SynthesizesFutureRow(t *testing.T) {
	txs := []*store.Transaction{
		buyTx("2024-01-10", "LKOH", 10, 6500),
	}
	announcements := []store.SmartlabDividend{
		{Ticker: "LKOH", Period: "4кв 2025", T2Date: "2026-05-01", ExDate: "2026-05-04", ValuePerShare: 278},
	}
	moex := map[string][]store.MOEXDividend{}
	asOf := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)

	got := ProjectPayments(announcements, moex, txs, asOf)
	if len(got) != 1 {
		t.Fatalf("want 1 projected row, got %d", len(got))
	}
	p := got[0]
	if !p.Projected {
		t.Error("row must have Projected=true")
	}
	if p.Gross != 2780 {
		t.Errorf("Gross = %v, want 2780 (10 * 278)", p.Gross)
	}
	wantTax := 2780 * RUStockTaxRate
	if diff := p.Tax - wantTax; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("Tax = %v, want %v", p.Tax, wantTax)
	}
	if diff := p.Net - (2780 - wantTax); diff > 1e-6 || diff < -1e-6 {
		t.Errorf("Net = %v, want %v", p.Net, 2780-wantTax)
	}
	if p.BookValue != 65000 {
		t.Errorf("BookValue = %v, want 65000 (10 * 6500)", p.BookValue)
	}
	// Yield = 2780 / 65000 ≈ 4.28%
	if p.Yield < 0.042 || p.Yield > 0.044 {
		t.Errorf("Yield = %v, want ≈ 0.0428", p.Yield)
	}
}

func TestProjectPayments_SkipsPastExDates(t *testing.T) {
	txs := []*store.Transaction{buyTx("2024-01-10", "LKOH", 10, 6500)}
	announcements := []store.SmartlabDividend{
		{Ticker: "LKOH", Period: "3кв 2025", T2Date: "2026-01-09", ExDate: "2026-01-12", ValuePerShare: 397},
	}
	asOf := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)

	got := ProjectPayments(announcements, nil, txs, asOf)
	if len(got) != 0 {
		t.Errorf("past ex-date should not be projected, got %d rows", len(got))
	}
}

func TestProjectPayments_SkipsZeroHoldings(t *testing.T) {
	// User holds no LKOH → no projected row.
	announcements := []store.SmartlabDividend{
		{Ticker: "LKOH", Period: "4кв 2025", T2Date: "2026-05-01", ExDate: "2026-05-04", ValuePerShare: 278},
	}
	asOf := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	got := ProjectPayments(announcements, nil, nil, asOf)
	if len(got) != 0 {
		t.Errorf("zero holdings → no projection, got %d rows", len(got))
	}
}

func TestSupersedesByMOEX(t *testing.T) {
	sl := store.SmartlabDividend{T2Date: "2026-05-01"}
	tests := []struct {
		name string
		moex []store.MOEXDividend
		want bool
	}{
		{"no moex data", nil, false},
		{"moex registry inside window (+10 days)", []store.MOEXDividend{{RegistryDate: "2026-05-11"}}, true},
		{"moex registry slightly before (-7 days)", []store.MOEXDividend{{RegistryDate: "2026-04-24"}}, true},
		{"moex registry far before window", []store.MOEXDividend{{RegistryDate: "2025-12-01"}}, false},
		{"moex registry far after window", []store.MOEXDividend{{RegistryDate: "2026-09-01"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SupersedesByMOEX(sl, tt.moex); got != tt.want {
				t.Errorf("SupersedesByMOEX = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProjectPayments_SuppressedByMOEX(t *testing.T) {
	txs := []*store.Transaction{buyTx("2024-01-10", "LKOH", 10, 6500)}
	announcements := []store.SmartlabDividend{
		{Ticker: "LKOH", Period: "4кв 2025", T2Date: "2026-05-01", ExDate: "2026-05-04", ValuePerShare: 278},
	}
	moex := map[string][]store.MOEXDividend{
		"LKOH": {{Ticker: "LKOH", RegistryDate: "2026-05-02", Value: 278}},
	}
	asOf := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)

	got := ProjectPayments(announcements, moex, txs, asOf)
	if len(got) != 0 {
		t.Errorf("MOEX confirmation should hide smart-lab projection, got %d rows", len(got))
	}
}
