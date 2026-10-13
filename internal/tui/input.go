package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/ville6000/tfs/internal/ui"
)

var (
	keySubmit = key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "submit"))
	keyCancel = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
)

// Input is a multi-line text entry screen.
type Input struct {
	title    string
	area     textarea.Model
	onSubmit func(string) tea.Cmd
}

// NewInput creates an input screen.
func NewInput(title string, onSubmit func(string) tea.Cmd) *Input {
	ta := textarea.New()
	ta.Placeholder = "Write here…"
	ta.ShowLineNumbers = false
	ta.SetStyles(textarea.DefaultStyles(ui.IsDark()))
	return &Input{title: title, area: ta, onSubmit: onSubmit}
}

func (i *Input) Init() tea.Cmd       { return i.area.Focus() }
func (i *Input) Title() string       { return i.title }
func (i *Input) Capturing() bool     { return true }
func (i *Input) Help() []key.Binding { return []key.Binding{keySubmit, keyCancel} }
func (i *Input) View() string        { return i.area.View() }
func (i *Input) SetSize(w, h int) {
	i.area.SetWidth(max(10, w-2))
	i.area.SetHeight(max(3, h-1))
}

func (i *Input) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(km, keyCancel):
			return i, Pop()
		case key.Matches(km, keySubmit):
			text := strings.TrimSpace(i.area.Value())
			if text == "" {
				return i, Status("nothing to submit")
			}
			return i, tea.Sequence(Pop(), i.onSubmit(text))
		}
	}
	var cmd tea.Cmd
	i.area, cmd = i.area.Update(msg)
	return i, cmd
}
