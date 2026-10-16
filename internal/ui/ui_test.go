package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/domain"
)

func TestHTMLToMarkdownKeepsBreaks(t *testing.T) {
	md := HTMLToMarkdown("<p>Steps:<br>1. open<br>2. look</p>", "")
	out := ansi.Strip(RenderMarkdown(md, 80, true))
	lines := strings.Split(out, "\n")
	var trimmed []string
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			trimmed = append(trimmed, l)
		}
	}
	if len(trimmed) != 3 || trimmed[0] != "Steps:" || trimmed[2] != "2. look" {
		t.Errorf("line breaks lost: %q", trimmed)
	}
}

func TestFormatLog(t *testing.T) {
	in := "\ufeff2026-10-13T09:00:00.1234567Z ##[section]Starting: npm test\r\n" +
		"2026-10-13T09:00:01.0000000Z ##[command]npm test\n" +
		"2026-10-13T09:00:02Z plain output\n" +
		"##[group]Details\n##[endgroup]\n" +
		"2026-10-13T09:00:03.5Z ##[error]2 tests failed\n" +
		"##[warning]deprecated\n"
	want := "Starting: npm test\n$ npm test\nplain output\nDetails\nerror: 2 tests failed\nwarning: deprecated"
	if got := FormatLog(in, false); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	if got := ansi.Strip(FormatLog("##[error]boom", true)); got != "✘ boom" {
		t.Errorf("styled: %q", got)
	}
}

func TestFormatDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "", 45 * time.Second: "45s", 192 * time.Second: "3m12s", 64 * time.Minute: "1h04m"} {
		if got := FormatDuration(d); got != want {
			t.Errorf("%v: %q", d, got)
		}
	}
}

func TestRenderPullRequestComments(t *testing.T) {
	pr := domain.PullRequest{ID: 7, Title: "T", Repository: "R"}
	if out := ansi.Strip(RenderPullRequest(pr, 80, false)); strings.Contains(out, "Comments") {
		t.Errorf("comments section without --comments:\n%s", out)
	}

	pr.Threads = []domain.CommentThread{
		{ID: 1, Status: "active", FilePath: "/src/main.go", Line: 42, Comments: []domain.Comment{
			{Author: "Alice", Text: "Why this?", Type: "text"},
			{Author: "Bob", Text: "Because.", Type: "text", ParentID: 1},
		}},
		{ID: 2, Status: "fixed", Comments: []domain.Comment{{Author: "Carol", Text: "General note", Type: "text"}}},
		{ID: 3, Comments: []domain.Comment{{Author: "System", Text: "voted approve", Type: "system"}}},
	}
	out := ansi.Strip(RenderPullRequest(pr, 80, false))
	for _, want := range []string{"Comments (2)", "/src/main.go:42 · active", "Alice", "Why this?", "Bob", "Because.", "General · fixed", "General note"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "voted approve") {
		t.Errorf("system comment rendered:\n%s", out)
	}

	pr.Threads = []domain.CommentThread{}
	if out := ansi.Strip(RenderPullRequest(pr, 80, false)); !strings.Contains(out, "Comments (0)") || !strings.Contains(out, "No comments.") {
		t.Errorf("empty comments:\n%s", out)
	}
}
