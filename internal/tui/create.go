package tui

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

// CreateChild is a form for creating a work item under a parent.
type CreateChild struct {
	parent     domain.WorkItem
	form       *huh.Form
	typ, title string
	assignToMe bool
}

// NewCreateChild creates the form; types are offered with the first one preselected.
func NewCreateChild(act *Actions, parent domain.WorkItem, types []string) *CreateChild {
	c := &CreateChild{parent: parent}
	if len(types) > 0 {
		c.typ = types[0]
	}
	c.form = huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Type").Options(huh.NewOptions(types...)...).Value(&c.typ),
		huh.NewInput().Title("Title").Value(&c.title).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("title is required")
			}
			return nil
		}),
		huh.NewConfirm().Title("Assign to me?").Value(&c.assignToMe),
	)).WithTheme(huh.ThemeFunc(huh.ThemeCharm)).WithShowHelp(true)
	// Read the values when the form completes, not when it is built.
	c.form.SubmitCmd = func() tea.Msg {
		return CmdMsg{tea.Sequence(Pop(), act.createChild(parent, c.typ, strings.TrimSpace(c.title), c.assignToMe))}
	}
	c.form.CancelCmd = Pop()
	return c
}

func (c *CreateChild) Init() tea.Cmd       { return c.form.Init() }
func (c *CreateChild) Title() string       { return fmt.Sprintf("New child of #%d", c.parent.ID) }
func (c *CreateChild) Capturing() bool     { return true }
func (c *CreateChild) Help() []key.Binding { return []key.Binding{keyCancel} }

func (c *CreateChild) SetSize(w, h int) {
	c.form = c.form.WithWidth(max(20, w-2)).WithHeight(max(5, h-2))
}

func (c *CreateChild) View() string {
	header := fmt.Sprintf("Parent: %s %s %s\n\n", ui.ID.Render(fmt.Sprintf("#%d", c.parent.ID)),
		ui.TypeStyle(c.parent.Type).Render(c.parent.Type), c.parent.Title)
	return header + c.form.View()
}

func (c *CreateChild) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok && key.Matches(km, keyCancel) {
		return c, Pop()
	}
	m, cmd := c.form.Update(msg)
	if f, ok := m.(*huh.Form); ok {
		c.form = f
	}
	return c, cmd
}
