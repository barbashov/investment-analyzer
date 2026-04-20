package ui

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// FormatMoney returns "1 234 567,89" — RU-locale style with thin spaces and decimal comma.
// `currency` is appended as a suffix (e.g. "₽", "¥"); pass "" to omit.
func FormatMoney(v float64, currency string) string {
	neg := v < 0
	v = math.Abs(v)
	// Round to cents first, then split — avoids the .995 carry bug where rounding
	// the fractional part alone ticks over to 100 and gets swallowed by % 100.
	cents := int64(math.Round(v * 100))
	whole := cents / 100
	frac := cents % 100

	s := groupThousands(whole)
	out := fmt.Sprintf("%s,%02d", s, frac)
	if neg {
		out = "-" + out
	}
	if currency != "" {
		out += " " + currency
	}
	return out
}

// FormatRUB is FormatMoney with the ruble symbol.
func FormatRUB(v float64) string { return FormatMoney(v, "₽") }

// FormatInt formats an integer with thousand separators (no currency).
func FormatInt(v int64) string {
	if v < 0 {
		return "-" + groupThousands(-v)
	}
	return groupThousands(v)
}

// FormatPct returns "12.4%" — dot decimal, one digit after the dot.
func FormatPct(v float64) string {
	return fmt.Sprintf("%.1f%%", v)
}

// FormatDate returns YYYY-MM-DD.
func FormatDate(t time.Time) string { return t.Format("2006-01-02") }

func groupThousands(v int64) string {
	s := fmt.Sprintf("%d", v)
	if len(s) <= 3 {
		return s
	}
	// Insert thin space every 3 digits from the right.
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		if len(s) > rem {
			b.WriteString("\u2009") // thin space
		}
	}
	for i := rem; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteString("\u2009")
		}
	}
	return b.String()
}
