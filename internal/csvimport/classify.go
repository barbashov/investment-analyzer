package csvimport

import (
	"strings"

	"investment-analyzer/internal/store"
)

// ClassifyOp maps a Russian operation label from Finam's CSV to a canonical OpType.
// Returns ("", false) for an unknown label so the caller can decide whether to skip or error.
func ClassifyOp(labelRU string) (store.OpType, bool) {
	switch strings.TrimSpace(labelRU) {
	case "Покупка актива":
		return store.OpBuy, true
	case "Продажа актива":
		return store.OpSell, true
	case "Дивиденды":
		return store.OpDividend, true
	case "Ввод денежных средств":
		return store.OpDeposit, true
	case "Вывод денежных средств":
		return store.OpWithdrawal, true
	case "Перевод денежных средств":
		return store.OpTransfer, true
	case "Зачисление ценных бумаг",
		"Перевод ценных бумаг":
		// "Перевод ценных бумаг" is ambiguous in Finam exports; default to IN
		// (the most common case — portfolio received via custody transfer).
		// Manual override via `invest tx add` is available for outliers.
		return store.OpSecurityIn, true
	case "Списание ценных бумаг":
		return store.OpSecurityOut, true
	case "Брокерская комиссия",
		"Биржевая комиссия",
		"Брокерская комиссия за плечо",
		"Комиссия за вывод денежных средств через СБП",
		"Пеня (Списание)":
		return store.OpCommission, true
	case "Покупка валюты":
		return store.OpFXBuy, true
	case "Продажа валюты":
		return store.OpFXSell, true
	case "Доход по предоставлениям займа":
		return store.OpIncome, true
	case "Списание налога НДФЛ":
		return store.OpTax, true
	}
	return "", false
}
