package output

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

// Builds prints a build table.
func Builds(w io.Writer, builds []domain.Build, now time.Time, width int) {
	cells := make([][]Cell, len(builds))
	for i, b := range builds {
		started := b.Started
		if started.IsZero() {
			started = b.Queued
		}
		cells[i] = []Cell{
			{Text: strconv.Itoa(b.ID), Style: ui.ID}, {Text: b.Pipeline, Style: ui.Accent}, {Text: b.Branch},
			{Text: ui.BuildLabel(b.State())}, {Text: ui.OrDash(b.RequestedFor), Style: ui.Muted},
			{Text: ui.FormatTime(started), Style: ui.Muted}, {Text: ui.FormatDuration(b.Duration(now)), Style: ui.Muted},
		}
	}
	Table(w, []string{"ID", "PIPELINE", "BRANCH", "STATUS", "BY", "STARTED", "TIME"}, cells, width)
}

// Build prints one build with its stages, jobs and tasks and their issues.
func Build(w io.Writer, b domain.Build, timeline []domain.TimelineRecord, now time.Time) {
	commit := b.Commit
	if len(commit) > 8 {
		commit = commit[:8]
	}
	lipgloss.Fprintln(w, ui.ID.Render(fmt.Sprintf("#%d", b.ID))+"  "+ui.Accent.Render(b.Pipeline)+"  "+ui.Bold.Render(b.Number))
	meta := []string{strings.TrimSpace(b.Branch + " @ " + commit)}
	for _, m := range []string{b.Reason, b.RequestedFor} {
		if m != "" {
			meta = append(meta, m)
		}
	}
	lipgloss.Fprintln(w, strings.Join(meta, "  ·  "))
	state := ui.BuildLabel(b.State())
	if d := ui.FormatDuration(b.Duration(now)); d != "" {
		state += "  ·  " + d
	}
	lipgloss.Fprintln(w, state)
	lipgloss.Fprintln(w, ui.Muted.Render(b.URL))
	lipgloss.Fprintln(w)
	for _, row := range domain.TimelineOutline(timeline) {
		r := row.Record
		line := strings.Repeat("  ", row.Depth) + ui.BuildSymbol(r.Status()) + " " + r.Name
		if d := ui.FormatDuration(r.Duration(now)); d != "" {
			line += ui.Muted.Render("  " + d)
		}
		lipgloss.Fprintln(w, line)
		for _, is := range r.Issues {
			style := ui.Warn
			if strings.EqualFold(is.Type, "error") {
				style = ui.Danger
			}
			lipgloss.Fprintln(w, strings.Repeat("  ", row.Depth+2)+style.Render(is.Type+": "+strings.ReplaceAll(is.Message, "\n", " ")))
		}
	}
}
