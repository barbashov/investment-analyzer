package csvimport

import (
	"regexp"
	"strconv"
	"strings"
)

// Examples of Finam dividend comments:
//   "Дивиденды по НОВАТЭК, АО, 002, RU000A0DKVS5 - за 2023 год Из суммы к выплате 1000.00 руб. удержан налог 130.00 руб."
//   "Дивиденды по Сбербанк, АП, 003, RU0009029557 - за 2023 год ..."
//
// Numbers in the comment use DOT as the decimal separator (unlike the CSV's amount column which uses comma).

var (
	taxRe         = regexp.MustCompile(`удержан налог\s+([\d]+(?:[.,]\d+)?)\s*руб`)
	periodYear    = regexp.MustCompile(`за\s+(\d{4})\s*год`)
	periodQuart   = regexp.MustCompile(`за\s+(\d)\s*кв\s+(\d{4})`)
	periodQuarter = regexp.MustCompile(`за\s+(\d)\s*-?[а-я]*\s+квартал\w*\s+(\d{4})`)
	periodHalf    = regexp.MustCompile(`за\s+(\d)\s*-?[а-я]*\s+полугоди\w*\s+(\d{4})`)
	periodMonths  = regexp.MustCompile(`за\s+(\d+)\s+месяц\w*\s+(\d{4})`)
	isinRe        = regexp.MustCompile(`\b([A-Z]{2}[A-Z0-9]{9}\d)\b`)
	assetRe       = regexp.MustCompile(`Дивиденды по\s+(.+?)\s*-\s*за\s`)
)

// ParseDividendTax returns the kopeck-precise tax amount mentioned in the comment, or nil if absent.
func ParseDividendTax(comment string) *float64 {
	m := taxRe.FindStringSubmatch(comment)
	if m == nil {
		return nil
	}
	s := strings.ReplaceAll(m[1], ",", ".")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

// ParseDividendPeriod returns a normalized period string (e.g. "2022", "2кв 2022",
// "1п 2024", "9мес 2024") or "" when the comment doesn't mention a period.
func ParseDividendPeriod(comment string) string {
	if m := periodQuart.FindStringSubmatch(comment); m != nil {
		return m[1] + "кв " + m[2]
	}
	if m := periodQuarter.FindStringSubmatch(comment); m != nil {
		return m[1] + "кв " + m[2]
	}
	if m := periodHalf.FindStringSubmatch(comment); m != nil {
		return m[1] + "п " + m[2]
	}
	if m := periodMonths.FindStringSubmatch(comment); m != nil {
		return m[1] + "мес " + m[2]
	}
	if m := periodYear.FindStringSubmatch(comment); m != nil {
		return m[1]
	}
	return ""
}

// ParseDividendISIN extracts the 12-char ISIN from a dividend comment, or "" if absent.
func ParseDividendISIN(comment string) string {
	if m := isinRe.FindStringSubmatch(comment); m != nil {
		return m[1]
	}
	return ""
}

// ParseDividendAssetName extracts the asset name segment between "Дивиденды по " and " - за".
// Example: "НОВАТЭК, АО, 002, RU000A0DKVS5". Returns "" if the comment doesn't match.
func ParseDividendAssetName(comment string) string {
	if m := assetRe.FindStringSubmatch(comment); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}
