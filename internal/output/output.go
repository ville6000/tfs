// Package output prints results as static tables, trees and JSON for non-interactive use.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

// Cell is a table cell with an optional style.
type Cell struct {
	Text  string
	Style lipgloss.Style
}

// Table renders rows with columns aligned. The last column is truncated to fit width when
// width > 0.
func Table(w io.Writer, headers []string, rows [][]Cell, width int) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = ansi.StringWidth(h)
	}
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], ansi.StringWidth(c.Text))
		}
	}
	last := len(headers) - 1
	if width > 0 {
		used := 0
		for i := 0; i < last; i++ {
			used += widths[i] + 2
		}
		widths[last] = max(10, min(widths[last], width-used))
	}
	line := func(cells []Cell, header bool) string {
		parts := make([]string, len(cells))
		for i, c := range cells {
			text := ansi.Truncate(c.Text, widths[i], "…")
			pad := ""
			if i < last {
				pad = strings.Repeat(" ", widths[i]-ansi.StringWidth(text))
			}
			style := c.Style
			if header {
				style = ui.Bold
			}
			parts[i] = style.Render(text) + pad
		}
		return strings.TrimRight(strings.Join(parts, "  "), " ")
	}
	hdr := make([]Cell, len(headers))
	for i, h := range headers {
		hdr[i] = Cell{Text: h}
	}
	lipgloss.Fprintln(w, line(hdr, true))
	for _, r := range rows {
		lipgloss.Fprintln(w, line(r, false))
	}
}

// WorkItems prints a work item table.
func WorkItems(w io.Writer, items []domain.WorkItem, width int) {
	rows := make([][]Cell, len(items))
	for i, it := range items {
		rows[i] = []Cell{
			{Text: strconv.Itoa(it.ID), Style: ui.ID},
			{Text: it.Type, Style: ui.TypeStyle(it.Type)},
			{Text: it.State, Style: ui.StateStyle(it.State)},
			{Text: ui.OrDash(it.AssignedTo), Style: ui.Muted},
			{Text: it.Title},
		}
	}
	Table(w, []string{"ID", "TYPE", "STATE", "ASSIGNED", "TITLE"}, rows, width)
}

// PullRequests prints a pull request table.
func PullRequests(w io.Writer, prs []domain.PullRequest, width int) {
	rows := make([][]Cell, len(prs))
	for i, p := range prs {
		title := p.Title
		if p.IsDraft {
			title = "[draft] " + title
		}
		rows[i] = []Cell{
			{Text: strconv.Itoa(p.ID), Style: ui.ID},
			{Text: p.Repository, Style: ui.Accent},
			{Text: p.CreatedBy, Style: ui.Muted},
			{Text: p.Approval.String(), Style: ui.ApprovalStyle(p.Approval)},
			{Text: p.SourceBranch + " → " + p.TargetBranch},
			{Text: title},
		}
	}
	Table(w, []string{"ID", "REPO", "AUTHOR", "APPROVAL", "BRANCHES", "TITLE"}, rows, width)
}

// TreeLine renders one tree node label.
func TreeLine(n *domain.TreeNode) string {
	it := n.Item
	label := fmt.Sprintf("%s %s %s %s", ui.ID.Render(fmt.Sprintf("#%d", it.ID)),
		ui.TypeStyle(it.Type).Render(it.Type), ui.StateStyle(it.State).Render("["+it.State+"]"), it.Title)
	if !n.Matches {
		label = ui.Muted.Render(fmt.Sprintf("#%d %s [%s] %s", it.ID, it.Type, it.State, it.Title))
	}
	return label
}

// maxAssigneeWidth caps how much room an assignee name takes in a tree.
const maxAssigneeWidth = 24

// TreeAssignee renders a node's assignee, or "" when it is unassigned. Ancestors that only
// connect matches are dimmed like the rest of their line.
func TreeAssignee(n *domain.TreeNode) string {
	name := strings.TrimSpace(n.Item.AssignedTo)
	if name == "" {
		return ""
	}
	name = ansi.Truncate(name, maxAssigneeWidth, "…")
	if !n.Matches {
		return ui.Muted.Render(name)
	}
	return name
}

// NoParentLabel is the heading for parentless non-Epic items.
const NoParentLabel = "No parent"

// Tree prints a work item hierarchy.
func Tree(w io.Writer, t domain.Tree) {
	var walk func(nodes []*domain.TreeNode, prefix string)
	walk = func(nodes []*domain.TreeNode, prefix string) {
		for i, n := range nodes {
			branch, next := "├── ", "│   "
			if i == len(nodes)-1 {
				branch, next = "└── ", "    "
			}
			lipgloss.Fprintln(w, ui.Muted.Render(prefix+branch)+TreeLine(n)+assigneeSuffix(n))
			walk(n.Children, prefix+next)
		}
	}
	for _, r := range t.Roots {
		lipgloss.Fprintln(w, TreeLine(r)+assigneeSuffix(r))
		walk(r.Children, "")
	}
	if len(t.Orphans) > 0 {
		lipgloss.Fprintln(w, ui.Bold.Render(NoParentLabel))
		walk(t.Orphans, "")
	}
}

func assigneeSuffix(n *domain.TreeNode) string {
	if a := TreeAssignee(n); a != "" {
		return ui.Muted.Render(" · ") + a
	}
	return ""
}

// JSON prints v as indented JSON.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
