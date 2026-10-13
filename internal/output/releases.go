package output

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

// ReleaseOverview prints the release dashboard.
func ReleaseOverview(w io.Writer, rows []domain.PipelineOverview, width int) {
	cells := make([][]Cell, 0, len(rows))
	for _, o := range rows {
		latest, created := "—", ""
		if o.Latest != nil {
			latest, created = o.Latest.Name, ui.FormatTime(o.Latest.Created)
		}
		cells = append(cells, []Cell{
			{Text: o.Pipeline.Name, Style: ui.Accent}, {Text: latest}, {Text: created, Style: ui.Muted},
			{Text: ui.StageDeployments(o.Deployments)},
		})
	}
	Table(w, []string{"PIPELINE", "LATEST", "CREATED", "STAGES"}, cells, width)
}

// Releases prints a pipeline's release history.
func Releases(w io.Writer, rels []domain.Release, width int) {
	cells := make([][]Cell, len(rels))
	for i, r := range rels {
		cells[i] = []Cell{
			{Text: strconv.Itoa(r.ID), Style: ui.ID}, {Text: r.Name, Style: ui.Bold},
			{Text: ui.FormatTime(r.Created), Style: ui.Muted}, {Text: ui.OrDash(r.CreatedBy), Style: ui.Muted},
			{Text: ui.ReleaseStages(r)},
		}
	}
	Table(w, []string{"ID", "RELEASE", "CREATED", "BY", "STAGES"}, cells, width)
}

// Release prints one release and its stages.
func Release(w io.Writer, r domain.Release) {
	lipgloss.Fprintln(w, ui.Bold.Render(r.Name)+"  "+ui.Accent.Render(r.Pipeline)+ui.Muted.Render(
		fmt.Sprintf("  ·  #%d  ·  by %s  ·  %s", r.ID, ui.OrDash(r.CreatedBy), ui.FormatTime(r.Created))))
	if d := strings.TrimSpace(r.Description); d != "" {
		lipgloss.Fprintln(w, d)
	}
	lipgloss.Fprintln(w, ui.Muted.Render(r.URL))
	lipgloss.Fprintln(w)
	cells := make([][]Cell, len(r.Stages))
	for i, s := range r.Stages {
		var waiting []string
		for _, a := range s.PendingApprovals {
			waiting = append(waiting, a.Approver)
		}
		cells[i] = []Cell{
			{Text: s.Name, Style: ui.Bold}, {Text: ui.StageLabel(s.State())},
			{Text: ui.OrDash(strings.Join(waiting, ", "))}, {Text: ui.FormatTime(s.Modified), Style: ui.Muted},
		}
	}
	Table(w, []string{"STAGE", "STATUS", "WAITING FOR", "UPDATED"}, cells, 0)
}

// Approvals prints pending release approvals.
func Approvals(w io.Writer, as []domain.ReleaseApproval, width int) {
	cells := make([][]Cell, len(as))
	for i, a := range as {
		when := "before deployment"
		if strings.EqualFold(a.Type, "postDeploy") {
			when = "after deployment"
		}
		cells[i] = []Cell{
			{Text: strconv.Itoa(a.ReleaseID), Style: ui.ID}, {Text: a.Pipeline, Style: ui.Accent}, {Text: a.Release, Style: ui.Bold},
			{Text: a.Stage, Style: ui.Warn}, {Text: when, Style: ui.Muted}, {Text: ui.FormatTime(a.Created), Style: ui.Muted},
		}
	}
	Table(w, []string{"RELEASE ID", "PIPELINE", "RELEASE", "STAGE", "WHEN", "WAITING SINCE"}, cells, width)
}
