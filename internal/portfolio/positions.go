package portfolio

import (
	"sort"

	"investment-analyzer/internal/store"
)

// Lot is one open buy lot in FIFO order.
type Lot struct {
	Date     string
	Quantity float64
	UnitCost float64
}

// Position is the aggregated state of a ticker after walking all transactions in order.
type Position struct {
	Ticker      string
	AssetClass  string
	Quantity    float64
	BookValue   float64 // sum of (lot.Quantity * lot.UnitCost) for open lots
	AvgCost     float64 // BookValue / Quantity (0 when Quantity is 0)
	Lots        []Lot   // remaining open lots, oldest first
	RealizedPnL float64 // realized profit/loss from closed lots (currency-agnostic; assumes single currency per ticker)
}

// ComputePositions walks transactions in chronological order and returns final holdings keyed by ticker.
// Tickers with zero remaining quantity are still included (so realized P&L is visible).
// Pass asOf="" to use all transactions; otherwise only those with date <= asOf participate.
func ComputePositions(txs []*store.Transaction, asOf string) map[string]*Position {
	sorted := append([]*store.Transaction(nil), txs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Date != sorted[j].Date {
			return sorted[i].Date < sorted[j].Date
		}
		return sorted[i].Time < sorted[j].Time
	})

	positions := map[string]*Position{}

	for _, tx := range sorted {
		if asOf != "" && tx.Date > asOf {
			break
		}
		if tx.Ticker == "" {
			continue
		}
		switch tx.OpType {
		case store.OpBuy, store.OpFXBuy, store.OpSecurityIn:
			if tx.Quantity == nil || *tx.Quantity <= 0 {
				continue
			}
			pos := getOrInit(positions, tx.Ticker, tx.AssetClass)
			unit := unitCost(tx)
			pos.Lots = append(pos.Lots, Lot{Date: tx.Date, Quantity: *tx.Quantity, UnitCost: unit})
		case store.OpSell, store.OpFXSell, store.OpSecurityOut:
			if tx.Quantity == nil || *tx.Quantity <= 0 {
				continue
			}
			pos := getOrInit(positions, tx.Ticker, tx.AssetClass)
			sellQty := *tx.Quantity
			sellPrice := unitCost(tx)
			realize := tx.OpType != store.OpSecurityOut // custody transfer OUT is a movement, not a sale
			for sellQty > 0 && len(pos.Lots) > 0 {
				lot := &pos.Lots[0]
				take := lot.Quantity
				if take > sellQty {
					take = sellQty
				}
				if realize {
					pos.RealizedPnL += take * (sellPrice - lot.UnitCost)
				}
				lot.Quantity -= take
				sellQty -= take
				if lot.Quantity <= 1e-9 {
					pos.Lots = pos.Lots[1:]
				}
			}
		}
	}

	for _, pos := range positions {
		var qty, book float64
		for _, l := range pos.Lots {
			qty += l.Quantity
			book += l.Quantity * l.UnitCost
		}
		pos.Quantity = qty
		pos.BookValue = book
		if qty > 0 {
			pos.AvgCost = book / qty
		}
	}

	return positions
}

func getOrInit(m map[string]*Position, ticker, class string) *Position {
	if p, ok := m[ticker]; ok {
		if p.AssetClass == "" && class != "" {
			p.AssetClass = class
		}
		return p
	}
	p := &Position{Ticker: ticker, AssetClass: class}
	m[ticker] = p
	return p
}

func unitCost(tx *store.Transaction) float64 {
	if tx.UnitPrice != nil && *tx.UnitPrice > 0 {
		return *tx.UnitPrice
	}
	if tx.Quantity != nil && *tx.Quantity > 0 {
		return tx.Amount / *tx.Quantity
	}
	return 0
}
