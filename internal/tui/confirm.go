package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/ville6000/tfs/internal/ui"
)

// Confirm asks before an action that changes something on the server, with an optional comment
// unless made by NewQuestion. It defaults to "no", so pressing enter through it does nothing;
// ctrl+s confirms from any field and esc cancels.
type Confirm struct {
	title  string
	form   *huh.Form
	ok     bool
	note   string
	detail string
	yes    key.Binding
	run    func(comment string) tea.Cmd
}

// NewConfirm creates a confirmation screen; run is called with the comment when confirmed.
func NewConfirm(title, question, detail, yes string, run func(comment string) tea.Cmd) *Confirm {
	return newConfirm(title, question, detail, yes, true, run)
}

// NewQuestion creates a confirmation screen without a comment; run is called when confirmed.
func NewQuestion(title, question, detail, yes string, run func() tea.Cmd) *Confirm {
	return newConfirm(title, question, detail, yes, false, func(string) tea.Cmd { return run() })
}

func newConfirm(title, question, detail, yes string, withComment bool, run func(comment string) tea.Cmd) *Confirm {
	c := &Confirm{title: title, detail: detail, run: run,
		yes: key.NewBinding(key.WithKeys(keySubmit.Keys()...), key.WithHelp(keySubmit.Help().Key, strings.ToLower(yes)))}
	var fields []huh.Field
	if withComment {
		fields = append(fields, huh.NewInput().Title("Comment").Description("Optional").Value(&c.note))
	}
	fields = append(fields, huh.NewConfirm().Title(question).Affirmative(yes).Negative("Cancel").Value(&c.ok))
	c.form = huh.NewForm(huh.NewGroup(fields...)).WithTheme(huh.ThemeFunc(huh.ThemeCharm)).WithShowHelp(true)
	c.form.SubmitCmd = func() tea.Msg {
		if !c.ok {
			return CmdMsg{tea.Sequence(Pop(), Status("cancelled"))}
		}
		return CmdMsg{tea.Sequence(Pop(), run(c.note))}
	}
	c.form.CancelCmd = Pop()
	return c
}

func (c *Confirm) Init() tea.Cmd       { return c.form.Init() }
func (c *Confirm) Title() string       { return c.title }
func (c *Confirm) Capturing() bool     { return true }
func (c *Confirm) Help() []key.Binding { return []key.Binding{c.yes, keyCancel} }
func (c *Confirm) SetSize(w, h int) {
	c.form = c.form.WithWidth(max(20, w-2)).WithHeight(max(5, h-3))
}
func (c *Confirm) View() string {
	return ui.Bold.Render(c.detail) + "\n\n" + c.form.View()
}

func (c *Confirm) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(km, keyCancel):
			return c, tea.Sequence(Pop(), Status("cancelled"))
		case key.Matches(km, c.yes):
			return c, tea.Sequence(Pop(), c.run(c.note))
		}
	}
	m, cmd := c.form.Update(msg)
	if f, ok := m.(*huh.Form); ok {
		c.form = f
	}
	return c, cmd
}
