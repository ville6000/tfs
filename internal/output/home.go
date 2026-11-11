package output

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

// Home prints the home screen's sections one after another. A section that could not be loaded
// shows its error.
func Home(w io.Writer, h domain.Home, now time.Time, width int) {
	first := true
	section := func(s ui.HomeSection, n int, errKey string, table func()) {
		if !first {
			lipgloss.Fprintln(w)
		}
		first = false
		if msg, failed := h.Errors[errKey]; failed {
			lipgloss.Fprintln(w, ui.Bold.Render(s.Title))
			lipgloss.Fprintln(w, "  "+ui.Danger.Render("error: "+msg))
			return
		}
		if n == 0 {
			lipgloss.Fprintln(w, ui.Bold.Render(s.Title)+": "+ui.Muted.Render(s.None))
			return
		}
		lipgloss.Fprintln(w, ui.Bold.Render(fmt.Sprintf("%s (%d)", s.Title, n)))
		table()
	}

	section(ui.HomeReviews, len(h.ReviewRequests), "pullRequests", func() {
		rows := make([][]Cell, len(h.ReviewRequests))
		for i, r := range h.ReviewRequests {
			rows[i] = []Cell{
				{Text: strconv.Itoa(r.ID), Style: ui.ID}, {Text: r.Repository, Style: ui.Accent}, {Text: r.CreatedBy, Style: ui.Muted},
				{Text: ui.ReviewVia(r)}, {Text: ui.FormatAge(r.Created, now), Style: ui.Muted}, {Text: r.Title},
			}
		}
		Table(w, []string{"ID", "REPO", "AUTHOR", "FOR", "AGE", "TITLE"}, rows, width)
	})
	for _, note := range h.Notes {
		lipgloss.Fprintln(w, ui.Warn.Render("note: "+note))
	}

	section(ui.HomeApprovals, len(h.Approvals), "approvals", func() { Approvals(w, h.Approvals, width) })

	section(ui.HomePullRequests, len(h.PullRequests), "pullRequests", func() {
		rows := make([][]Cell, len(h.PullRequests))
		for i, p := range h.PullRequests {
			title := p.Title
			if p.IsDraft {
				title = "[draft] " + title
			}
			rows[i] = []Cell{
				{Text: strconv.Itoa(p.ID), Style: ui.ID}, {Text: p.Repository, Style: ui.Accent},
				{Text: p.Approval.String(), Style: ui.ApprovalStyle(p.Approval)}, {Text: p.TargetBranch, Style: ui.Muted},
				{Text: ui.FormatAge(p.Created, now), Style: ui.Muted}, {Text: title},
			}
		}
		Table(w, []string{"ID", "REPO", "APPROVAL", "TARGET", "AGE", "TITLE"}, rows, width)
	})

	section(ui.HomeBuilds, len(h.Builds), "builds", func() { Builds(w, h.Builds, now, width) })

	section(ui.HomeWorkItems, len(h.WorkItems), "workItems", func() {
		rows := make([][]Cell, len(h.WorkItems))
		for i, it := range h.WorkItems {
			rows[i] = []Cell{
				{Text: strconv.Itoa(it.ID), Style: ui.ID}, {Text: it.Type, Style: ui.TypeStyle(it.Type)},
				{Text: it.State, Style: ui.StateStyle(it.State)}, {Text: it.Title},
			}
		}
		Table(w, []string{"ID", "TYPE", "STATE", "TITLE"}, rows, width)
	})
}
