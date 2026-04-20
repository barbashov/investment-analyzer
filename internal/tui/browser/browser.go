// Package browser is a shared list-with-detail Bubbletea framework used by
// `invest tx list` and `invest dividends payouts`. Each consumer supplies typed Rows;
// the framework handles filtering, sorting, scrolling, and the detail panel.
package browser

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"investment-analyzer/internal/ui"
)

// Row is the contract a browser entry must satisfy.
type Row interface {
	// Cells returns the column values for the table row, in the order matching Headers.
	Cells() []string
	// Detail returns the key/value pairs shown in the detail panel for the focused row.
	Detail() []ui.KVField
	// Match returns true if the row should appear under the given filter tokens.
	// Tokens map filter keys (e.g. "ticker") to values; the special key "" holds free text.
	Match(tokens map[string]string) bool
}

// SortMode names a sort order plus the comparator that implements it.
type SortMode struct {
	Label string
	Less  func(a, b Row) bool
}

// KeyAction is invoked for keys not handled by the framework. Return (cmd, true) to consume the key.
// `current` is the focused row at the moment of the keystroke (may be nil if the table is empty).
type KeyAction func(msg tea.KeyMsg, current Row) (tea.Cmd, bool)

// FooterFn computes the right-side footer string from the currently visible rows.
// Useful for showing per-filter totals (e.g. sum of NET for visible dividends).
type FooterFn func(visible []Row) string

// RowStyleFn returns a per-row style override. Called for every non-cursor
// row during View. Return the zero lipgloss.Style to opt out (the default
// style is then applied). Cursor highlight always takes precedence so the
// focused row stays readable.
type RowStyleFn func(Row) lipgloss.Style

// Model is the bubbletea model. Construct with New and run via tea.NewProgram(m).
type Model struct {
	Title      string
	Headers    []string
	Rows       []Row // immutable input
	OnKey      KeyAction
	Footer     FooterFn
	RowStyle   RowStyleFn // optional per-row style (e.g. to dim projected rows)
	StartHelp  string // optional one-line help shown until the user types
	FilterHelp string // optional one-line legend shown under the filter input when in filter mode

	view       []Row
	cursor     int
	offset     int
	filter     textinput.Model
	filterMode bool
	tokens     map[string]string
	sorts      []SortMode
	sortIdx    int
	width      int
	height     int
	flash      string
	quit       bool
}

// New returns an initial Model. `sorts` must contain at least one mode.
func New(title string, headers []string, rows []Row, sorts []SortMode) *Model {
	if len(sorts) == 0 {
		sorts = []SortMode{{Label: "as-given", Less: func(a, b Row) bool { return false }}}
	}
	ti := textinput.New()
	ti.Placeholder = "filter (e.g. ticker:SBER from:2024-01-01)"
	ti.Prompt = "» "
	ti.CharLimit = 200
	m := &Model{
		Title:   title,
		Headers: headers,
		Rows:    rows,
		filter:  ti,
		sorts:   sorts,
		tokens:  map[string]string{},
	}
	m.recompute()
	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return textinput.Blink }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.filterMode {
			switch msg.String() {
			case "esc":
				m.filterMode = false
				m.filter.Blur()
				return m, nil
			case "enter":
				m.tokens = parseFilter(m.filter.Value())
				m.filterMode = false
				m.filter.Blur()
				m.recompute()
				return m, nil
			}
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			return m, cmd
		}

		// Custom key handler gets first shot.
		if m.OnKey != nil {
			if cmd, handled := m.OnKey(msg, m.current()); handled {
				return m, cmd
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.quit = true
			return m, tea.Quit
		case "j", "down":
			m.move(1)
		case "k", "up":
			m.move(-1)
		case "ctrl+d", "pgdown":
			m.move(m.tableHeight() / 2)
		case "ctrl+u", "pgup":
			m.move(-m.tableHeight() / 2)
		case "g", "home":
			m.cursor = 0
			m.offset = 0
		case "G", "end":
			m.cursor = max0(len(m.view) - 1)
			m.ensureVisible()
		case "/":
			m.filterMode = true
			m.filter.SetValue(rebuildFilter(m.tokens))
			m.filter.Focus()
			return m, textinput.Blink
		case "f":
			m.sortIdx = (m.sortIdx + 1) % len(m.sorts)
			m.flash = "sort: " + m.sorts[m.sortIdx].Label
			m.recompute()
		case "c":
			m.tokens = map[string]string{}
			m.filter.SetValue("")
			m.recompute()
		}
	}

	return m, nil
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.quit {
		return ""
	}
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "27", Dark: "117"})
	subtle := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "244", Dark: "245"})
	rowStyle := lipgloss.NewStyle()
	cursorStyle := lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "254", Dark: "237"}).Bold(true)

	var b strings.Builder

	// 1) Title bar
	footerRight := ""
	if m.Footer != nil {
		footerRight = m.Footer(m.view)
	}
	titleLine := fmt.Sprintf("%s — %d / %d", m.Title, len(m.view), len(m.Rows))
	if footerRight != "" {
		titleLine += "   " + footerRight
	}
	b.WriteString(headerStyle.Render(titleLine))
	b.WriteString("\n")

	// 2) Filter input or filter summary
	if m.filterMode {
		b.WriteString(m.filter.View())
		b.WriteString("\n")
		if m.FilterHelp != "" {
			b.WriteString(subtle.Render(m.FilterHelp))
			b.WriteString("\n")
		}
	} else {
		filterStr := rebuildFilter(m.tokens)
		if filterStr == "" {
			filterStr = "(no filter — press / to set, c to clear)"
		} else {
			filterStr = "filter: " + filterStr
		}
		b.WriteString(subtle.Render(filterStr))
		b.WriteString("\n")
	}

	// 3) Computed widths
	cols := computeWidths(m.Headers, m.view, m.width)

	// 4) Table header
	b.WriteString(headerStyle.Render(renderRow(m.Headers, cols)))
	b.WriteString("\n")

	// 5) Visible rows
	avail := m.tableHeight()
	for i := 0; i < avail; i++ {
		idx := m.offset + i
		if idx >= len(m.view) {
			b.WriteString("\n")
			continue
		}
		line := renderRow(m.view[idx].Cells(), cols)
		switch {
		case idx == m.cursor:
			b.WriteString(cursorStyle.Render(line))
		case m.RowStyle != nil:
			b.WriteString(m.RowStyle(m.view[idx]).Render(line))
		default:
			b.WriteString(rowStyle.Render(line))
		}
		b.WriteString("\n")
	}

	// 6) Detail panel for the focused row
	b.WriteString(strings.Repeat("─", min(m.width, 100)))
	b.WriteString("\n")
	if cur := m.current(); cur != nil {
		fields := cur.Detail()
		maxLabel := 0
		for _, f := range fields {
			if len(f.Label) > maxLabel {
				maxLabel = len(f.Label)
			}
		}
		labelStyle := headerStyle.Width(maxLabel + 1)
		for _, f := range fields {
			b.WriteString(labelStyle.Render(f.Label + ":"))
			b.WriteString(" ")
			b.WriteString(f.Value)
			b.WriteString("\n")
		}
	}

	// 7) Footer / help
	help := "j/k move  ctrl+d/u page  g/G top/end  /  filter  f sort  c clear  q quit"
	if m.flash != "" {
		help = m.flash + "    " + help
		m.flash = ""
	}
	b.WriteString(subtle.Render(help))

	return b.String()
}

// Quit reports whether the user has asked to exit.
func (m *Model) Quit() bool { return m.quit }

// Refresh re-applies filter+sort to a new row set (used after deletions).
func (m *Model) Refresh(rows []Row) {
	m.Rows = rows
	if m.cursor >= len(rows) {
		m.cursor = max0(len(rows) - 1)
	}
	m.recompute()
}

// Helpers ----------------------------------------------------------------------

func (m *Model) current() Row {
	if m.cursor < 0 || m.cursor >= len(m.view) {
		return nil
	}
	return m.view[m.cursor]
}

func (m *Model) move(delta int) {
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.view) {
		m.cursor = max0(len(m.view) - 1)
	}
	m.ensureVisible()
}

func (m *Model) ensureVisible() {
	avail := m.tableHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+avail {
		m.offset = m.cursor - avail + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// tableHeight reserves rows for title (1), filter (1), header (1), separator (1), detail (~6), help (1).
func (m *Model) tableHeight() int {
	overhead := 11
	h := m.height - overhead
	if h < 3 {
		h = 3
	}
	return h
}

func (m *Model) recompute() {
	m.view = m.view[:0]
	for _, r := range m.Rows {
		if r.Match(m.tokens) {
			m.view = append(m.view, r)
		}
	}
	if m.sortIdx < len(m.sorts) {
		less := m.sorts[m.sortIdx].Less
		sort.SliceStable(m.view, func(i, j int) bool { return less(m.view[i], m.view[j]) })
	}
	if m.cursor >= len(m.view) {
		m.cursor = max0(len(m.view) - 1)
	}
	m.ensureVisible()
}

// parseFilter splits "ticker:SBER op:buy free text" into tokens. The "" key holds free text.
func parseFilter(s string) map[string]string {
	out := map[string]string{}
	var freeParts []string
	for _, tok := range strings.Fields(s) {
		if i := strings.IndexByte(tok, ':'); i > 0 {
			k := strings.ToLower(tok[:i])
			v := tok[i+1:]
			out[k] = v
			continue
		}
		freeParts = append(freeParts, tok)
	}
	if len(freeParts) > 0 {
		out[""] = strings.Join(freeParts, " ")
	}
	return out
}

func rebuildFilter(tokens map[string]string) string {
	keys := make([]string, 0, len(tokens))
	for k := range tokens {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := tokens[k]
		if k == "" {
			parts = append(parts, v)
		} else {
			parts = append(parts, k+":"+v)
		}
	}
	return strings.Join(parts, " ")
}

func computeWidths(headers []string, rows []Row, totalWidth int) []int {
	w := make([]int, len(headers))
	for i, h := range headers {
		if len(h) > w[i] {
			w[i] = len(h)
		}
	}
	for _, r := range rows {
		for i, c := range r.Cells() {
			if i >= len(w) {
				break
			}
			if l := visibleLen(c); l > w[i] {
				w[i] = l
			}
		}
	}
	// Cap each column to keep things tidy if the total overflows.
	const gap = 2
	used := 0
	for _, x := range w {
		used += x + gap
	}
	if totalWidth > 0 && used > totalWidth {
		// Trim the longest column iteratively.
		for used > totalWidth {
			maxIdx := 0
			for i, x := range w {
				if x > w[maxIdx] {
					maxIdx = i
				}
			}
			if w[maxIdx] <= 6 {
				break
			}
			w[maxIdx]--
			used--
		}
	}
	return w
}

func renderRow(cells []string, widths []int) string {
	var b strings.Builder
	for i, w := range widths {
		var v string
		if i < len(cells) {
			v = cells[i]
		}
		v = padOrTrim(v, w)
		b.WriteString(v)
		if i < len(widths)-1 {
			b.WriteString("  ")
		}
	}
	return b.String()
}

func padOrTrim(s string, w int) string {
	l := visibleLen(s)
	if l == w {
		return s
	}
	if l > w {
		// crude rune-safe trim
		runes := []rune(s)
		if w >= 1 && len(runes) > w {
			return string(runes[:w-1]) + "…"
		}
		return string(runes[:w])
	}
	return s + strings.Repeat(" ", w-l)
}

// visibleLen counts runes (good enough for our domain — mostly ASCII + Cyrillic, no emoji).
func visibleLen(s string) int { return len([]rune(s)) }

func max0(a int) int {
	if a < 0 {
		return 0
	}
	return a
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
