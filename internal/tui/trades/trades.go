// Package trades implements `invest tx list` — an interactive trades browser.
package trades

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"investment-analyzer/internal/store"
	"investment-analyzer/internal/tui/browser"
	"investment-analyzer/internal/ui"
)

// TradeRow wraps a store.Transaction for browser rendering.
type TradeRow struct{ Tx *store.Transaction }

func (r TradeRow) Cells() []string {
	qty, price, amount := "—", "—", ui.FormatRUB(r.Tx.Amount)
	if r.Tx.Quantity != nil {
		qty = fmt.Sprintf("%g", *r.Tx.Quantity)
	}
	if r.Tx.UnitPrice != nil {
		price = ui.FormatRUB(*r.Tx.UnitPrice)
	}
	return []string{
		r.Tx.Date,
		emptyAsDash(r.Tx.Ticker),
		string(r.Tx.OpType),
		qty,
		price,
		amount,
		shortAccount(r.Tx.Account),
	}
}

func (r TradeRow) Detail() []ui.KVField {
	fields := []ui.KVField{
		{Label: "Hash", Value: shortHash(r.Tx.TradeHash)},
		{Label: "Source", Value: string(r.Tx.Source)},
		{Label: "Op (RU)", Value: r.Tx.OpLabelRU},
		{Label: "Account", Value: r.Tx.Account},
		{Label: "Currency", Value: r.Tx.Currency},
	}
	if r.Tx.AssetClass != "" {
		fields = append(fields, ui.KVField{Label: "Class", Value: r.Tx.AssetClass})
	}
	if r.Tx.AssetName != "" {
		fields = append(fields, ui.KVField{Label: "Asset", Value: r.Tx.AssetName})
	}
	if r.Tx.SourceRef != "" {
		fields = append(fields, ui.KVField{Label: "SourceRef", Value: r.Tx.SourceRef})
	}
	if r.Tx.Comment != "" {
		fields = append(fields, ui.KVField{Label: "Comment", Value: r.Tx.Comment})
	}
	return fields
}

func (r TradeRow) Match(tokens map[string]string) bool {
	for k, v := range tokens {
		v = strings.ToLower(v)
		switch k {
		case "":
			if !strings.Contains(strings.ToLower(r.Tx.Comment+" "+r.Tx.AssetName+" "+r.Tx.Ticker), v) {
				return false
			}
		case "ticker":
			if !strings.EqualFold(r.Tx.Ticker, v) {
				return false
			}
		case "op":
			// Allow comma-separated values.
			ok := false
			for _, want := range strings.Split(v, ",") {
				if strings.EqualFold(string(r.Tx.OpType), want) {
					ok = true
					break
				}
			}
			if !ok {
				return false
			}
		case "account":
			if !strings.Contains(strings.ToLower(r.Tx.Account), v) {
				return false
			}
		case "from":
			if r.Tx.Date < v {
				return false
			}
		case "to":
			if r.Tx.Date > v {
				return false
			}
		case "source":
			if !strings.EqualFold(string(r.Tx.Source), v) {
				return false
			}
		}
	}
	return true
}

// Run starts the trades browser. `st` is needed for delete persistence.
func Run(st *store.Store) error {
	txs, err := st.ListTransactions("", "")
	if err != nil {
		return err
	}
	rows := toRows(txs)
	sorts := []browser.SortMode{
		{Label: "date ↓", Less: func(a, b browser.Row) bool {
			return a.(TradeRow).Tx.Date > b.(TradeRow).Tx.Date
		}},
		{Label: "date ↑", Less: func(a, b browser.Row) bool {
			return a.(TradeRow).Tx.Date < b.(TradeRow).Tx.Date
		}},
		{Label: "ticker", Less: func(a, b browser.Row) bool {
			return a.(TradeRow).Tx.Ticker < b.(TradeRow).Tx.Ticker
		}},
		{Label: "amount ↓", Less: func(a, b browser.Row) bool {
			return a.(TradeRow).Tx.Amount > b.(TradeRow).Tx.Amount
		}},
	}

	m := browser.New("Trades",
		[]string{"DATE", "TICKER", "OP", "QTY", "PRICE", "AMOUNT", "ACCOUNT"},
		rows, sorts)
	m.FilterHelp = "keys: ticker:SBER  op:buy,sell  account:…  from:YYYY-MM-DD  to:YYYY-MM-DD  source:csv|manual  (free text matches comment/asset/ticker)"

	pendingDelete := ""
	m.OnKey = func(msg tea.KeyMsg, current browser.Row) (tea.Cmd, bool) {
		if msg.String() != "d" {
			pendingDelete = "" // any other key cancels
			return nil, false
		}
		if current == nil {
			return nil, true
		}
		tx := current.(TradeRow).Tx
		if tx.Source != store.SourceManual {
			return nil, true // CSV rows are read-only
		}
		if pendingDelete != tx.TradeHash {
			pendingDelete = tx.TradeHash
			return nil, true
		}
		// confirmed
		if _, err := st.DB.Exec(`DELETE FROM transactions WHERE trade_hash = ?`, tx.TradeHash); err != nil {
			pendingDelete = ""
			return nil, true
		}
		pendingDelete = ""
		// Reload data
		if fresh, err := st.ListTransactions("", ""); err == nil {
			m.Refresh(toRows(fresh))
		}
		return nil, true
	}

	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func toRows(txs []*store.Transaction) []browser.Row {
	out := make([]browser.Row, 0, len(txs))
	for _, t := range txs {
		out = append(out, TradeRow{Tx: t})
	}
	return out
}

func emptyAsDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func shortAccount(a string) string {
	if r := []rune(a); len(r) > 12 {
		return "…" + string(r[len(r)-10:])
	}
	return a
}

func shortHash(h string) string {
	if len(h) < 12 {
		return h
	}
	return h[:12]
}
