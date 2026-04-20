package assets

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		ticker string
		want   Class
	}{
		{"SBER", ClassStock},
		{"SBERP", ClassStock},
		{"LKOH", ClassStock},
		{"FXUS", ClassETF},
		{"FXTB", ClassETF},
		{"OFZ-26215", ClassBond},
		{"SU26215RMFS2", ClassBond},
		{"SU25084RMFS3", ClassBond},
		{"RU000A1054W1", ClassBond},
		{"CNYRUB_TOM", ClassCurrency},
		{"GLDRUB_TOM", ClassCurrency},
		{"USDRUB_TMS", ClassCurrency},
		{"", ClassUnknown},
	}
	for _, c := range cases {
		if got := Classify(c.ticker); got != c.want {
			t.Errorf("Classify(%q) = %s, want %s", c.ticker, got, c.want)
		}
	}
}

func TestMOEXRouting(t *testing.T) {
	if got := MOEXEngine(ClassCurrency); got != "currency" {
		t.Errorf("CURRENCY engine: got %s, want currency", got)
	}
	if got := MOEXMarket(ClassCurrency); got != "selt" {
		t.Errorf("CURRENCY market: got %s, want selt", got)
	}
	if got := MOEXMarket(ClassBond); got != "bonds" {
		t.Errorf("BOND market: got %s, want bonds", got)
	}
	if got := MOEXMarket(ClassStock); got != "shares" {
		t.Errorf("STOCK market: got %s, want shares", got)
	}
}
