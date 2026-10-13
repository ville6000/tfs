package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/ui"
)

// Column describes a table column. Flex columns share the remaining width.
type Column struct {
	Title string
	Max   int
	Flex  bool
}

// Row is a table row. Key identifies the row across reloads.
type Row struct {
	Key    int
	Cells  []string
	Styles []lipgloss.Style
}

func (r Row) text() string { return strings.ToLower(strings.Join(r.Cells, " ")) }

var (
	keyUp     = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up"))
	keyDown   = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down"))
	keyPgUp   = key.NewBinding(key.WithKeys("pgup", "ctrl+u"), key.WithHelp("pgup", "page up"))
	keyPgDown = key.NewBinding(key.WithKeys("pgdown", "ctrl+d"), key.WithHelp("pgdn", "page down"))
	keyTop    = key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("g", "top"))
	keyBottom = key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("G", "bottom"))
	keyFilter = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter"))
)

// Table is a scrollable, filterable selection table.
type Table struct {
	cols      []Column
	all       []Row
	rows      []Row
	cursor    int
	offset    int
	width     int
	height    int
	filter    textinput.Model
	filtering bool
	empty     string
}

// NewTable creates a table with the given columns.
func NewTable(cols []Column) Table {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "type to filter"
	return Table{cols: cols, filter: ti, empty: "Nothing to show."}
}

// SetEmptyText sets the message shown when there are no rows.
func (t *Table) SetEmptyText(s string) { t.empty = s }

// SetRows replaces the rows, keeping the selection on the same key when possible.
func (t *Table) SetRows(rows []Row) {
	sel, ok := t.Selected()
	t.all = rows
	t.applyFilter()
	if ok {
		for i, r := range t.rows {
			if r.Key == sel.Key {
				t.cursor = i
			}
		}
	}
	t.clamp()
}

// UpdateRow replaces the row with the same key.
func (t *Table) UpdateRow(row Row) {
	for i := range t.all {
		if t.all[i].Key == row.Key {
			t.all[i] = row
		}
	}
	for i := range t.rows {
		if t.rows[i].Key == row.Key {
			t.rows[i] = row
		}
	}
}

// Selected returns the row under the cursor.
func (t Table) Selected() (Row, bool) {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return Row{}, false
	}
	return t.rows[t.cursor], true
}

// SetSize sets the space the table may use, including its footer line.
func (t *Table) SetSize(w, h int) {
	t.width, t.height = w, h
	t.filter.SetWidth(max(10, w-20))
	t.clamp()
}

// Filtering reports whether the filter input has focus.
func (t Table) Filtering() bool { return t.filtering }

// Help lists the table's own key bindings.
func (t Table) Help() []key.Binding {
	return []key.Binding{keyUp, keyDown, keyFilter, keyPgUp, keyPgDown, keyTop, keyBottom}
}

func (t *Table) applyFilter() {
	terms := strings.Fields(strings.ToLower(t.filter.Value()))
	t.rows = t.rows[:0:0]
	for _, r := range t.all {
		text := r.text()
		ok := true
		for _, term := range terms {
			if !strings.Contains(text, term) {
				ok = false
				break
			}
		}
		if ok {
			t.rows = append(t.rows, r)
		}
	}
	t.clamp()
}

func (t *Table) visibleRows() int { return max(1, t.height-2) } // header + footer

func (t *Table) clamp() {
	t.cursor = max(0, min(t.cursor, len(t.rows)-1))
	vis := t.visibleRows()
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+vis {
		t.offset = t.cursor - vis + 1
	}
	t.offset = max(0, min(t.offset, len(t.rows)-vis))
}

// Update handles navigation and filtering keys.
func (t Table) Update(msg tea.Msg) (Table, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return t, nil
	}
	if t.filtering {
		switch km.String() {
		case "enter", "tab":
			t.filtering = false
			t.filter.Blur()
			return t, nil
		case "esc":
			t.filtering = false
			t.filter.Blur()
			t.filter.SetValue("")
			t.applyFilter()
			return t, nil
		case "up", "down":
			// allow moving while typing
		default:
			var cmd tea.Cmd
			t.filter, cmd = t.filter.Update(msg)
			t.applyFilter()
			return t, cmd
		}
	}
	switch {
	case key.Matches(km, keyUp):
		t.cursor--
	case key.Matches(km, keyDown):
		t.cursor++
	case key.Matches(km, keyPgUp):
		t.cursor -= t.visibleRows()
	case key.Matches(km, keyPgDown):
		t.cursor += t.visibleRows()
	case key.Matches(km, keyTop):
		t.cursor = 0
	case key.Matches(km, keyBottom):
		t.cursor = len(t.rows) - 1
	case key.Matches(km, keyFilter):
		t.filtering = true
		return t, t.filter.Focus()
	case km.String() == "esc" && t.filter.Value() != "":
		t.filter.SetValue("")
		t.applyFilter()
	}
	t.clamp()
	return t, nil
}

const colGap = 2

func (t Table) widths() []int {
	w := make([]int, len(t.cols))
	flex := 0
	used := 2 // cursor marker
	for i, c := range t.cols {
		if c.Flex {
			flex++
			continue
		}
		w[i] = ansi.StringWidth(c.Title)
		for _, r := range t.all {
			w[i] = max(w[i], ansi.StringWidth(r.Cells[i]))
		}
		if c.Max > 0 {
			w[i] = min(w[i], c.Max)
		}
		used += w[i] + colGap
	}
	if flex > 0 {
		each := max(10, (t.width-used-colGap*(flex-1))/flex)
		for i, c := range t.cols {
			if c.Flex {
				w[i] = each
			}
		}
	}
	return w
}

func (t Table) renderLine(cells []string, styles []lipgloss.Style, widths []int, selected bool) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		text := ansi.Truncate(c, widths[i], "…")
		text += strings.Repeat(" ", max(0, widths[i]-ansi.StringWidth(text)))
		st := lipgloss.NewStyle()
		if i < len(styles) {
			st = styles[i]
		}
		if selected {
			st = st.Bold(true)
		}
		parts[i] = st.Render(text)
	}
	marker := "  "
	if selected {
		marker = ui.Accent.Render("▌ ")
	}
	return marker + strings.Join(parts, strings.Repeat(" ", colGap))
}

// View renders the table.
func (t Table) View() string {
	widths := t.widths()
	var b strings.Builder
	titles := make([]string, len(t.cols))
	hs := make([]lipgloss.Style, len(t.cols))
	for i, c := range t.cols {
		titles[i] = c.Title
		hs[i] = ui.Muted.Bold(true)
	}
	b.WriteString(t.renderLine(titles, hs, widths, false) + "\n")

	vis := t.visibleRows()
	if len(t.rows) == 0 {
		b.WriteString(ui.Muted.Render("  "+t.empty) + "\n")
		vis--
	}
	end := min(len(t.rows), t.offset+vis)
	for i := t.offset; i < end; i++ {
		r := t.rows[i]
		b.WriteString(t.renderLine(r.Cells, r.Styles, widths, i == t.cursor) + "\n")
	}
	for i := end - t.offset; i < vis; i++ {
		b.WriteString("\n")
	}

	count := fmt.Sprintf("%d/%d", min(t.cursor+1, len(t.rows)), len(t.rows))
	if len(t.rows) != len(t.all) {
		count += fmt.Sprintf(" (of %d)", len(t.all))
	}
	footer := ui.Muted.Render(count)
	if t.filtering || t.filter.Value() != "" {
		footer = t.filter.View() + "  " + footer
	}
	b.WriteString(footer)
	return b.String()
}
