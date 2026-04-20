package cli

import (
	"fmt"
	"math"
	"strings"

	"github.com/spf13/cobra"

	"investment-analyzer/internal/apperr"
	"investment-analyzer/internal/store"
	"investment-analyzer/internal/tui/trades"
)

func newTxCmd(ac *appContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tx",
		Short: "Manage individual transactions",
	}
	cmd.AddCommand(newTxAddCmd(ac), newTxListCmd(ac))
	return cmd
}

func newTxListCmd(ac *appContext) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Interactive trades browser (filter, sort, drill-down, delete manual rows)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(ac.opts.DBPath)
			if err != nil {
				return apperr.Wrap("store_open", "open database", 2, err)
			}
			defer func() { _ = st.Close() }()
			return trades.Run(st)
		},
	}
}

type txAddFlags struct {
	op       string
	date     string
	timeStr  string
	ticker   string
	account  string
	quantity float64
	price    float64
	amount   float64
	currency string
	tax      float64
	period   string
	note     string

	// Sentinels so we can tell "user passed 0" from "flag absent".
	hasQuantity bool
	hasPrice    bool
	hasAmount   bool
	hasTax      bool
}

func newTxAddCmd(ac *appContext) *cobra.Command {
	f := &txAddFlags{}
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Manually record a transaction (same dedup as CSV import)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f.hasQuantity = cmd.Flags().Changed("quantity")
			f.hasPrice = cmd.Flags().Changed("price")
			f.hasAmount = cmd.Flags().Changed("amount")
			f.hasTax = cmd.Flags().Changed("tax")

			tx, err := buildTxFromFlags(f)
			if err != nil {
				return apperr.New("validation", err.Error(), 2)
			}

			st, err := store.Open(ac.opts.DBPath)
			if err != nil {
				return apperr.Wrap("store_open", "open database", 2, err)
			}
			defer func() { _ = st.Close() }()

			res, err := st.InsertTransaction(tx)
			if err != nil {
				return apperr.Wrap("store_insert", "insert transaction", 2, err)
			}
			switch res {
			case store.InsertedNew:
				_, _ = fmt.Fprintf(ac.out, "added %s %s %s %s amount=%.2f %s (hash %s)\n",
					tx.OpType, tx.Date, defaultEmpty(tx.Ticker), tx.Account, tx.Amount, tx.Currency, shortHash(tx.TradeHash))
			case store.InsertedDuplicate:
				_, _ = fmt.Fprintf(ac.out, "duplicate: nothing inserted (hash %s)\n", shortHash(tx.TradeHash))
			}
			return nil
		},
	}

	pf := cmd.Flags()
	pf.StringVar(&f.op, "op", "", "Operation: buy|sell|dividend|deposit|withdrawal|transfer|commission|fx-buy|fx-sell|income|tax")
	pf.StringVar(&f.date, "date", "", "Date YYYY-MM-DD (required)")
	pf.StringVar(&f.timeStr, "time", "", "Time HH:MM:SS (optional)")
	pf.StringVar(&f.ticker, "ticker", "", "Ticker (e.g. SBER) — required for buy/sell/fx-*; optional for dividend")
	pf.StringVar(&f.account, "account", "", "Account ID (required)")
	pf.Float64Var(&f.quantity, "quantity", 0, "Quantity (required for buy/sell/fx-*)")
	pf.Float64Var(&f.price, "price", 0, "Unit price (one of --price or --amount required for buy/sell/fx-*)")
	pf.Float64Var(&f.amount, "amount", 0, "Total amount (required for cash-only ops; alternative to --price for buy/sell)")
	pf.StringVar(&f.currency, "currency", "RUB", "Currency code (RUB, CNY, ...)")
	pf.Float64Var(&f.tax, "tax", 0, "Tax withheld (DIVIDEND only)")
	pf.StringVar(&f.period, "period", "", "Dividend period e.g. \"2024\" or \"2кв 2024\" (DIVIDEND only)")
	pf.StringVar(&f.note, "note", "", "Free-text comment (participates in trade_hash for disambiguation)")

	_ = cmd.MarkFlagRequired("op")
	_ = cmd.MarkFlagRequired("date")
	_ = cmd.MarkFlagRequired("account")

	return cmd
}

func buildTxFromFlags(f *txAddFlags) (*store.Transaction, error) {
	op, label, err := normalizeOp(f.op)
	if err != nil {
		return nil, err
	}

	// Per-op validation + derivation.
	var (
		qty   *float64
		price *float64
		amt   = f.amount
	)

	switch op {
	case store.OpBuy, store.OpSell, store.OpFXBuy, store.OpFXSell:
		if f.ticker == "" {
			return nil, fmt.Errorf("--ticker is required for %s", op)
		}
		if !f.hasQuantity || f.quantity <= 0 {
			return nil, fmt.Errorf("--quantity > 0 is required for %s", op)
		}
		hasP := f.hasPrice && f.price > 0
		hasA := f.hasAmount && f.amount > 0
		if !hasP && !hasA {
			return nil, fmt.Errorf("either --price or --amount is required for %s", op)
		}
		if hasP && hasA {
			derived := f.quantity * f.price
			if math.Abs(derived-f.amount) > 0.01 {
				return nil, fmt.Errorf("--amount %.2f conflicts with quantity*price = %.2f", f.amount, derived)
			}
		}
		if hasP {
			amt = f.quantity * f.price
			price = ptrFloat(f.price)
		} else {
			price = ptrFloat(f.amount / f.quantity)
		}
		q := f.quantity
		qty = &q
	case store.OpDividend, store.OpDeposit, store.OpWithdrawal,
		store.OpTransfer, store.OpCommission, store.OpIncome, store.OpTax:
		if !f.hasAmount {
			// TRANSFER may be amount-less (securities-only); allow zero.
			if op != store.OpTransfer {
				return nil, fmt.Errorf("--amount is required for %s", op)
			}
			amt = 0
		} else if f.amount < 0 {
			return nil, fmt.Errorf("--amount must be positive (sign comes from op_type)")
		}
		// Empty ticker is allowed for DIVIDEND (matches Finam CSV quirk — ticker
		// is blank on dividend rows; we resolve via ISIN in the comment).
	default:
		return nil, fmt.Errorf("unsupported op: %s", op)
	}

	tx := &store.Transaction{
		Source:    store.SourceManual,
		Date:      f.date,
		Time:      f.timeStr,
		OpType:    op,
		OpLabelRU: label,
		Ticker:    strings.ToUpper(strings.TrimSpace(f.ticker)),
		Account:   f.account,
		Amount:    amt,
		Currency:  f.currency,
		Quantity:  qty,
		UnitPrice: price,
		Comment:   f.note,
	}

	if op == store.OpDividend {
		if f.hasTax {
			t := f.tax
			tx.DivTax = &t
		}
		tx.DivPeriod = f.period
	}

	return tx, nil
}

// normalizeOp returns the canonical OpType plus a Russian label for op_label_ru.
func normalizeOp(raw string) (store.OpType, string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "buy":
		return store.OpBuy, "Покупка актива", nil
	case "sell":
		return store.OpSell, "Продажа актива", nil
	case "dividend", "div":
		return store.OpDividend, "Дивиденды", nil
	case "deposit":
		return store.OpDeposit, "Ввод денежных средств", nil
	case "withdrawal", "withdraw":
		return store.OpWithdrawal, "Вывод денежных средств", nil
	case "transfer":
		return store.OpTransfer, "Перевод денежных средств", nil
	case "commission", "fee":
		return store.OpCommission, "Брокерская комиссия", nil
	case "fx-buy", "fxbuy":
		return store.OpFXBuy, "Покупка валюты", nil
	case "fx-sell", "fxsell":
		return store.OpFXSell, "Продажа валюты", nil
	case "income":
		return store.OpIncome, "Доход", nil
	case "tax":
		return store.OpTax, "Списание налога НДФЛ", nil
	}
	return "", "", fmt.Errorf("unknown op: %q", raw)
}

func ptrFloat(v float64) *float64 { return &v }

func defaultEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func shortHash(h string) string {
	if len(h) < 12 {
		return h
	}
	return h[:12]
}
