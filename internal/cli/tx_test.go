package cli

import (
	"testing"

	"investment-analyzer/internal/store"
)

func TestBuildTxFromFlags_SecurityInNoPrice(t *testing.T) {
	f := &txAddFlags{
		op:          "security-in",
		date:        "2026-04-24",
		account:     "TEST",
		ticker:      "GAZP",
		quantity:    100,
		hasQuantity: true,
		currency:    "RUB",
	}
	tx, err := buildTxFromFlags(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tx.OpType != store.OpSecurityIn {
		t.Errorf("OpType = %q, want SECURITY_IN", tx.OpType)
	}
	if tx.Quantity == nil || *tx.Quantity != 100 {
		t.Errorf("Quantity = %v, want 100", tx.Quantity)
	}
	if tx.UnitPrice != nil {
		t.Errorf("UnitPrice = %v, want nil when no --price given", *tx.UnitPrice)
	}
	if tx.Amount != 0 {
		t.Errorf("Amount = %v, want 0 when no price/amount given", tx.Amount)
	}
}

func TestBuildTxFromFlags_SecurityOutWithPrice(t *testing.T) {
	f := &txAddFlags{
		op:          "security-out",
		date:        "2026-04-24",
		account:     "TEST",
		ticker:      "GAZP",
		quantity:    30,
		price:       200,
		hasQuantity: true,
		hasPrice:    true,
		currency:    "RUB",
	}
	tx, err := buildTxFromFlags(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tx.OpType != store.OpSecurityOut {
		t.Errorf("OpType = %q, want SECURITY_OUT", tx.OpType)
	}
	if tx.Amount != 6000 {
		t.Errorf("Amount = %v, want 6000 (30 × 200)", tx.Amount)
	}
	if tx.UnitPrice == nil || *tx.UnitPrice != 200 {
		t.Errorf("UnitPrice = %v, want 200", tx.UnitPrice)
	}
}

func TestBuildTxFromFlags_SecurityInRequiresTicker(t *testing.T) {
	f := &txAddFlags{
		op:          "security-in",
		date:        "2026-04-24",
		account:     "TEST",
		quantity:    100,
		hasQuantity: true,
		currency:    "RUB",
	}
	if _, err := buildTxFromFlags(f); err == nil {
		t.Fatal("want error when --ticker is missing for security-in")
	}
}
