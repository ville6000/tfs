package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/ado"
	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

var (
	keyApprove   = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "approve"))
	keyReject    = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "reject"))
	keyApprovals = key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "my approvals"))
)

// Releases is what the release screens share: the client, the project and pipeline filters.
type Releases struct {
	Client   *ado.Client
	Project  string
	Patterns []string
	Open     func(url string) error
	// PerPipeline is how many recent releases per pipeline the dashboard looks at.
	PerPipeline int
	// Actions serve the work item screens opened from a release; when nil, those screens only browse.
	Actions *Actions
}

// releaseChangedMsg announces that a release was approved or rejected.
type releaseChangedMsg struct{ releaseID int }

func (r *Releases) open(url string) tea.Cmd {
	if err := r.Open(url); err != nil {
		return StatusErr(err)
	}
	return Status("opened " + url)
}

func (r *Releases) actions() *Actions {
	if r.Actions == nil {
		r.Actions = &Actions{Client: r.Client, Open: r.Open}
	}
	return r.Actions
}

func (r *Releases) changed(id int, status string) tea.Msg {
	return CmdMsg{tea.Batch(func() tea.Msg { return releaseChangedMsg{id} }, Status(status))}
}

func approvalWhen(a domain.ReleaseApproval) string {
	if strings.EqualFold(a.Type, "postDeploy") {
		return "after deployment"
	}
	return "before deployment"
}

// decide asks to approve or reject one approval and then applies it.
func (r *Releases) decide(a domain.ReleaseApproval, approve bool) tea.Cmd {
	verb, label := "Reject", "rejecting"
	if approve {
		verb, label = "Approve", "approving"
	}
	detail := fmt.Sprintf("%s · %s → %s (%s)", a.Pipeline, a.Release, a.Stage, approvalWhen(a))
	question := fmt.Sprintf("%s %s for %s?", verb, a.Release, a.Stage)
	return Push(NewConfirm(verb+" "+a.Stage, question, detail, verb, func(comment string) tea.Cmd {
		return Busy(label, func() tea.Msg {
			if err := r.Client.SetApproval(context.Background(), r.Project, a.ID, approve, comment); err != nil {
				return StatusMsg{Text: err.Error(), Err: true}
			}
			done := "rejected"
			if approve {
				done = "approved"
			}
			return r.changed(a.ReleaseID, fmt.Sprintf("%s %s for %s", done, a.Release, a.Stage))
		})
	}))
}

// decideStage approves or rejects the stage's pending approval that is the user's to act on.
func (r *Releases) decideStage(rel domain.Release, stage domain.ReleaseStage, approve bool) tea.Cmd {
	if len(stage.PendingApprovals) == 0 {
		return Status(fmt.Sprintf("%s is not waiting for an approval", stage.Name))
	}
	return Busy("finding approval", func() tea.Msg {
		a, err := r.Client.PickApproval(context.Background(), r.Project, rel, stage)
		if err != nil {
			return StatusMsg{Text: err.Error(), Err: true}
		}
		a.Pipeline, a.Release, a.Stage, a.ReleaseID = rel.Pipeline, rel.Name, stage.Name, rel.ID
		return CmdMsg{r.decide(a, approve)}
	})
}

// ---- dashboard ---------------------------------------------------------------------------

type overviewLoadedMsg struct {
	screen int64
	rows   []domain.PipelineOverview
	mine   []domain.ReleaseApproval
	err    error
}

// ReleaseDashboard shows each pipeline with what is deployed on each stage.
type ReleaseDashboard struct {
	id    int64
	r     *Releases
	rows  map[int]domain.PipelineOverview
	mine  int
	table Table
}

// NewReleaseDashboard creates the dashboard screen.
func NewReleaseDashboard(r *Releases) *ReleaseDashboard {
	t := NewTable([]Column{{Title: "PIPELINE", Max: 32}, {Title: "LATEST", Max: 24}, {Title: "CREATED", Max: 16}, {Title: "STAGES", Flex: true}})
	t.SetEmptyText("Loading…")
	return &ReleaseDashboard{id: nextID(), r: r, table: t, rows: map[int]domain.PipelineOverview{}}
}

func (d *ReleaseDashboard) reload() tea.Cmd {
	return Busy("loading releases", func() tea.Msg {
		ctx := context.Background()
		rows, err := d.r.Client.ReleaseOverview(ctx, d.r.Project, d.r.Patterns, d.r.PerPipeline)
		mine, _ := d.r.Client.MyPendingApprovals(ctx, d.r.Project)
		return overviewLoadedMsg{screen: d.id, rows: rows, mine: mine, err: err}
	})
}

func (d *ReleaseDashboard) Init() tea.Cmd    { return d.reload() }
func (d *ReleaseDashboard) Capturing() bool  { return d.table.Filtering() }
func (d *ReleaseDashboard) SetSize(w, h int) { d.table.SetSize(w, h) }
func (d *ReleaseDashboard) View() string     { return d.table.View() }
func (d *ReleaseDashboard) Title() string {
	if d.mine > 0 {
		return fmt.Sprintf("releases · %s", ui.Warn.Render(fmt.Sprintf("%d waiting for you (P)", d.mine)))
	}
	return "releases"
}
func (d *ReleaseDashboard) Help() []key.Binding {
	return append([]key.Binding{keyEnter, keyApprovals, keyOpen, keyRefresh}, d.table.Help()...)
}

func (d *ReleaseDashboard) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case overviewLoadedMsg:
		if msg.screen != d.id {
			return d, nil
		}
		d.mine = len(msg.mine)
		d.table.SetEmptyText("No release pipelines match.")
		d.rows = map[int]domain.PipelineOverview{}
		rows := make([]Row, 0, len(msg.rows))
		for _, o := range msg.rows {
			if o.Pipeline.ID == 0 { // failed to load; reported below
				continue
			}
			d.rows[o.Pipeline.ID] = o
			latest, created := "—", ""
			if o.Latest != nil {
				latest, created = o.Latest.Name, ui.FormatTime(o.Latest.Created)
			}
			stages := ui.StageDeployments(o.Deployments)
			rows = append(rows, Row{
				Key:    o.Pipeline.ID,
				Cells:  []string{o.Pipeline.Name, latest, created, stages},
				Styles: []lipgloss.Style{ui.Accent, {}, ui.Muted, {}},
				Search: o.Pipeline.Path + " " + o.Pipeline.Name + " " + latest + " " + ansi.Strip(stages),
			})
		}
		d.table.SetRows(rows)
		if msg.err != nil {
			return d, StatusErr(msg.err)
		}
		return d, nil
	case releaseChangedMsg:
		return d, d.reload()
	case tea.KeyPressMsg:
		if d.table.Filtering() {
			break
		}
		if key.Matches(msg, keyRefresh) {
			return d, d.reload()
		}
		if key.Matches(msg, keyApprovals) {
			return d, Push(NewApprovalsList(d.r))
		}
		row, ok := d.table.Selected()
		if !ok {
			break
		}
		o := d.rows[row.Key]
		switch {
		case key.Matches(msg, keyEnter):
			return d, Push(NewReleaseHistory(d.r, o.Pipeline))
		case key.Matches(msg, keyOpen):
			return d, d.r.open(o.Pipeline.URL)
		}
	}
	var cmd tea.Cmd
	d.table, cmd = d.table.Update(msg)
	return d, cmd
}

// ---- pipeline history ----------------------------------------------------------------------

type releasesLoadedMsg struct {
	screen   int64
	releases []domain.Release
	err      error
}

// ReleaseHistory lists a pipeline's recent releases.
type ReleaseHistory struct {
	id       int64
	r        *Releases
	pipeline domain.ReleasePipeline
	releases map[int]domain.Release
	table    Table
}

// NewReleaseHistory creates the history screen for one pipeline.
func NewReleaseHistory(r *Releases, p domain.ReleasePipeline) *ReleaseHistory {
	t := NewTable([]Column{{Title: "RELEASE", Max: 24}, {Title: "CREATED", Max: 16}, {Title: "BY", Max: 22}, {Title: "STAGES", Flex: true}})
	t.SetEmptyText("Loading…")
	return &ReleaseHistory{id: nextID(), r: r, pipeline: p, table: t, releases: map[int]domain.Release{}}
}

func (h *ReleaseHistory) reload() tea.Cmd {
	return Busy("loading "+h.pipeline.Name, func() tea.Msg {
		rels, err := h.r.Client.ListReleases(context.Background(), h.r.Project, h.pipeline.ID, 50)
		return releasesLoadedMsg{screen: h.id, releases: rels, err: err}
	})
}

func (h *ReleaseHistory) Init() tea.Cmd     { return h.reload() }
func (h *ReleaseHistory) Title() string     { return h.pipeline.Name }
func (h *ReleaseHistory) Capturing() bool   { return h.table.Filtering() }
func (h *ReleaseHistory) SetSize(w, ht int) { h.table.SetSize(w, ht) }
func (h *ReleaseHistory) View() string      { return h.table.View() }
func (h *ReleaseHistory) Help() []key.Binding {
	return append([]key.Binding{keyEnter, keyOpen, keyRefresh}, h.table.Help()...)
}

func (h *ReleaseHistory) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case releasesLoadedMsg:
		if msg.screen != h.id {
			return h, nil
		}
		if msg.err != nil {
			h.table.SetEmptyText("Failed to load.")
			return h, StatusErr(msg.err)
		}
		h.table.SetEmptyText("No releases yet.")
		h.releases = map[int]domain.Release{}
		rows := make([]Row, len(msg.releases))
		for i, rel := range msg.releases {
			h.releases[rel.ID] = rel
			stages := ui.ReleaseStages(rel)
			rows[i] = Row{
				Key:    rel.ID,
				Cells:  []string{rel.Name, ui.FormatTime(rel.Created), ui.OrDash(rel.CreatedBy), stages},
				Styles: []lipgloss.Style{ui.Bold, ui.Muted, ui.Muted, {}},
				Search: rel.Name + " " + rel.CreatedBy + " " + ansi.Strip(stages),
			}
		}
		h.table.SetRows(rows)
		return h, nil
	case releaseChangedMsg:
		if _, ok := h.releases[msg.releaseID]; ok {
			return h, h.reload()
		}
		return h, nil
	case tea.KeyPressMsg:
		if h.table.Filtering() {
			break
		}
		if key.Matches(msg, keyRefresh) {
			return h, h.reload()
		}
		row, ok := h.table.Selected()
		if !ok {
			break
		}
		rel := h.releases[row.Key]
		switch {
		case key.Matches(msg, keyEnter):
			return h, Push(NewReleaseView(h.r, rel.ID))
		case key.Matches(msg, keyOpen):
			return h, h.r.open(rel.URL)
		}
	}
	var cmd tea.Cmd
	h.table, cmd = h.table.Update(msg)
	return h, cmd
}

// ---- one release -----------------------------------------------------------------------------

var (
	keyToItems  = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "work items"))
	keyToStages = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "stages"))
)

type releaseLoadedMsg struct {
	screen  int64
	release domain.Release
	err     error
}

type releaseChangesMsg struct {
	screen  int64
	stageID int
	changes domain.ReleaseChanges
	err     error
}

// stageChanges are the work items one stage gets with the release, once loaded.
type stageChanges struct {
	loading bool
	changes domain.ReleaseChanges
	err     error
}

// ReleaseView shows one release's stages and lets the user approve or reject pending approvals.
// Below the stages it lists the work items the selected stage gets with the release.
type ReleaseView struct {
	sid     int64
	r       *Releases
	id      int
	release *domain.Release
	table   Table // stages
	items   Table // work items of the selected stage
	changes map[int]*stageChanges
	onItems bool // the work item list has the keyboard
	placed  bool // the cursor was moved to the stage that matters once
	width   int
	height  int

	// Stage is the stage to start on; by default the first one awaiting approval or not deployed.
	Stage string
}

// NewReleaseView creates the screen for one release.
func NewReleaseView(r *Releases, id int) *ReleaseView {
	t := NewTable([]Column{{Title: "STAGE", Max: 24}, {Title: "STATUS", Max: 26}, {Title: "WAITING FOR", Max: 40}, {Title: "UPDATED", Flex: true}})
	t.SetEmptyText("Loading…")
	items := NewTable([]Column{
		{Title: "ID", Max: 8}, {Title: "TYPE", Max: 20}, {Title: "STATE", Max: 14},
		{Title: "ASSIGNED", Max: 22}, {Title: "TITLE", Flex: true},
	})
	items.SetFocused(false)
	return &ReleaseView{sid: nextID(), r: r, id: id, table: t, items: items, changes: map[int]*stageChanges{}}
}

func (v *ReleaseView) reload() tea.Cmd {
	return Busy(fmt.Sprintf("loading release %d", v.id), func() tea.Msg {
		rel, err := v.r.Client.GetRelease(context.Background(), v.r.Project, v.id)
		return releaseLoadedMsg{screen: v.sid, release: rel, err: err}
	})
}

func (v *ReleaseView) selectedStage() (domain.ReleaseStage, bool) {
	row, ok := v.table.Selected()
	if v.release == nil || !ok || row.Key >= len(v.release.Stages) {
		return domain.ReleaseStage{}, false
	}
	return v.release.Stages[row.Key], true
}

// loadChanges starts loading the selected stage's work items unless they are loaded or loading.
func (v *ReleaseView) loadChanges() tea.Cmd {
	stage, ok := v.selectedStage()
	if !ok || v.changes[stage.ID] != nil {
		return nil
	}
	v.changes[stage.ID] = &stageChanges{loading: true}
	rel := *v.release
	return Busy("loading work items", func() tea.Msg {
		ch, err := v.r.Client.ReleaseChanges(context.Background(), v.r.Project, rel, stage)
		return releaseChangesMsg{screen: v.sid, stageID: stage.ID, changes: ch, err: err}
	})
}

// showChanges fills the work item list for the selected stage.
func (v *ReleaseView) showChanges() {
	stage, ok := v.selectedStage()
	sc := v.changes[stage.ID]
	var rows []Row
	switch {
	case !ok:
		v.items.SetEmptyText("")
	case sc == nil || sc.loading:
		v.items.SetEmptyText("Loading…")
	case sc.err != nil:
		v.items.SetEmptyText("Could not load the work items.")
	default:
		v.items.SetEmptyText("No new work items.")
		rows = make([]Row, len(sc.changes.WorkItems))
		for i, w := range sc.changes.WorkItems {
			rows[i] = workItemRow(w)
		}
	}
	v.items.SetRows(rows)
	v.layout()
}

func (v *ReleaseView) selectedItem() (domain.WorkItem, bool) {
	stage, _ := v.selectedStage()
	row, ok := v.items.Selected()
	sc := v.changes[stage.ID]
	if !ok || sc == nil {
		return domain.WorkItem{}, false
	}
	for _, w := range sc.changes.WorkItems {
		if w.ID == row.Key {
			return w, true
		}
	}
	return domain.WorkItem{}, false
}

func (v *ReleaseView) focusItems(on bool) {
	v.onItems = on
	v.table.SetFocused(!on)
	v.items.SetFocused(on)
}

func (v *ReleaseView) header() string {
	if v.release == nil {
		return ""
	}
	rel := v.release
	lines := []string{
		ui.Bold.Render(rel.Name) + "  " + ui.Accent.Render(rel.Pipeline) + ui.Muted.Render("  ·  by "+ui.OrDash(rel.CreatedBy)+"  ·  "+ui.FormatTime(rel.Created)),
	}
	if a := ui.ReleaseArtifacts(*rel); a != "" {
		lines = append(lines, a)
	}
	if d := strings.TrimSpace(rel.Description); d != "" {
		lines = append(lines, d)
	}
	lines = append(lines, ui.Muted.Render(rel.URL), "")
	return strings.Join(lines, "\n")
}

// changesHeading is the line above the work item list, and a line of notes when there are any.
func (v *ReleaseView) changesHeading() string {
	stage, ok := v.selectedStage()
	if !ok {
		return ""
	}
	sc := v.changes[stage.ID]
	if sc == nil || sc.loading || sc.err != nil {
		return ui.Bold.Render("Work items for " + stage.Name)
	}
	heading := ui.Bold.Render(sc.changes.Heading())
	if len(sc.changes.Notes) > 0 {
		heading += "\n" + ui.Warn.Render(ansi.Truncate(strings.Join(sc.changes.Notes, " · "), max(10, v.width-2), "…"))
	}
	return heading
}

func (v *ReleaseView) layout() {
	headerLines := strings.Count(v.header(), "\n") + 1
	if v.release == nil {
		v.table.SetSize(v.width, max(3, v.height-headerLines))
		return
	}
	avail := v.height - headerLines - 1 - (strings.Count(v.changesHeading(), "\n") + 1) // blank line and heading
	stages := min(len(v.release.Stages)+2, max(3, avail/2))
	v.table.SetSize(v.width, max(3, stages))
	v.items.SetSize(v.width, max(3, avail-stages))
}

func (v *ReleaseView) Init() tea.Cmd    { return v.reload() }
func (v *ReleaseView) Capturing() bool  { return v.table.Filtering() || v.items.Filtering() }
func (v *ReleaseView) SetSize(w, h int) { v.width, v.height = w, h; v.layout() }
func (v *ReleaseView) View() string {
	if v.release == nil {
		return v.header() + "\n" + v.table.View()
	}
	return v.header() + "\n" + v.table.View() + "\n\n" + v.changesHeading() + "\n" + v.items.View()
}
func (v *ReleaseView) Title() string {
	if v.release != nil {
		return v.release.Name
	}
	return fmt.Sprintf("release %d", v.id)
}
func (v *ReleaseView) Help() []key.Binding {
	if v.onItems {
		return append([]key.Binding{keyApprove, keyReject, keyEnter, keyOpen, keyToStages, keyRefresh}, v.items.Help()[:3]...)
	}
	return append([]key.Binding{keyApprove, keyReject, keyToItems, keyOpen, keyRefresh}, v.table.Help()[:3]...)
}

func (v *ReleaseView) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case releaseLoadedMsg:
		if msg.screen != v.sid {
			return v, nil
		}
		if msg.err != nil {
			v.table.SetEmptyText("Failed to load.")
			return v, StatusErr(msg.err)
		}
		v.release = &msg.release
		v.table.SetEmptyText("This release has no stages.")
		rows := make([]Row, len(msg.release.Stages))
		for i, s := range msg.release.Stages {
			var waiting []string
			for _, a := range s.PendingApprovals {
				waiting = append(waiting, a.Approver)
			}
			rows[i] = Row{
				Key:    i,
				Cells:  []string{s.Name, ui.StageLabel(s.State()), ui.OrDash(strings.Join(waiting, ", ")), ui.FormatTime(s.Modified)},
				Styles: []lipgloss.Style{ui.Bold, {}, {}, ui.Muted},
				Search: s.Name + " " + s.State().String() + " " + strings.Join(waiting, " "),
			}
		}
		v.table.SetRows(rows)
		if !v.placed {
			v.placed = true
			v.table.cursor = msg.release.FocusStage()
			for i, s := range msg.release.Stages {
				if v.Stage != "" && strings.EqualFold(s.Name, v.Stage) {
					v.table.cursor = i
				}
			}
			v.table.clamp()
		}
		v.showChanges()
		return v, v.loadChanges()
	case releaseChangesMsg:
		if msg.screen != v.sid {
			return v, nil
		}
		v.changes[msg.stageID] = &stageChanges{changes: msg.changes, err: msg.err}
		v.showChanges()
		if msg.err != nil {
			return v, StatusErr(fmt.Errorf("work items: %w", msg.err))
		}
		return v, nil
	case releaseChangedMsg:
		if msg.releaseID == v.id {
			return v, v.reload()
		}
		return v, nil
	case tea.KeyPressMsg:
		if v.Capturing() || v.release == nil {
			break
		}
		switch {
		case key.Matches(msg, keyRefresh):
			v.changes = map[int]*stageChanges{}
			return v, v.reload()
		case key.Matches(msg, keyToItems):
			v.focusItems(!v.onItems)
			return v, nil
		case key.Matches(msg, keyApprove), key.Matches(msg, keyReject):
			stage, ok := v.selectedStage()
			if !ok {
				return v, nil
			}
			return v, v.r.decideStage(*v.release, stage, key.Matches(msg, keyApprove))
		case key.Matches(msg, keyOpen):
			if !v.onItems {
				return v, v.r.open(v.release.URL)
			}
			if w, ok := v.selectedItem(); ok {
				return v, v.r.open(w.URL)
			}
			return v, nil
		case key.Matches(msg, keyEnter):
			if !v.onItems {
				v.focusItems(true)
				return v, nil
			}
			if w, ok := v.selectedItem(); ok {
				return v, Push(NewWorkItemDetail(v.r.actions(), w.ID))
			}
			return v, nil
		}
	}
	var cmd tea.Cmd
	if v.onItems {
		v.items, cmd = v.items.Update(msg)
		return v, cmd
	}
	v.table, cmd = v.table.Update(msg)
	if _, ok := msg.(tea.KeyPressMsg); ok && v.release != nil {
		v.showChanges()
		return v, tea.Batch(cmd, v.loadChanges())
	}
	return v, cmd
}

// ---- my approvals --------------------------------------------------------------------------

type approvalsLoadedMsg struct {
	screen    int64
	approvals []domain.ReleaseApproval
	err       error
}

// ApprovalsList lists release approvals waiting for the user.
type ApprovalsList struct {
	id        int64
	r         *Releases
	approvals map[int]domain.ReleaseApproval
	table     Table
}

// NewApprovalsList creates the approvals screen.
func NewApprovalsList(r *Releases) *ApprovalsList {
	t := NewTable([]Column{{Title: "PIPELINE", Max: 28}, {Title: "RELEASE", Max: 24}, {Title: "STAGE", Max: 20},
		{Title: "WHEN", Max: 18}, {Title: "WAITING SINCE", Flex: true}})
	t.SetEmptyText("Loading…")
	return &ApprovalsList{id: nextID(), r: r, table: t, approvals: map[int]domain.ReleaseApproval{}}
}

func (l *ApprovalsList) reload() tea.Cmd {
	return Busy("loading approvals", func() tea.Msg {
		as, err := l.r.Client.MyPendingApprovals(context.Background(), l.r.Project)
		return approvalsLoadedMsg{screen: l.id, approvals: as, err: err}
	})
}

func (l *ApprovalsList) Init() tea.Cmd    { return l.reload() }
func (l *ApprovalsList) Title() string    { return "my approvals" }
func (l *ApprovalsList) Capturing() bool  { return l.table.Filtering() }
func (l *ApprovalsList) SetSize(w, h int) { l.table.SetSize(w, h) }
func (l *ApprovalsList) View() string     { return l.table.View() }
func (l *ApprovalsList) Help() []key.Binding {
	return append([]key.Binding{keyApprove, keyReject, keyEnter, keyOpen, keyRefresh}, l.table.Help()...)
}

func (l *ApprovalsList) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case approvalsLoadedMsg:
		if msg.screen != l.id {
			return l, nil
		}
		if msg.err != nil {
			l.table.SetEmptyText("Failed to load.")
			return l, StatusErr(msg.err)
		}
		l.table.SetEmptyText("Nothing is waiting for your approval.")
		l.approvals = map[int]domain.ReleaseApproval{}
		var rows []Row
		for _, a := range msg.approvals {
			if !domain.MatchPipeline(l.r.Patterns, domain.ReleasePipeline{Name: a.Pipeline}) {
				continue
			}
			l.approvals[a.ID] = a
			rows = append(rows, Row{
				Key:    a.ID,
				Cells:  []string{a.Pipeline, a.Release, a.Stage, approvalWhen(a), ui.FormatTime(a.Created)},
				Styles: []lipgloss.Style{ui.Accent, ui.Bold, ui.Warn, ui.Muted, ui.Muted},
			})
		}
		l.table.SetRows(rows)
		return l, nil
	case releaseChangedMsg:
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
		a := l.approvals[row.Key]
		switch {
		case key.Matches(msg, keyApprove):
			return l, l.r.decide(a, true)
		case key.Matches(msg, keyReject):
			return l, l.r.decide(a, false)
		case key.Matches(msg, keyEnter):
			return l, Push(NewReleaseView(l.r, a.ReleaseID))
		case key.Matches(msg, keyOpen):
			return l, l.r.open(l.r.Client.ReleaseWebURL(l.r.Project, a.ReleaseID))
		}
	}
	var cmd tea.Cmd
	l.table, cmd = l.table.Update(msg)
	return l, cmd
}
