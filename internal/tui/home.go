package tui

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

var (
	keyNextSection = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next section"))
	keyPrevSection = key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "previous section"))
)

// HomeSources load the home screen. Each source fills its sections on its own, so a failing or
// slow one never holds up the others.
type HomeSources struct {
	// PullRequests fills both the reviews waiting for the user and their own pull requests.
	PullRequests func(context.Context) (domain.MyPullRequests, error)
	Approvals    func(context.Context) ([]domain.ReleaseApproval, error)
	Builds       func(context.Context) ([]domain.Build, error)
	WorkItems    func(context.Context) ([]domain.WorkItem, error)
}

// Load reads every source at once, for static output. A source that fails leaves its sections
// empty, with its error under the section's JSON name.
func (s HomeSources) Load(ctx context.Context, project string) domain.Home {
	// Empty sections are empty lists in JSON, not null.
	h := domain.Home{Project: project, ReviewRequests: []domain.ReviewRequest{}, Approvals: []domain.ReleaseApproval{},
		PullRequests: []domain.PullRequest{}, Builds: []domain.Build{}, WorkItems: []domain.WorkItem{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	run := func(name string, load func() error) {
		wg.Go(func() {
			if err := load(); err != nil {
				mu.Lock()
				defer mu.Unlock()
				if h.Errors == nil {
					h.Errors = map[string]string{}
				}
				h.Errors[name] = err.Error()
			}
		})
	}
	run("pullRequests", func() error {
		prs, err := s.PullRequests(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		h.ReviewRequests = append(h.ReviewRequests, prs.ToReview...)
		h.PullRequests = append(h.PullRequests, prs.Created...)
		if prs.Note != "" {
			h.Notes = append(h.Notes, prs.Note)
		}
		return nil
	})
	run("approvals", func() error {
		as, err := s.Approvals(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		h.Approvals = append(h.Approvals, as...)
		return nil
	})
	run("builds", func() error {
		bs, err := s.Builds(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		h.Builds = append(h.Builds, bs...)
		return nil
	})
	run("workItems", func() error {
		ws, err := s.WorkItems(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		h.WorkItems = append(h.WorkItems, ws...)
		return nil
	})
	wg.Wait()
	return h
}

// homeSource identifies one of the HomeSources.
type homeSource int

const (
	sourcePullRequests homeSource = iota
	sourceApprovals
	sourceBuilds
	sourceWorkItems
)

// The sections, in the order they are shown: what others wait for first, then the user's own work.
const (
	sectionReviews = iota
	sectionApprovals
	sectionPullRequests
	sectionBuilds
	sectionWorkItems
)

type homeLoadedMsg struct {
	screen    int64
	source    homeSource
	prs       domain.MyPullRequests
	approvals []domain.ReleaseApproval
	builds    []domain.Build
	items     []domain.WorkItem
	err       error
}

// homeSection is one titled list on the home screen.
type homeSection struct {
	ui.HomeSection
	source  homeSource
	table   Table // lays out the rows; the home screen does the scrolling
	entries []any // what each row shows: a ReviewRequest, PullRequest, Build, ReleaseApproval or WorkItem
	loaded  bool
	err     error
	note    string
}

func (s *homeSection) heading() string {
	title := ui.Bold.Render(s.Title)
	switch {
	case !s.loaded && s.err == nil:
		return title + "  " + ui.Muted.Render("loading…")
	case s.err != nil:
		return title
	case len(s.entries) == 0:
		return title + "  " + ui.Muted.Render(s.None)
	}
	return title + "  " + ui.Muted.Render(strconv.Itoa(len(s.entries)))
}

// Home shows what waits for the user (pull requests to review, release approvals) and how their
// own pull requests, builds and work items are doing, in one scrollable list of sections.
type Home struct {
	id       int64
	project  string
	act      *Actions
	builds   *Builds
	rel      *Releases
	src      HomeSources
	sections []*homeSection
	// sec and row are the selected entry; row is -1 while there is none.
	sec, row int
	// moved is set once the user moves the cursor; until then loads keep it on the first entry.
	moved   bool
	offset  int // first visible line
	width   int
	height  int
	ticking bool
	now     func() time.Time
}

// NewHome creates the home screen of project. act serves the work item and pull request rows,
// builds and rel the build and approval rows.
func NewHome(project string, act *Actions, builds *Builds, rel *Releases, src HomeSources) *Home {
	section := func(s ui.HomeSection, source homeSource, cols ...Column) *homeSection {
		return &homeSection{HomeSection: s, source: source, table: NewTable(cols)}
	}
	return &Home{
		id: nextID(), project: project, act: act, builds: builds, rel: rel, src: src, row: -1,
		sections: []*homeSection{
			sectionReviews: section(ui.HomeReviews, sourcePullRequests, Column{Max: 8}, Column{Max: 20}, Column{Max: 20},
				Column{Max: 24}, Column{Max: 4}, Column{Flex: true}),
			sectionApprovals: section(ui.HomeApprovals, sourceApprovals, Column{Max: 28}, Column{Max: 24}, Column{Max: 20},
				Column{Max: 18}, Column{Max: 4}),
			sectionPullRequests: section(ui.HomePullRequests, sourcePullRequests, Column{Max: 8}, Column{Max: 20}, Column{Max: 24},
				Column{Max: 20}, Column{Max: 4}, Column{Flex: true}),
			sectionBuilds: section(ui.HomeBuilds, sourceBuilds, Column{Max: 8}, Column{Max: 28}, Column{Max: 30},
				Column{Max: 22}, Column{Max: 4}, Column{Max: 8}),
			sectionWorkItems: section(ui.HomeWorkItems, sourceWorkItems, Column{Max: 8}, Column{Max: 20}, Column{Max: 14},
				Column{Flex: true}),
		},
	}
}

func (h *Home) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

func (h *Home) load(source homeSource) tea.Cmd {
	msg := homeLoadedMsg{screen: h.id, source: source}
	switch source {
	case sourcePullRequests:
		return Busy("loading pull requests", func() tea.Msg {
			msg.prs, msg.err = h.src.PullRequests(context.Background())
			return msg
		})
	case sourceApprovals:
		return Busy("loading approvals", func() tea.Msg {
			msg.approvals, msg.err = h.src.Approvals(context.Background())
			return msg
		})
	case sourceBuilds:
		return Busy("loading builds", func() tea.Msg {
			msg.builds, msg.err = h.src.Builds(context.Background())
			return msg
		})
	}
	return Busy("loading work items", func() tea.Msg {
		msg.items, msg.err = h.src.WorkItems(context.Background())
		return msg
	})
}

func (h *Home) reload() tea.Cmd {
	return tea.Batch(h.load(sourcePullRequests), h.load(sourceApprovals), h.load(sourceBuilds), h.load(sourceWorkItems))
}

func (h *Home) Init() tea.Cmd   { return h.reload() }
func (h *Home) Title() string   { return h.project }
func (h *Home) Capturing() bool { return false }

func (h *Home) SetSize(w, ht int) {
	h.width, h.height = w, ht
	for _, s := range h.sections {
		s.table.SetSize(w, ht)
	}
	h.follow()
}

func (h *Home) selected() (any, bool) {
	if h.row < 0 || h.sec < 0 || h.sec >= len(h.sections) || h.row >= len(h.sections[h.sec].entries) {
		return nil, false
	}
	return h.sections[h.sec].entries[h.row], true
}

// positions lists every selectable entry as (section, row), top to bottom.
func (h *Home) positions() [][2]int {
	var out [][2]int
	for si, s := range h.sections {
		for ri := range s.entries {
			out = append(out, [2]int{si, ri})
		}
	}
	return out
}

func (h *Home) selectFirst() {
	h.sec, h.row = 0, -1
	if ps := h.positions(); len(ps) > 0 {
		h.sec, h.row = ps[0][0], ps[0][1]
	}
}

// move moves the cursor by delta entries, across sections.
func (h *Home) move(delta int) {
	ps := h.positions()
	if len(ps) == 0 {
		return
	}
	cur := 0
	for i, p := range ps {
		if p[0] == h.sec && p[1] == h.row {
			cur = i
		}
	}
	next := max(0, min(len(ps)-1, cur+delta))
	h.sec, h.row = ps[next][0], ps[next][1]
	h.moved = true
}

// jump moves to the first entry of the next (dir 1) or previous (dir -1) section that has any,
// wrapping around.
func (h *Home) jump(dir int) {
	n := len(h.sections)
	for i := 1; i <= n; i++ {
		si := ((h.sec+dir*i)%n + n) % n
		if len(h.sections[si].entries) > 0 {
			h.sec, h.row, h.moved = si, 0, true
			return
		}
	}
}

// fill replaces a section's rows, keeping the cursor on the same entry when it is still there.
func (h *Home) fill(si int, rows []Row, entries []any, err error, note string) {
	s := h.sections[si]
	keep, key := false, 0
	if h.moved && h.sec == si && h.row >= 0 && h.row < len(s.table.all) {
		keep, key = true, s.table.all[h.row].Key
	}
	s.loaded, s.err, s.note = true, err, note
	if err != nil {
		rows, entries = nil, nil
	}
	s.table.SetRows(rows)
	s.entries = entries
	switch {
	case !h.moved:
		h.selectFirst()
	case keep && len(entries) == 0:
		h.selectFirst()
	case keep:
		h.row = min(h.row, len(entries)-1)
		for i, r := range rows {
			if r.Key == key {
				h.row = i
			}
		}
	}
	h.follow()
}

// apply shows what a source loaded; failures show in their sections.
func (h *Home) apply(msg homeLoadedMsg) tea.Cmd {
	now := h.clock()
	switch msg.source {
	case sourcePullRequests:
		var rows []Row
		var entries []any
		for _, r := range msg.prs.ToReview {
			rows = append(rows, Row{
				Key:    r.ID,
				Cells:  []string{strconv.Itoa(r.ID), r.Repository, r.CreatedBy, ui.ReviewVia(r), ui.FormatAge(r.Created, now), r.Title},
				Styles: []lipgloss.Style{ui.ID, ui.Accent, ui.Muted, {}, ui.Muted, {}},
			})
			entries = append(entries, r)
		}
		h.fill(sectionReviews, rows, entries, msg.err, msg.prs.Note)
		rows, entries = nil, nil
		for _, p := range msg.prs.Created {
			title := p.Title
			if p.IsDraft {
				title = "[draft] " + title
			}
			rows = append(rows, Row{
				Key:    p.ID,
				Cells:  []string{strconv.Itoa(p.ID), p.Repository, p.Approval.String(), p.TargetBranch, ui.FormatAge(p.Created, now), title},
				Styles: []lipgloss.Style{ui.ID, ui.Accent, ui.ApprovalStyle(p.Approval), ui.Muted, ui.Muted, {}},
			})
			entries = append(entries, p)
		}
		h.fill(sectionPullRequests, rows, entries, msg.err, "")
	case sourceApprovals:
		var rows []Row
		var entries []any
		for _, a := range msg.approvals {
			rows = append(rows, Row{
				Key:    a.ID,
				Cells:  []string{a.Pipeline, a.Release, a.Stage, approvalWhen(a), ui.FormatAge(a.Created, now)},
				Styles: []lipgloss.Style{ui.Accent, ui.Bold, ui.Warn, ui.Muted, ui.Muted},
			})
			entries = append(entries, a)
		}
		h.fill(sectionApprovals, rows, entries, msg.err, "")
	case sourceBuilds:
		var rows []Row
		var entries []any
		running := false
		for _, b := range msg.builds {
			started := b.Started
			if started.IsZero() {
				started = b.Queued
			}
			rows = append(rows, Row{
				Key:    b.ID,
				Cells:  []string{strconv.Itoa(b.ID), b.Pipeline, b.Branch, ui.BuildLabel(b.State()), ui.FormatAge(started, now), ui.FormatDuration(b.Duration(now))},
				Styles: []lipgloss.Style{ui.ID, ui.Accent, {}, {}, ui.Muted, ui.Muted},
			})
			entries = append(entries, b)
			running = running || !b.State().Done()
		}
		h.fill(sectionBuilds, rows, entries, msg.err, "")
		if running && !h.ticking {
			h.ticking = true
			return tick(h.id)
		}
	case sourceWorkItems:
		var rows []Row
		var entries []any
		for _, w := range msg.items {
			rows = append(rows, homeWorkItemRow(w))
			entries = append(entries, w)
		}
		h.fill(sectionWorkItems, rows, entries, msg.err, "")
	}
	return nil
}

func homeWorkItemRow(w domain.WorkItem) Row {
	return Row{
		Key:    w.ID,
		Cells:  []string{strconv.Itoa(w.ID), w.Type, w.State, w.Title},
		Styles: []lipgloss.Style{ui.ID, ui.TypeStyle(w.Type), ui.StateStyle(w.State), {}},
	}
}

// homeLine is one line of the home screen; row is -1 for headings, messages and gaps.
type homeLine struct {
	text     string
	sec, row int
}

func (h *Home) lines() []homeLine {
	var out []homeLine
	message := func(si int, st lipgloss.Style, text string) {
		out = append(out, homeLine{text: "  " + st.Render(ansi.Truncate(text, max(10, h.width-2), "…")), sec: si, row: -1})
	}
	for si, s := range h.sections {
		if si > 0 {
			out = append(out, homeLine{sec: si, row: -1})
		}
		out = append(out, homeLine{text: s.heading(), sec: si, row: -1})
		if s.err != nil {
			message(si, ui.Danger, s.err.Error())
		}
		if s.note != "" {
			message(si, ui.Warn, s.note)
		}
		widths := s.table.widths()
		for ri, r := range s.table.all {
			out = append(out, homeLine{text: s.table.renderLine(r.Cells, r.Styles, widths, si == h.sec && ri == h.row), sec: si, row: ri})
		}
	}
	return out
}

// follow scrolls so the selected entry is visible, along with its section's heading when it is
// the section's first entry.
func (h *Home) follow() {
	lines := h.lines()
	height := max(1, h.height)
	cur, heading := -1, -1
	for i, l := range lines {
		if l.sec == h.sec && l.row == -1 && heading == -1 && l.text != "" {
			heading = i
		}
		if l.sec == h.sec && l.row == h.row && h.row >= 0 {
			cur = i
		}
	}
	if cur == -1 {
		h.offset = 0
		return
	}
	top := cur
	if h.row == 0 && heading >= 0 && cur-heading < height {
		top = heading
	}
	if top < h.offset {
		h.offset = top
	}
	if cur >= h.offset+height {
		h.offset = cur - height + 1
	}
	h.offset = max(0, min(h.offset, len(lines)-height))
}

func (h *Home) View() string {
	lines := h.lines()
	end := min(len(lines), h.offset+max(1, h.height))
	texts := make([]string, 0, end-h.offset)
	for _, l := range lines[min(h.offset, end):end] {
		texts = append(texts, l.text)
	}
	return strings.Join(texts, "\n")
}

func (h *Home) Help() []key.Binding {
	keys := []key.Binding{keyEnter, keyOpen}
	switch e, _ := h.selected(); e.(type) {
	case domain.ReviewRequest, domain.PullRequest:
		keys = append(keys, keyCheckout, keyPRWorktree)
	case domain.ReleaseApproval:
		keys = append(keys, keyApprove, keyReject, keyPromote)
	case domain.WorkItem:
		keys = append(keys, keyState, keyBranch, keyWorktree, keyComment)
	}
	return append(keys, keyCopy, keyRefresh, keyNextSection, keyPrevSection, keyUp, keyDown, keyTop, keyBottom)
}

func (h *Home) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case homeLoadedMsg:
		if msg.screen != h.id {
			return h, nil
		}
		return h, h.apply(msg)
	case buildTickMsg:
		if msg.screen != h.id {
			return h, nil
		}
		h.ticking = false
		return h, h.load(sourceBuilds)
	case workItemChangedMsg:
		s := h.sections[sectionWorkItems]
		for i, e := range s.entries {
			if w, ok := e.(domain.WorkItem); ok && w.ID == msg.item.ID {
				s.entries[i] = msg.item
				s.table.UpdateRow(homeWorkItemRow(msg.item))
			}
		}
		return h, nil
	case releaseChangedMsg:
		return h, h.load(sourceApprovals)
	case tea.KeyPressMsg:
		return h, h.key(msg)
	}
	return h, nil
}

func (h *Home) key(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyRefresh):
		return h.reload()
	case key.Matches(msg, keyUp):
		h.move(-1)
	case key.Matches(msg, keyDown):
		h.move(1)
	case key.Matches(msg, keyPgUp):
		h.move(-max(1, h.height-2))
	case key.Matches(msg, keyPgDown):
		h.move(max(1, h.height-2))
	case key.Matches(msg, keyTop):
		h.move(-len(h.positions()))
	case key.Matches(msg, keyBottom):
		h.move(len(h.positions()))
	case key.Matches(msg, keyNextSection):
		h.jump(1)
	case key.Matches(msg, keyPrevSection):
		h.jump(-1)
	default:
		return h.action(msg)
	}
	h.follow()
	return nil
}

// action runs the key's action on the selected entry.
func (h *Home) action(msg tea.KeyPressMsg) tea.Cmd {
	e, ok := h.selected()
	if !ok {
		return nil
	}
	switch e := e.(type) {
	case domain.ReviewRequest:
		return h.pullRequestKey(msg, e.PullRequest)
	case domain.PullRequest:
		return h.pullRequestKey(msg, e)
	case domain.ReleaseApproval:
		url := h.rel.Client.ReleaseWebURL(h.rel.Project, e.ReleaseID)
		switch {
		case key.Matches(msg, keyApprove):
			return h.rel.decide(e, true)
		case key.Matches(msg, keyReject):
			return h.rel.decide(e, false)
		case key.Matches(msg, keyPromote):
			return h.rel.promoteApproval(e)
		case key.Matches(msg, keyEnter):
			return Push(NewReleaseView(h.rel, e.ReleaseID))
		case key.Matches(msg, keyOpen):
			return h.rel.open(url)
		case key.Matches(msg, keyCopy):
			return copyURL(url)
		}
	case domain.Build:
		switch {
		case key.Matches(msg, keyEnter):
			return Push(NewBuildView(h.builds, e.ID))
		case key.Matches(msg, keyOpen):
			return h.builds.open(e.URL)
		case key.Matches(msg, keyCopy):
			return copyURL(e.URL)
		}
	case domain.WorkItem:
		switch {
		case key.Matches(msg, keyEnter):
			return Push(NewWorkItemDetail(h.act, e.ID))
		case key.Matches(msg, keyOpen):
			return h.act.open(e.URL)
		case key.Matches(msg, keyCopy):
			return copyURL(e.URL)
		case key.Matches(msg, keyState):
			return h.act.changeState(e)
		case key.Matches(msg, keyBranch):
			return h.act.branch(e)
		case key.Matches(msg, keyWorktree):
			return h.act.worktree(e)
		case key.Matches(msg, keyComment):
			return h.act.comment(e.ID)
		}
	}
	return nil
}

func (h *Home) pullRequestKey(msg tea.KeyPressMsg, pr domain.PullRequest) tea.Cmd {
	switch {
	case key.Matches(msg, keyEnter):
		return Push(NewPullRequestDetail(h.act, pr))
	case key.Matches(msg, keyOpen):
		return h.act.open(pr.URL)
	case key.Matches(msg, keyCopy):
		return copyURL(pr.URL)
	case key.Matches(msg, keyCheckout):
		return h.act.checkout(pr, false)
	case key.Matches(msg, keyPRWorktree):
		return h.act.checkout(pr, true)
	}
	return nil
}
