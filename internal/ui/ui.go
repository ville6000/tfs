// Package ui holds the styles and rendering helpers shared by the static printers and the TUI.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/charmbracelet/x/term"

	"github.com/ville6000/tfs/internal/domain"
)

// ANSI palette colors follow the user's terminal theme, so they read well on light and dark
// backgrounds alike.
var (
	ColorAccent  = lipgloss.Color("4")
	ColorMuted   = lipgloss.Color("8")
	ColorOK      = lipgloss.Color("2")
	ColorWarn    = lipgloss.Color("3")
	ColorDanger  = lipgloss.Color("1")
	ColorSpecial = lipgloss.Color("5")
	ColorInfo    = lipgloss.Color("6")
)

// Common styles.
var (
	Bold   = lipgloss.NewStyle().Bold(true)
	Muted  = lipgloss.NewStyle().Foreground(ColorMuted)
	Accent = lipgloss.NewStyle().Foreground(ColorAccent)
	OK     = lipgloss.NewStyle().Foreground(ColorOK)
	Warn   = lipgloss.NewStyle().Foreground(ColorWarn)
	Danger = lipgloss.NewStyle().Foreground(ColorDanger)
	Header = lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	ID     = lipgloss.NewStyle().Foreground(ColorInfo)
)

// StateStyle colors a work item state by its category.
func StateStyle(state string) lipgloss.Style {
	switch strings.ToLower(state) {
	case "new", "to do", "proposed", "approved":
		return lipgloss.NewStyle().Foreground(ColorAccent)
	case "active", "in progress", "committed", "doing", "open":
		return lipgloss.NewStyle().Foreground(ColorWarn)
	case "resolved", "closed", "done", "completed":
		return lipgloss.NewStyle().Foreground(ColorOK)
	case "removed", "cut", "abandoned":
		return Muted
	}
	return lipgloss.NewStyle()
}

// TypeStyle colors a work item type.
func TypeStyle(t string) lipgloss.Style {
	switch strings.ToLower(t) {
	case "epic":
		return lipgloss.NewStyle().Foreground(ColorWarn).Bold(true)
	case "feature":
		return lipgloss.NewStyle().Foreground(ColorSpecial)
	case "user story", "product backlog item", "requirement":
		return lipgloss.NewStyle().Foreground(ColorAccent)
	case "bug", "issue", "impediment":
		return lipgloss.NewStyle().Foreground(ColorDanger)
	case "task":
		return lipgloss.NewStyle().Foreground(ColorInfo)
	}
	return lipgloss.NewStyle()
}

// ApprovalStyle colors a pull request approval.
func ApprovalStyle(a domain.Approval) lipgloss.Style {
	switch a {
	case domain.ApprovalApproved:
		return OK
	case domain.ApprovalApprovedWithSuggestions:
		return lipgloss.NewStyle().Foreground(ColorInfo)
	case domain.ApprovalWaitingForAuthor:
		return Warn
	case domain.ApprovalRejected:
		return Danger
	}
	return Muted
}

// VoteLabel renders a reviewer vote.
func VoteLabel(vote int) string {
	switch {
	case vote >= 10:
		return OK.Render("✔ approved")
	case vote >= 5:
		return lipgloss.NewStyle().Foreground(ColorInfo).Render("✔ approved with suggestions")
	case vote == -5:
		return Warn.Render("… waiting for author")
	case vote <= -10:
		return Danger.Render("✘ rejected")
	}
	return Muted.Render("· no vote")
}

// OrDash returns s, or a dash when blank.
func OrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

var (
	darkOnce sync.Once
	dark     = true
)

// IsDark reports whether the terminal has a dark background (true when unknown).
func IsDark() bool {
	darkOnce.Do(func() {
		if term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) {
			dark = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
		}
	})
	return dark
}

// HTMLToMarkdown converts the HTML stored in rich-text fields and comments to Markdown.
// domain resolves relative links (e.g. attachments) against the collection URL.
func HTMLToMarkdown(html, domainURL string) string {
	if strings.TrimSpace(html) == "" {
		return ""
	}
	var opts []converter.ConvertOptionFunc
	if domainURL != "" {
		opts = append(opts, converter.WithDomain(domainURL))
	}
	md, err := htmltomarkdown.ConvertString(html, opts...)
	if err != nil {
		return html
	}
	return strings.TrimSpace(md)
}

// RenderMarkdown renders Markdown for the terminal at the given width. When styled is false the
// Markdown is returned as plain text.
func RenderMarkdown(md string, width int, styled bool) string {
	if !styled {
		return md
	}
	style := "dark"
	if s := os.Getenv("GLAMOUR_STYLE"); s != "" {
		style = s
	} else if !IsDark() {
		style = "light"
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(max(20, width-2)), glamour.WithPreservedNewLines())
	if err != nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return strings.Trim(out, "\n")
}

// WorkItemHeader renders the title and metadata lines of a work item.
func WorkItemHeader(w domain.WorkItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", ID.Render(fmt.Sprintf("#%d", w.ID)), Bold.Render(w.Title))
	meta := []string{
		TypeStyle(w.Type).Render(w.Type),
		StateStyle(w.State).Render(w.State),
		"assigned: " + OrDash(w.AssignedTo),
	}
	if w.HasParent() {
		meta = append(meta, fmt.Sprintf("parent: #%d", w.ParentID))
	}
	if w.Tags != "" {
		meta = append(meta, "tags: "+w.Tags)
	}
	b.WriteString(strings.Join(meta, Muted.Render("  ·  ")) + "\n")
	b.WriteString(Muted.Render(w.URL))
	return b.String()
}

// WorkItemMarkdown renders description, acceptance criteria and comments as Markdown.
func WorkItemMarkdown(d domain.WorkItemDetail, collection string) string {
	var b strings.Builder
	section := func(title, html string) {
		b.WriteString("## " + title + "\n\n")
		if md := HTMLToMarkdown(html, collection); md != "" {
			b.WriteString(md + "\n\n")
		} else {
			b.WriteString("_none_\n\n")
		}
	}
	section("Description", d.Description)
	if strings.TrimSpace(d.AcceptanceCriteria) != "" {
		section("Acceptance criteria", d.AcceptanceCriteria)
	}
	fmt.Fprintf(&b, "## Comments (%d)\n\n", len(d.Comments))
	for i, c := range d.Comments {
		if i > 0 {
			b.WriteString("---\n\n")
		}
		fmt.Fprintf(&b, "**%s** · %s\n\n%s\n\n", c.Author, FormatTime(c.Created), HTMLToMarkdown(c.Text, collection))
	}
	return b.String()
}

// RenderWorkItem renders a full work item view.
func RenderWorkItem(d domain.WorkItemDetail, collection string, width int, styled bool) string {
	return WorkItemHeader(d.WorkItem) + "\n\n" + RenderMarkdown(WorkItemMarkdown(d, collection), width, styled)
}

// RenderPullRequest renders a full pull request view.
func RenderPullRequest(pr domain.PullRequest, width int, styled bool) string {
	var b strings.Builder
	title := pr.Title
	if pr.IsDraft {
		title += Muted.Render(" [draft]")
	}
	fmt.Fprintf(&b, "%s %s\n", ID.Render(fmt.Sprintf("!%d", pr.ID)), Bold.Render(title))
	fmt.Fprintf(&b, "%s  %s → %s\n", Accent.Render(pr.Repository), pr.SourceBranch, pr.TargetBranch)
	fmt.Fprintf(&b, "%s  ·  %s  ·  by %s  ·  %s\n", pr.Status, ApprovalStyle(pr.Approval).Render(pr.Approval.String()), pr.CreatedBy, FormatTime(pr.Created))
	b.WriteString(Muted.Render(pr.URL) + "\n")
	if len(pr.Reviewers) > 0 {
		b.WriteString("\n" + Header.Render("Reviewers") + "\n")
		for _, r := range pr.Reviewers {
			req := ""
			if r.IsRequired {
				req = Muted.Render(" (required)")
			}
			fmt.Fprintf(&b, "  %s%s  %s\n", r.Name, req, VoteLabel(r.Vote))
		}
	}
	desc := strings.TrimSpace(pr.Description)
	if desc == "" {
		desc = "_No description._"
	}
	b.WriteString("\n" + RenderMarkdown(desc, width, styled))
	return b.String()
}

// FormatTime renders a timestamp in local time, or "" for the zero time.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

// Println writes s to w, downsampling colors to what w supports (none when piped).
func Println(w io.Writer, s string) { lipgloss.Fprintln(w, s) }

// StageSymbol renders a one-character symbol for a release stage state.
func StageSymbol(s domain.StageState) string {
	switch s {
	case domain.StageSucceeded:
		return OK.Render("✔")
	case domain.StagePartiallySucceeded:
		return Warn.Render("◐")
	case domain.StageDeploying:
		return lipgloss.NewStyle().Foreground(ColorInfo).Render("●")
	case domain.StageAwaitingApproval:
		return Warn.Bold(true).Render("⏸")
	case domain.StageQueued:
		return Muted.Render("◷")
	case domain.StageFailed:
		return Danger.Render("✘")
	case domain.StageCanceled:
		return Muted.Render("⊘")
	}
	return Muted.Render("○")
}

// StageLabel renders a stage state as a symbol and words, e.g. "⏸ awaiting approval".
func StageLabel(s domain.StageState) string {
	text := s.String()
	switch s {
	case domain.StageSucceeded:
		text = OK.Render(text)
	case domain.StageAwaitingApproval, domain.StagePartiallySucceeded:
		text = Warn.Render(text)
	case domain.StageFailed:
		text = Danger.Render(text)
	case domain.StageDeploying:
		text = lipgloss.NewStyle().Foreground(ColorInfo).Render(text)
	case domain.StageNotStarted, domain.StageQueued, domain.StageCanceled:
		text = Muted.Render(text)
	}
	return StageSymbol(s) + " " + text
}

// ReleaseStages renders a release's stages on one line: "Dev ✔  Test ●  Prod ○".
func ReleaseStages(r domain.Release) string {
	parts := make([]string, len(r.Stages))
	for i, s := range r.Stages {
		parts[i] = s.Name + " " + StageSymbol(s.State())
	}
	return strings.Join(parts, "  ")
}

// StageDeployments renders what is deployed where: "Dev ● Release-12  Prod ✔ Release-10".
func StageDeployments(ds []domain.StageDeployment) string {
	parts := make([]string, len(ds))
	for i, d := range ds {
		if d.Release == nil {
			parts[i] = d.Stage.Name + " " + StageSymbol(domain.StageNotStarted)
			continue
		}
		parts[i] = d.Stage.Name + " " + StageSymbol(d.State.State()) + " " + Muted.Render(d.Release.Name)
	}
	return strings.Join(parts, "  ")
}
