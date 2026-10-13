package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	app := &App{out: &out, errOut: &errOut}
	root := app.rootCmd("test")
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestWorkItemListAndConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/wiql") {
			_ = json.NewEncoder(w).Encode(map[string]any{"workItems": []map[string]int{{"id": 7}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"value": []any{map[string]any{"id": 7, "fields": map[string]any{
			"System.Title": "Hello", "System.WorkItemType": "Bug", "System.State": "New", "System.TeamProject": "P"}}}})
	}))
	defer srv.Close()

	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TFS_PAT", "x")
	t.Setenv("TFS_ALLOW_INSECURE", "1")

	if _, err := runCLI(t, "config", "set", "collectionUrl", srv.URL+"/Col"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "wi", "list", "--no-tui"); err == nil || !strings.Contains(err.Error(), "no project") {
		t.Fatalf("expected missing project error, got %v", err)
	}
	out, err := runCLI(t, "wi", "list", "-p", "P", "--no-tui")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "7   Bug   New    —         Hello") {
		t.Errorf("table:\n%s", out)
	}
	out, err = runCLI(t, "wi", "list", "-p", "P", "--json")
	if err != nil || !strings.Contains(out, `"title": "Hello"`) {
		t.Errorf("json: %v\n%s", err, out)
	}
	if _, err := runCLI(t, "wi", "list", "-p", "P", "--all", "--open"); err == nil {
		t.Error("expected conflicting flags error")
	}
	if _, err := runCLI(t, "wi", "show", "--no-tui"); err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Errorf("show without id: %v", err)
	}
}

func TestParseID(t *testing.T) {
	for in, want := range map[string]int{"12": 12, "#12": 12, "!7": 7} {
		if got, err := parseID(in); err != nil || got != want {
			t.Errorf("parseID(%q) = %d, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "0", "-1", "abc"} {
		if _, err := parseID(bad); err == nil {
			t.Errorf("parseID(%q) should fail", bad)
		}
	}
}
