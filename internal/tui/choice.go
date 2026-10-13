package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ville6000/tfs/internal/ui"
)

var keySelect = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select"))

// Choice lets the user pick one value from a list.
type Choice struct {
	title    string
	values   []string
	table    Table
	onSelect func(string) tea.Cmd
}

// NewChoice creates a choice screen; current is preselected.
func NewChoice(title string, values []string, current string, onSelect func(string) tea.Cmd) *Choice {
	t := NewTable([]Column{{Title: "VALUE", Flex: true}})
	rows := make([]Row, len(values))
	for i, v := range values {
		st := ui.StateStyle(v)
		if strings.EqualFold(v, current) {
			v += "  (current)"
		}
		rows[i] = Row{Key: i, Cells: []string{v}, Styles: []lipgloss.Style{st}}
	}
	t.SetRows(rows)
	for i, v := range values {
		if strings.EqualFold(v, current) {
			t.cursor = i
		}
	}
	return &Choice{title: title, values: values, table: t, onSelect: onSelect}
}

func (c *Choice) Init() tea.Cmd       { return nil }
func (c *Choice) Title() string       { return c.title }
func (c *Choice) Capturing() bool     { return c.table.Filtering() }
func (c *Choice) SetSize(w, h int)    { c.table.SetSize(w, h) }
func (c *Choice) View() string        { return c.table.View() }
func (c *Choice) Help() []key.Binding { return append([]key.Binding{keySelect}, c.table.Help()[:3]...) }
func (c *Choice) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok && key.Matches(km, keySelect) && !c.table.Filtering() {
		if r, ok := c.table.Selected(); ok {
			return c, tea.Sequence(Pop(), c.onSelect(c.values[r.Key]))
		}
		return c, nil
	}
	var cmd tea.Cmd
	c.table, cmd = c.table.Update(msg)
	return c, cmd
}
