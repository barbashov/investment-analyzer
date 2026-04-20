// Package ui provides synocli-style styled output (lipgloss tables, KV blocks).
package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// AnsiClearScreen is the escape sequence used by --watch to repaint cleanly.
const AnsiClearScreen = "\x1b[H\x1b[2J"

// KVField is a single label:value pair rendered by PrintKVBlock.
type KVField struct {
	Label string
	Value string
}

// HumanUI carries terminal capabilities + a lipgloss renderer bound to a specific writer.
type HumanUI struct {
	renderer *lipgloss.Renderer
	Styled   bool
	Tty      bool
}

// NewHumanUI builds a HumanUI for the given writer. Honors NO_COLOR and TTY presence.
func NewHumanUI(w io.Writer) HumanUI {
	tty := isTTYWriter(w)
	r := lipgloss.NewRenderer(w, termenv.WithTTY(tty))
	noColor := termenv.EnvNoColor()
	styled := tty && !noColor
	if !styled {
		r.SetColorProfile(termenv.Ascii)
	}
	return HumanUI{renderer: r, Styled: styled, Tty: tty}
}

func isTTYWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func (u HumanUI) style() lipgloss.Style { return u.renderer.NewStyle() }

func (u HumanUI) Title(text string) string {
	s := u.style().Bold(true)
	if u.Styled {
		s = s.Foreground(lipgloss.AdaptiveColor{Light: "27", Dark: "117"})
	}
	return s.Render(text)
}

func (u HumanUI) Muted(text string) string {
	s := u.style()
	if u.Styled {
		s = s.Foreground(lipgloss.AdaptiveColor{Light: "244", Dark: "245"})
	}
	return s.Render(text)
}

// Gain colors a string green (used for positive P&L, dividends received).
func (u HumanUI) Gain(text string) string {
	if !u.Styled {
		return text
	}
	return u.style().Foreground(lipgloss.Color("42")).Render(text)
}

// Loss colors a string red (used for negative P&L, taxes withheld).
func (u HumanUI) Loss(text string) string {
	if !u.Styled {
		return text
	}
	return u.style().Foreground(lipgloss.Color("196")).Render(text)
}

// Accent is used for column emphasis (yields, totals).
func (u HumanUI) Accent(text string) string {
	if !u.Styled {
		return text
	}
	return u.style().Foreground(lipgloss.AdaptiveColor{Light: "63", Dark: "111"}).Bold(true).Render(text)
}

// PrintError writes a styled error line to w.
func PrintError(w io.Writer, err error) {
	if err == nil {
		return
	}
	ui := NewHumanUI(w)
	prefix := "error:"
	if ui.Styled {
		prefix = ui.style().Bold(true).Foreground(lipgloss.Color("196")).Render("error:")
	}
	_, _ = fmt.Fprintln(w, prefix, err.Error())
}

// PrintKVBlock prints a labeled key/value block, with the labels right-padded to align colons.
func PrintKVBlock(w io.Writer, title string, fields []KVField) {
	ui := NewHumanUI(w)
	if title != "" {
		_, _ = fmt.Fprintln(w, ui.Title(title))
	}
	maxLabel := 0
	for _, f := range fields {
		if len(f.Label) > maxLabel {
			maxLabel = len(f.Label)
		}
	}
	label := ui.style().Bold(true).Width(maxLabel + 1)
	if ui.Styled {
		label = label.Foreground(lipgloss.AdaptiveColor{Light: "63", Dark: "111"})
	}
	for _, f := range fields {
		_, _ = fmt.Fprintln(w, label.Render(f.Label+":")+" "+f.Value)
	}
}

// PrintTable renders a styled table to w.
func PrintTable(w io.Writer, headers []string, rows [][]string) {
	ui := NewHumanUI(w)
	t := table.New().
		Headers(headers...).
		Rows(rows...).
		BorderRow(false).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				s := ui.style().Bold(true)
				if ui.Styled {
					s = s.Foreground(lipgloss.AdaptiveColor{Light: "63", Dark: "111"})
				}
				return s
			}
			return ui.style()
		})
	if ui.Styled {
		t = t.BorderStyle(ui.style().Foreground(lipgloss.AdaptiveColor{Light: "249", Dark: "238"}))
	}
	_, _ = fmt.Fprintln(w, t.Render())
}

// PollLoop runs fn forever at `interval`, exiting cleanly on ctx cancellation.
func PollLoop(ctx context.Context, interval time.Duration, fn func() error) error {
	for {
		if err := fn(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// JoinNonEmpty returns parts joined by sep, dropping empty strings.
func JoinNonEmpty(parts []string, sep string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
