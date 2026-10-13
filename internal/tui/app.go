// Package tui implements the interactive Bubble Tea views: work item and pull request lists,
// the hierarchy tree, detail views, pickers and forms.
package tui

import (
	"strings"
	"sync/atomic"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ville6000/tfs/internal/ui"
)

// Screen is one view on the App's navigation stack.
type Screen interface {
	Init() tea.Cmd
	Update(tea.Msg) (Screen, tea.Cmd)
	View() string
	SetSize(width, height int)
	Title() string
	Help() []key.Binding
	// Capturing reports that the screen is taking text input, so q/esc/? must reach it.
	Capturing() bool
}

// PushMsg pushes a screen.
type PushMsg struct{ Screen Screen }

// PopMsg pops the top screen.
type PopMsg struct{}

// StatusMsg sets the status line.
type StatusMsg struct {
	Text string
	Err  bool
}

// CmdMsg asks the App to run a command; background work uses it to trigger follow-up actions.
type CmdMsg struct{ Cmd tea.Cmd }

type busyStartMsg struct{ label string }
type busyDoneMsg struct{ msg tea.Msg }

// Push returns a command that pushes s.
func Push(s Screen) tea.Cmd { return func() tea.Msg { return PushMsg{s} } }

// Pop returns a command that pops the top screen.
func Pop() tea.Cmd { return func() tea.Msg { return PopMsg{} } }

// Status returns a command that shows an informational status.
func Status(text string) tea.Cmd { return func() tea.Msg { return StatusMsg{Text: text} } }

// StatusErr returns a command that shows an error status.
func StatusErr(err error) tea.Cmd {
	return func() tea.Msg { return StatusMsg{Text: err.Error(), Err: true} }
}

// Busy runs fn in the background while showing a spinner with label.
func Busy(label string, fn func() tea.Msg) tea.Cmd {
	return tea.Sequence(
		func() tea.Msg { return busyStartMsg{label} },
		func() tea.Msg { return busyDoneMsg{fn()} },
	)
}

var screenIDs atomic.Int64

func nextID() int64 { return screenIDs.Add(1) }

var (
	keyQuit = key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit"))
	keyBack = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))
	keyHelp = key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "more keys"))
	// Shown instead of q while a screen takes text input.
	keyForceQuit = key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit"))
)

// App hosts a stack of screens.
type App struct {
	stack     []Screen
	width     int
	height    int
	status    string
	statusErr bool
	busy      []string
	spinner   spinner.Model
	help      help.Model
	fullHelp  bool
}

// NewApp creates an app with root as its first screen.
func NewApp(root Screen) *App {
	h := help.New()
	h.Styles = helpStyles()
	return &App{
		stack:   []Screen{root},
		spinner: spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(ui.Accent)),
		help:    h,
	}
}

// Run starts the program with root as the first screen.
func Run(root Screen) error {
	_, err := tea.NewProgram(NewApp(root)).Run()
	return err
}

func (a *App) top() Screen { return a.stack[len(a.stack)-1] }

// Init implements tea.Model.
func (a *App) Init() tea.Cmd { return a.top().Init() }

const chromeLines = 3 // title line, status line, help line

func (a *App) resize() {
	for _, s := range a.stack {
		s.SetSize(a.width, max(1, a.height-chromeLines))
	}
}

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.help.SetWidth(msg.Width)
		a.resize()
		return a, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
		top := a.top()
		if !top.Capturing() {
			switch {
			case key.Matches(msg, keyQuit):
				return a, tea.Quit
			case key.Matches(msg, keyHelp):
				a.fullHelp = !a.fullHelp
				return a, nil
			case key.Matches(msg, keyBack) && len(a.stack) > 1:
				return a.pop()
			}
		}
		a.status = ""
		s, cmd := top.Update(msg)
		a.stack[len(a.stack)-1] = s
		return a, cmd
	case PushMsg:
		msg.Screen.SetSize(a.width, max(1, a.height-chromeLines))
		a.stack = append(a.stack, msg.Screen)
		a.fullHelp = false
		return a, msg.Screen.Init()
	case PopMsg:
		return a.pop()
	case StatusMsg:
		a.status, a.statusErr = msg.Text, msg.Err
		return a, nil
	case CmdMsg:
		return a, msg.Cmd
	case busyStartMsg:
		a.busy = append(a.busy, msg.label)
		if len(a.busy) == 1 {
			return a, a.spinner.Tick
		}
		return a, nil
	case busyDoneMsg:
		if len(a.busy) > 0 {
			a.busy = a.busy[1:]
		}
		return a.Update(msg.msg)
	case spinner.TickMsg:
		if len(a.busy) == 0 {
			return a, nil
		}
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		return a, cmd
	case nil:
		return a, nil
	}
	// Everything else (async results) is broadcast; screens ignore messages not meant for them.
	var cmds []tea.Cmd
	for i, s := range a.stack {
		var cmd tea.Cmd
		a.stack[i], cmd = s.Update(msg)
		cmds = append(cmds, cmd)
	}
	return a, tea.Batch(cmds...)
}

func (a *App) pop() (tea.Model, tea.Cmd) {
	if len(a.stack) == 1 {
		return a, tea.Quit
	}
	a.stack = a.stack[:len(a.stack)-1]
	a.fullHelp = false
	return a, nil
}

// helpStyles replaces the bubbles defaults (dim grays that are hard to read) with the terminal's
// own palette: bold cyan keys, normal-foreground descriptions, muted separators only.
func helpStyles() help.Styles {
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(ui.ColorInfo)
	descStyle := lipgloss.NewStyle()
	sepStyle := ui.Muted
	return help.Styles{
		ShortKey:       keyStyle,
		ShortDesc:      descStyle,
		ShortSeparator: sepStyle,
		Ellipsis:       sepStyle,
		FullKey:        keyStyle,
		FullDesc:       descStyle,
		FullSeparator:  sepStyle,
	}
}

type helpKeys struct {
	keys []key.Binding
}

func (h helpKeys) ShortHelp() []key.Binding { return h.keys }
func (h helpKeys) FullHelp() [][]key.Binding {
	var cols [][]key.Binding
	for i := 0; i < len(h.keys); i += 4 {
		cols = append(cols, h.keys[i:min(i+4, len(h.keys))])
	}
	return cols
}

// View implements tea.Model.
func (a *App) View() tea.View {
	top := a.top()
	var b strings.Builder

	crumbs := make([]string, len(a.stack))
	for i, s := range a.stack {
		crumbs[i] = s.Title()
	}
	title := ui.Header.Render(" tfs ") + " " + ui.Bold.Render(strings.Join(crumbs, ui.Muted.Render(" › ")))
	if len(a.busy) > 0 {
		title += "  " + a.spinner.View() + " " + ui.Muted.Render(a.busy[0]+"…")
	}
	b.WriteString(lipgloss.NewStyle().MaxWidth(a.width).Render(title) + "\n")

	body := top.View()
	bodyHeight := max(1, a.height-chromeLines)
	if a.fullHelp {
		bodyHeight = max(1, bodyHeight-4)
	}
	b.WriteString(lipgloss.NewStyle().Height(bodyHeight).MaxHeight(bodyHeight).MaxWidth(a.width).Render(body) + "\n")

	status := ""
	if a.status != "" {
		st := ui.OK
		if a.statusErr {
			st = ui.Danger
		}
		status = st.Render(a.status)
	}
	b.WriteString(lipgloss.NewStyle().MaxWidth(a.width).Render(status) + "\n")

	keys := append([]key.Binding{}, top.Help()...)
	if top.Capturing() {
		// q, ? and esc go to the input; the screen lists its own esc behaviour.
		keys = append(keys, keyForceQuit)
	} else {
		if len(a.stack) > 1 {
			keys = append(keys, keyBack)
		}
		keys = append(keys, keyHelp, keyQuit)
	}
	if a.fullHelp {
		b.WriteString(a.help.FullHelpView(helpKeys{keys}.FullHelp()))
	} else {
		b.WriteString(a.help.ShortHelpView(keys))
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}
