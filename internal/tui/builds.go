package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/ado"
	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

// buildRefresh is how often views of running builds refresh.
const buildRefresh = 10 * time.Second

var (
	keyNextFailure = key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "next failure"))
	keyLog         = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "log"))
)

// Builds is what the build screens share.
type Builds struct {
	Client *ado.Client
	Query  domain.BuildQuery
	Open   func(url string) error
	now    func() time.Time
}

func (b *Builds) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

func (b *Builds) open(url string) tea.Cmd {
	if err := b.Open(url); err != nil {
		return StatusErr(err)
	}
	return Status("opened " + url)
}

type buildTickMsg struct{ screen int64 }

func tick(screen int64) tea.Cmd {
	return tea.Tick(buildRefresh, func(time.Time) tea.Msg { return buildTickMsg{screen} })
}

// ---- list ----------------------------------------------------------------------------------

type buildsLoadedMsg struct {
	screen int64
	builds []domain.Build
	err    error
}

// BuildList lists recent builds; it refreshes while any of them is still running.
type BuildList struct {
	id      int64
	b       *Builds
	title   string
	builds  map[int]domain.Build
	table   Table
	ticking bool
}

// NewBuildList creates the build list screen.
func NewBuildList(b *Builds, title string) *BuildList {
	t := NewTable([]Column{{Title: "ID", Max: 8}, {Title: "PIPELINE", Max: 28}, {Title: "BRANCH", Max: 30},
		{Title: "STATUS", Max: 22}, {Title: "BY", Max: 20}, {Title: "STARTED", Max: 16}, {Title: "TIME", Flex: true}})
	t.SetEmptyText("Loading…")
	return &BuildList{id: nextID(), b: b, title: title, table: t, builds: map[int]domain.Build{}}
}

func (l *BuildList) reload() tea.Cmd {
	return Busy("loading builds", func() tea.Msg {
		bs, err := l.b.Client.ListBuilds(context.Background(), l.b.Query)
		return buildsLoadedMsg{screen: l.id, builds: bs, err: err}
	})
}

func (l *BuildList) Init() tea.Cmd    { return l.reload() }
func (l *BuildList) Title() string    { return l.title }
func (l *BuildList) Capturing() bool  { return l.table.Filtering() }
func (l *BuildList) SetSize(w, h int) { l.table.SetSize(w, h) }
func (l *BuildList) View() string     { return l.table.View() }
func (l *BuildList) Help() []key.Binding {
	return append([]key.Binding{keyEnter, keyOpen, keyCopy, keyRefresh}, l.table.Help()...)
}

func buildRow(b domain.Build, now time.Time) Row {
	state := b.State()
	started := b.Started
	if started.IsZero() {
		started = b.Queued
	}
	return Row{
		Key:    b.ID,
		Cells:  []string{strconv.Itoa(b.ID), b.Pipeline, b.Branch, ui.BuildLabel(state), ui.OrDash(b.RequestedFor), ui.FormatTime(started), ui.FormatDuration(b.Duration(now))},
		Styles: []lipgloss.Style{ui.ID, ui.Accent, {}, {}, ui.Muted, ui.Muted, ui.Muted},
		Search: fmt.Sprintf("%d %s %s %s %s %s", b.ID, b.Number, b.Pipeline, b.Branch, b.RequestedFor, state),
	}
}

func (l *BuildList) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case buildsLoadedMsg:
		if msg.screen != l.id {
			return l, nil
		}
		if msg.err != nil {
			l.table.SetEmptyText("Failed to load.")
			return l, StatusErr(msg.err)
		}
		l.table.SetEmptyText("No builds match.")
		now := l.b.clock()
		l.builds = map[int]domain.Build{}
		rows := make([]Row, len(msg.builds))
		running := false
		for i, b := range msg.builds {
			l.builds[b.ID] = b
			rows[i] = buildRow(b, now)
			running = running || !b.State().Done()
		}
		l.table.SetRows(rows)
		if running && !l.ticking {
			l.ticking = true
			return l, tick(l.id)
		}
		return l, nil
	case buildTickMsg:
		if msg.screen != l.id {
			return l, nil
		}
		l.ticking = false
		return l, l.reload()
	case tea.KeyPressMsg:
		if l.table.Filtering() {
			break
		}
		if key.Matches(msg, keyRefresh) {
			return l, l.reload()
		}
		row, ok := l.table.Selected()
		if !ok {
			break
		}
		b := l.builds[row.Key]
		switch {
		case key.Matches(msg, keyEnter):
			return l, Push(NewBuildView(l.b, b.ID))
		case key.Matches(msg, keyOpen):
			return l, l.b.open(b.URL)
		case key.Matches(msg, keyCopy):
			return l, copyURL(b.URL)
		}
	}
	var cmd tea.Cmd
	l.table, cmd = l.table.Update(msg)
	return l, cmd
}

// ---- one build -----------------------------------------------------------------------------

type buildLoadedMsg struct {
	screen   int64
	build    domain.Build
	timeline []domain.TimelineRecord
	err      error
}

const issueLines = 4

// BuildView shows a build's stages, jobs and tasks; enter opens a task's log.
type BuildView struct {
	sid     int64
	b       *Builds
	id      int
	build   *domain.Build
	rows    []domain.TimelineRow
	table   Table
	width   int
	height  int
	ticking bool
	focused bool // moved to the first failure once
}

// NewBuildView creates the screen for one build.
func NewBuildView(b *Builds, id int) *BuildView {
	t := NewTable([]Column{{Title: "STEP", Max: 60}, {Title: "STATUS", Max: 22}, {Title: "TIME", Max: 8}, {Title: "ISSUES", Flex: true}})
	t.SetEmptyText("Loading…")
	return &BuildView{sid: nextID(), b: b, id: id, table: t}
}

func (v *BuildView) reload() tea.Cmd {
	return Busy(fmt.Sprintf("loading build %d", v.id), func() tea.Msg {
		ctx := context.Background()
		build, err := v.b.Client.GetBuild(ctx, v.b.Query.Project, v.id)
		if err != nil {
			return buildLoadedMsg{screen: v.sid, err: err}
		}
		tl, err := v.b.Client.BuildTimeline(ctx, v.b.Query.Project, v.id)
		return buildLoadedMsg{screen: v.sid, build: build, timeline: tl, err: err}
	})
}

func (v *BuildView) header() string {
	if v.build == nil {
		return ""
	}
	b := v.build
	commit := b.Commit
	if len(commit) > 8 {
		commit = commit[:8]
	}
	src := []string{b.Branch}
	if commit != "" {
		src = append(src, "@ "+commit)
	}
	meta := []string{strings.Join(src, " ")}
	if b.Reason != "" {
		meta = append(meta, b.Reason)
	}
	if b.RequestedFor != "" {
		meta = append(meta, b.RequestedFor)
	}
	state := ui.BuildLabel(b.State())
	if d := ui.FormatDuration(b.Duration(v.b.clock())); d != "" {
		state += ui.Muted.Render("  ·  " + d)
	}
	return strings.Join([]string{
		ui.ID.Render(fmt.Sprintf("#%d", b.ID)) + "  " + ui.Accent.Render(b.Pipeline) + "  " + ui.Bold.Render(b.Number),
		strings.Join(meta, ui.Muted.Render("  ·  ")),
		state,
		ui.Muted.Render(b.URL),
		"",
	}, "\n")
}

func (v *BuildView) layout() {
	headerLines := strings.Count(v.header(), "\n") + 1
	v.table.SetSize(v.width, max(3, v.height-headerLines-issueLines))
}

func (v *BuildView) issues() string {
	r, ok := v.table.Selected()
	if !ok || r.Key >= len(v.rows) {
		return strings.Repeat("\n", issueLines-1)
	}
	rec := v.rows[r.Key].Record
	var lines []string
	for _, is := range rec.Issues {
		style := ui.Warn
		if strings.EqualFold(is.Type, "error") {
			style = ui.Danger
		}
		lines = append(lines, style.Render(ansi.Truncate(strings.ReplaceAll(is.Message, "\n", " "), max(10, v.width-2), "…")))
		if len(lines) == issueLines-1 {
			break
		}
	}
	for len(lines) < issueLines-1 {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (v *BuildView) Init() tea.Cmd    { return v.reload() }
func (v *BuildView) Capturing() bool  { return v.table.Filtering() }
func (v *BuildView) SetSize(w, h int) { v.width, v.height = w, h; v.layout() }
func (v *BuildView) View() string {
	return v.header() + "\n" + v.table.View() + "\n" + v.issues()
}
func (v *BuildView) Title() string {
	if v.build != nil {
		return v.build.Pipeline + " " + v.build.Number
	}
	return fmt.Sprintf("build %d", v.id)
}
func (v *BuildView) Help() []key.Binding {
	return append([]key.Binding{keyLog, keyNextFailure, keyOpen, keyCopy, keyRefresh}, v.table.Help()[:3]...)
}

func issuesSummary(r domain.TimelineRecord) string {
	var parts []string
	if r.Errors > 0 {
		parts = append(parts, ui.Danger.Render(fmt.Sprintf("%d error%s", r.Errors, plural(r.Errors))))
	}
	if r.Warnings > 0 {
		parts = append(parts, ui.Warn.Render(fmt.Sprintf("%d warning%s", r.Warnings, plural(r.Warnings))))
	}
	return strings.Join(parts, ", ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (v *BuildView) nextFailure() {
	cur, _ := v.table.Selected()
	n := len(v.rows)
	for step := 1; step <= n; step++ {
		i := (cur.Key + step) % n
		r := v.rows[i].Record
		if strings.EqualFold(r.Type, "Task") && r.Status() == domain.BuildFailed {
			v.table.cursor = i
			v.table.clamp()
			return
		}
	}
}

func (v *BuildView) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case buildLoadedMsg:
		if msg.screen != v.sid {
			return v, nil
		}
		if msg.err != nil {
			v.table.SetEmptyText("Failed to load.")
			return v, StatusErr(msg.err)
		}
		v.build = &msg.build
		v.rows = domain.TimelineOutline(msg.timeline)
		now := v.b.clock()
		rows := make([]Row, len(v.rows))
		for i, tr := range v.rows {
			r := tr.Record
			name := strings.Repeat("  ", tr.Depth) + r.Name
			style := lipgloss.NewStyle()
			if tr.Depth == 0 {
				style = ui.Bold
			}
			rows[i] = Row{
				Key:    i,
				Cells:  []string{name, ui.BuildLabel(r.Status()), ui.FormatDuration(r.Duration(now)), issuesSummary(r)},
				Styles: []lipgloss.Style{style, {}, ui.Muted, {}},
				Search: r.Name + " " + r.Status().String(),
			}
		}
		v.table.SetEmptyText("No steps yet.")
		v.table.SetRows(rows)
		v.layout()
		if !v.focused && v.build.State() == domain.BuildFailed {
			v.focused = true
			v.table.cursor = len(v.rows) - 1 // start before the first row so the search wraps to it
			v.nextFailure()
		}
		if !v.build.State().Done() && !v.ticking {
			v.ticking = true
			return v, tick(v.sid)
		}
		return v, nil
	case buildTickMsg:
		if msg.screen != v.sid {
			return v, nil
		}
		v.ticking = false
		return v, v.reload()
	case tea.KeyPressMsg:
		if v.table.Filtering() || v.build == nil {
			break
		}
		switch {
		case key.Matches(msg, keyRefresh):
			return v, v.reload()
		case key.Matches(msg, keyOpen):
			return v, v.b.open(v.build.URL)
		case key.Matches(msg, keyCopy):
			return v, copyURL(v.build.URL)
		case key.Matches(msg, keyNextFailure):
			v.nextFailure()
			return v, nil
		case key.Matches(msg, keyLog):
			row, ok := v.table.Selected()
			if !ok {
				return v, nil
			}
			rec := v.rows[row.Key].Record
			if rec.LogID == 0 {
				return v, Status(rec.Name + " has no log")
			}
			return v, Push(NewLogView(v.b, *v.build, rec))
		}
	}
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return v, cmd
}

// ---- log -----------------------------------------------------------------------------------

type logLoadedMsg struct {
	screen int64
	text   string
	err    error
}

// LogView shows a task's log, starting at the end where failures usually are.
type LogView struct {
	sid    int64
	b      *Builds
	build  domain.Build
	record domain.TimelineRecord
	vp     viewport.Model
	loaded bool
}

// NewLogView creates the log screen for one timeline record.
func NewLogView(b *Builds, build domain.Build, rec domain.TimelineRecord) *LogView {
	vp := viewport.New()
	vp.SoftWrap = true
	return &LogView{sid: nextID(), b: b, build: build, record: rec, vp: vp}
}

func (l *LogView) reload() tea.Cmd {
	return Busy("loading log", func() tea.Msg {
		text, err := l.b.Client.BuildLog(context.Background(), l.b.Query.Project, l.build.ID, l.record.LogID)
		return logLoadedMsg{screen: l.sid, text: text, err: err}
	})
}

func (l *LogView) Init() tea.Cmd {
	l.vp.SetContent(ui.Muted.Render("Loading…"))
	return l.reload()
}
func (l *LogView) Title() string    { return l.record.Name }
func (l *LogView) Capturing() bool  { return false }
func (l *LogView) View() string     { return l.vp.View() }
func (l *LogView) SetSize(w, h int) { l.vp.SetWidth(w); l.vp.SetHeight(h) }
func (l *LogView) Help() []key.Binding {
	return []key.Binding{keyUp, keyDown, keyPgUp, keyPgDown, keyTop, keyBottom, keyOpen, keyRefresh}
}

func (l *LogView) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case logLoadedMsg:
		if msg.screen != l.sid {
			return l, nil
		}
		if msg.err != nil {
			l.vp.SetContent(ui.Danger.Render(msg.err.Error()))
			return l, StatusErr(msg.err)
		}
		atBottom := !l.loaded || l.vp.AtBottom()
		l.loaded = true
		l.vp.SetContent(ui.FormatLog(msg.text, true))
		if atBottom {
			l.vp.GotoBottom()
		}
		return l, nil
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, keyTop):
			l.vp.GotoTop()
			return l, nil
		case key.Matches(msg, keyBottom):
			l.vp.GotoBottom()
			return l, nil
		case key.Matches(msg, keyOpen):
			return l, l.b.open(l.build.URL)
		case key.Matches(msg, keyRefresh):
			return l, l.reload()
		}
	}
	var cmd tea.Cmd
	l.vp, cmd = l.vp.Update(msg)
	return l, cmd
}
