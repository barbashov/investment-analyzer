package csvimport

import (
	"path/filepath"
	"testing"

	"investment-analyzer/internal/store"
)

func TestParseFileSample(t *testing.T) {
	res, err := ParseFile(filepath.Join("testdata", "sample.csv"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected row errors: %v", res.Errors)
	}
	if got, want := len(res.Transactions), 7; got != want {
		t.Fatalf("transaction count: got %d, want %d", got, want)
	}

	// Row 0: FXUS buy — amount, qty, price, currency
	t0 := res.Transactions[0]
	if t0.OpType != store.OpBuy {
		t.Errorf("row0 op: got %s, want BUY", t0.OpType)
	}
	if t0.Ticker != "FXUS" {
		t.Errorf("row0 ticker: got %q", t0.Ticker)
	}
	if t0.Amount != 10000.00 {
		t.Errorf("row0 amount: got %v, want 10000.00", t0.Amount)
	}
	if t0.Quantity == nil || *t0.Quantity != 100.0 {
		t.Errorf("row0 quantity: got %v", t0.Quantity)
	}
	if t0.UnitPrice == nil || *t0.UnitPrice != 100.00 {
		t.Errorf("row0 unit_price: got %v", t0.UnitPrice)
	}

	// Row 2: deposit with leading + sign — amount must be stored as positive.
	if d := res.Transactions[2]; d.OpType != store.OpDeposit || d.Amount != 10000.00 {
		t.Errorf("deposit row: op=%s amount=%v", d.OpType, d.Amount)
	}

	// Row 3: dividend — tax/period/asset_name extracted from comment.
	div := res.Transactions[3]
	if div.OpType != store.OpDividend {
		t.Fatalf("div op: got %s", div.OpType)
	}
	if div.DivTax == nil || *div.DivTax != 75.00 {
		t.Errorf("div tax: got %v", div.DivTax)
	}
	if div.DivPeriod != "2023" {
		t.Errorf("div period: got %q", div.DivPeriod)
	}
	if div.AssetName == "" {
		t.Errorf("div asset_name should be derived from comment, got empty")
	}
	if div.Ticker != "" {
		t.Errorf("div ticker should remain empty (Finam quirk), got %q", div.Ticker)
	}

	// Row 6: time present + currency-pair buy.
	fx := res.Transactions[6]
	if fx.OpType != store.OpFXBuy {
		t.Errorf("fx op: got %s", fx.OpType)
	}
	if fx.Time != "14:30:00" {
		t.Errorf("fx time: got %q", fx.Time)
	}
	if fx.Ticker != "CNYRUB_TOM" {
		t.Errorf("fx ticker: got %q", fx.Ticker)
	}
}

func TestParseAndInsertIdempotent(t *testing.T) {
	res, err := ParseFile(filepath.Join("testdata", "sample.csv"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	insertAll := func() (added, dup int) {
		for _, tx := range res.Transactions {
			r, err := st.InsertTransaction(tx)
			if err != nil {
				t.Fatalf("insert: %v", err)
			}
			if r == store.InsertedNew {
				added++
			} else {
				dup++
			}
		}
		return
	}

	a1, d1 := insertAll()
	if a1 != 7 || d1 != 0 {
		t.Errorf("first pass: added=%d dup=%d, want 7/0", a1, d1)
	}
	a2, d2 := insertAll()
	if a2 != 0 || d2 != 7 {
		t.Errorf("second pass: added=%d dup=%d, want 0/7", a2, d2)
	}
}

func TestParseDividendCommentHelpers(t *testing.T) {
	c := "Дивиденды по Сбербанк, АП, 003, RU0009029557 - за 2023 год Из суммы к выплате 1150.00 руб. удержан налог 150.00 руб."
	if tax := ParseDividendTax(c); tax == nil || *tax != 150.00 {
		t.Errorf("tax: got %v", tax)
	}
	if got := ParseDividendPeriod(c); got != "2023" {
		t.Errorf("period: got %q", got)
	}
	if got := ParseDividendISIN(c); got != "RU0009029557" {
		t.Errorf("isin: got %q", got)
	}
	if got := ParseDividendAssetName(c); got != "Сбербанк, АП, 003, RU0009029557" {
		t.Errorf("asset_name: got %q", got)
	}

	cq := "Дивиденды по X - за 2 кв 2024 ... удержан налог 100,50 руб."
	if got := ParseDividendPeriod(cq); got != "2кв 2024" {
		t.Errorf("quarterly period: got %q", got)
	}
	if tax := ParseDividendTax(cq); tax == nil || *tax != 100.50 {
		t.Errorf("comma tax: got %v", tax)
	}
}
