package ui

import "testing"

func TestFormatMoneyRoundingCarry(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want string
	}{
		{"carry_up", 1.995, "2,00 ₽"},
		{"half_up_at_cent", 0.005, "0,01 ₽"},
		{"no_carry", 1.994, "1,99 ₽"},
		{"negative_carry", -1.995, "-2,00 ₽"},
		{"integer", 1234.0, "1\u2009234,00 ₽"},
		{"grouping", 1234567.89, "1\u2009234\u2009567,89 ₽"},
		{"zero", 0, "0,00 ₽"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FormatRUB(c.in); got != c.want {
				t.Errorf("FormatRUB(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
