package portfolio

import (
	"sort"

	"investment-analyzer/internal/csvimport"
	"investment-analyzer/internal/store"
)

// DividendPayment is a single received dividend, with the ticker resolved if possible.
type DividendPayment struct {
	Date      string
	Ticker    string  // resolved via TickerResolver; "" if unresolved
	AssetName string  // human label from the comment
	ISIN      string  // extracted from comment when present
	Period    string  // "2024" / "2кв 2024" / ...
	Account   string
	Currency  string
	Net       float64 // = transaction amount (post-tax inflow)
	Tax       float64 // 0 when not parseable from comment
	Gross     float64 // Net + Tax
	BookValue float64 // book value of the position on Date (filled by AnnotatePayments; 0 if unknown)
	Yield     float64 // Gross / BookValue (filled by AnnotatePayments; 0 if BookValue is 0)
}

// TickerResolver maps an ISIN or asset-name fragment to a ticker. Implementations decide the lookup.
type TickerResolver interface {
	Resolve(isin, assetName string) string
}

// MapTickerResolver is a simple in-memory ISIN→ticker map.
// Phase 8 will replace this with a MOEX-backed resolver.
type MapTickerResolver struct {
	ByISIN map[string]string
}

func (m MapTickerResolver) Resolve(isin, _ string) string {
	if isin == "" {
		return ""
	}
	return m.ByISIN[isin]
}

// ExtractDividends produces one DividendPayment per DIVIDEND transaction.
// `resolver` may be nil; tickers are then left empty.
func ExtractDividends(txs []*store.Transaction, resolver TickerResolver) []DividendPayment {
	var out []DividendPayment
	for _, tx := range txs {
		if tx.OpType != store.OpDividend {
			continue
		}
		isin := csvimport.ParseDividendISIN(tx.Comment)
		ticker := tx.Ticker
		if ticker == "" && resolver != nil {
			ticker = resolver.Resolve(isin, tx.AssetName)
		}
		var tax float64
		if tx.DivTax != nil {
			tax = *tx.DivTax
		}
		period := tx.DivPeriod
		if period == "" {
			period = csvimport.ParseDividendPeriod(tx.Comment)
		}
		if period == "" && len(tx.Date) >= 4 {
			period = tx.Date[:4]
		}
		out = append(out, DividendPayment{
			Date:      tx.Date,
			Ticker:    ticker,
			AssetName: tx.AssetName,
			ISIN:      isin,
			Period:    period,
			Account:   tx.Account,
			Currency:  tx.Currency,
			Net:       tx.Amount,
			Tax:       tax,
			Gross:     tx.Amount + tax,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// AnnotatePayments fills BookValue/Yield on each payment using positions at the payment date.
// Computes positions once per unique date (sorted ascending so each pass is incremental in spirit
// — even though ComputePositions itself walks from start, this avoids quadratic blowup on payment count).
func AnnotatePayments(payments []DividendPayment, txs []*store.Transaction) []DividendPayment {
	if len(payments) == 0 {
		return payments
	}
	dates := make(map[string]struct{}, len(payments))
	for _, p := range payments {
		dates[p.Date] = struct{}{}
	}
	cache := make(map[string]map[string]*Position, len(dates))
	for d := range dates {
		cache[d] = ComputePositions(txs, d)
	}
	for i := range payments {
		p := &payments[i]
		if p.Ticker == "" {
			continue
		}
		pos, ok := cache[p.Date][p.Ticker]
		if !ok || pos.BookValue <= 0 {
			continue
		}
		p.BookValue = pos.BookValue
		p.Yield = p.Gross / pos.BookValue
	}
	return payments
}

// DividendBucket is an aggregate over some grouping (by ticker, by year, by month).
type DividendBucket struct {
	Key            string
	Payments       int
	Gross          float64
	Tax            float64
	Net            float64
	BookValueSum   float64 // Σ BookValue over payments in this bucket (for Yield calc)
	Yield          float64 // Σ Gross / Σ BookValue when BookValueSum > 0; 0 otherwise
}

// GroupBy returns aggregated buckets keyed by the result of `keyFn(p)`. Buckets with empty key are dropped.
// The result is sorted by descending Net (so the biggest contributors float to the top of reports).
func GroupBy(payments []DividendPayment, keyFn func(DividendPayment) string) []DividendBucket {
	bucketMap := map[string]*DividendBucket{}
	for _, p := range payments {
		k := keyFn(p)
		if k == "" {
			continue
		}
		b, ok := bucketMap[k]
		if !ok {
			b = &DividendBucket{Key: k}
			bucketMap[k] = b
		}
		b.Payments++
		b.Gross += p.Gross
		b.Tax += p.Tax
		b.Net += p.Net
		b.BookValueSum += p.BookValue
	}
	out := make([]DividendBucket, 0, len(bucketMap))
	for _, b := range bucketMap {
		if b.BookValueSum > 0 {
			b.Yield = b.Gross / b.BookValueSum
		}
		out = append(out, *b)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Net != out[j].Net {
			return out[i].Net > out[j].Net
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// ByTicker is a convenience grouping function. Falls back to AssetName when ticker is unresolved.
func ByTicker(p DividendPayment) string {
	if p.Ticker != "" {
		return p.Ticker
	}
	return p.AssetName
}

// ByYear groups by the YYYY prefix of the date.
func ByYear(p DividendPayment) string {
	if len(p.Date) >= 4 {
		return p.Date[:4]
	}
	return ""
}

// ByMonth groups by YYYY-MM.
func ByMonth(p DividendPayment) string {
	if len(p.Date) >= 7 {
		return p.Date[:7]
	}
	return ""
}
