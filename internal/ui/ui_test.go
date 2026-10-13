package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
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
