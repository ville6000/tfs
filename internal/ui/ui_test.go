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

func TestWorkItemMarkdownSections(t *testing.T) {
	md := WorkItemMarkdown(domain.WorkItemDetail{ReproSteps: "Open the app"}, "")
	if !strings.Contains(md, "## Description\n\n_none_") || !strings.Contains(md, "## Repro steps\n\nOpen the app") ||
		strings.Contains(md, "Acceptance criteria") {
		t.Errorf("sections:\n%s", md)
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

func TestFormatAge(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{30 * time.Second: "now", 5 * time.Minute: "5m", 3 * time.Hour: "3h", 50 * time.Hour: "2d"} {
		if got := FormatAge(now.Add(-d), now); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
	if got := FormatAge(time.Time{}, now); got != "" {
		t.Errorf("zero time: %q", got)
	}
}

func TestGroupName(t *testing.T) {
	for in, want := range map[string]string{
		`[Portal]\Web Team`: "Web Team", `[DefaultCollection]\Reviewers`: "Reviewers",
		"Web Team": "Web Team", `CONTOSO\devs`: `CONTOSO\devs`, `[Portal]\`: `[Portal]\`,
	} {
		if got := GroupName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
