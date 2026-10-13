package ui

import (
	"strings"
	"testing"

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
