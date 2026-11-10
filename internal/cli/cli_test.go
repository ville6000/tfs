package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, _, err := runCLIWith(t, nil, args...)
	return out, err
}

// runCLIWith runs the CLI with stdin (os.Stdin when nil) and returns its stdout and stderr.
func runCLIWith(t *testing.T, stdin io.Reader, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	app := &App{in: stdin, out: &out, errOut: &errOut}
	root := app.rootCmd("test")
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func TestWorkItemListAndConfig(t *testing.T) {
	var wiql string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/wiql") {
			b, _ := io.ReadAll(r.Body)
			wiql = string(b)
			_ = json.NewEncoder(w).Encode(map[string]any{"workItems": []map[string]int{{"id": 7}}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/teamfieldvalues") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"field":  map[string]string{"referenceName": "System.AreaPath"},
				"values": []map[string]any{{"value": "P\\" + strings.Split(r.URL.Path, "/")[3], "includeChildren": true}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"value": []any{map[string]any{"id": 7, "fields": map[string]any{
			"System.Title": "Hello", "System.WorkItemType": "Bug", "System.State": "New", "System.TeamProject": "P"}}}})
	}))
	defer srv.Close()

	t.Chdir(t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
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
	if strings.Contains(wiql, "AreaPath") {
		t.Errorf("no team should query the whole project: %s", wiql)
	}
	if _, err := runCLI(t, "wi", "list", "-p", "P", "--board", "Team A", "--json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wiql, `[System.AreaPath] UNDER 'P\\Team A'`) {
		t.Errorf("--board should scope to the team's area: %s", wiql)
	}
	if _, err := runCLI(t, "config", "set", "team", "Team B"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "wi", "list", "-p", "P", "--json"); err != nil || !strings.Contains(wiql, "Team B") {
		t.Errorf("configured team should scope: %v %s", err, wiql)
	}
	if _, err := runCLI(t, "wi", "list", "-p", "P", "--team=", "--json"); err != nil || strings.Contains(wiql, "AreaPath") {
		t.Errorf("--team= should query the whole project: %v %s", err, wiql)
	}
	if _, err := runCLI(t, "wi", "list", "-p", "P", "--all", "--open"); err == nil {
		t.Error("expected conflicting flags error")
	}
	if _, err := runCLI(t, "wi", "show", "--no-tui"); err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Errorf("show without id: %v", err)
	}
}

func TestWorkItemRichText(t *testing.T) {
	var reqs []string // "METHOD path body" for each request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs = append(reqs, r.Method+" "+r.URL.Path+" "+strings.TrimSpace(string(b)))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "fields": map[string]any{
			"System.Title": "Hello", "System.WorkItemType": "Bug", "System.TeamProject": "P"}})
	}))
	defer srv.Close()

	t.Chdir(t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TFS_PAT", "x")
	t.Setenv("TFS_ALLOW_INSECURE", "1")
	if _, err := runCLI(t, "config", "set", "collectionUrl", srv.URL+"/Col"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		args        []string
		stdin, want string
		warn        bool // the item is a Bug, so setting only its description warns
	}{
		{[]string{"-d", "a<b\nc"}, "", `"path":"/fields/System.Description","value":"a&lt;b<br/>c"`, true},
		{[]string{"-d", "-"}, "from\n  stdin\n", `"path":"/fields/System.Description","value":"from<br/>&nbsp;&nbsp;stdin"}`, true},
		{[]string{"-d", ""}, "", `"path":"/fields/System.Description","value":""`, false},
		{[]string{"--acceptance-criteria", "x & y"}, "", `"path":"/fields/Microsoft.VSTS.Common.AcceptanceCriteria","value":"x &amp; y"`, false},
		{[]string{"--acceptance-criteria", ""}, "", `"path":"/fields/Microsoft.VSTS.Common.AcceptanceCriteria","value":""`, false},
		{[]string{"--repro-steps", "-"}, "1. open\r\n", `"path":"/fields/Microsoft.VSTS.TCM.ReproSteps","value":"1. open"}`, false},
		{[]string{"-d", "x", "--repro-steps", "y"}, "", `"path":"/fields/Microsoft.VSTS.TCM.ReproSteps","value":"y"`, false},
	} {
		reqs = nil
		_, errOut, err := runCLIWith(t, strings.NewReader(tc.stdin), append([]string{"wi", "update", "7"}, tc.args...)...)
		if err != nil {
			t.Fatalf("%q: %v", tc.args, err)
		}
		if len(reqs) != 1 || !strings.HasPrefix(reqs[0], "PATCH ") || !strings.Contains(reqs[0], tc.want) {
			t.Errorf("%q: %v", tc.args, reqs)
		}
		if warned := strings.Contains(errOut, "--repro-steps"); warned != tc.warn {
			t.Errorf("%q: warning %v, want %v: %q", tc.args, warned, tc.warn, errOut)
		}
	}

	// Bad stdin input fails before any request, even one that resolving @me would send.
	for _, tc := range []struct {
		args       []string
		stdin, err string
	}{
		{[]string{"wi", "update", "7", "-d", "-"}, "\n", "no description on stdin"},
		{[]string{"wi", "update", "7", "--acceptance-criteria", "-"}, "", "no acceptance criteria on stdin"},
		{[]string{"wi", "update", "7", "-a", "@me", "-d", "-", "--acceptance-criteria", "-"}, "x", "only one of"},
		{[]string{"wi", "create", "--title", "X", "-d", "-", "--repro-steps", "-"}, "x", "only one of"},
		{[]string{"wi", "comment", "7", "-"}, " \n", "no comment on stdin"},
	} {
		reqs = nil
		_, _, err := runCLIWith(t, strings.NewReader(tc.stdin), tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.err) || len(reqs) != 0 {
			t.Errorf("%q: %v %v", tc.args, err, reqs)
		}
	}

	reqs = nil
	_, _, err := runCLIWith(t, strings.NewReader("Users can sign in\n"),
		"wi", "create", "-p", "P", "-t", "user-story", "--title", "SSO", "--acceptance-criteria", "-")
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || !strings.HasPrefix(reqs[0], "POST ") ||
		!strings.Contains(reqs[0], `"path":"/fields/Microsoft.VSTS.Common.AcceptanceCriteria","value":"Users can sign in"}`) {
		t.Errorf("create reading stdin: %v", reqs)
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

func TestPullRequestCreateFlagsNeedSubmit(t *testing.T) {
	for _, args := range [][]string{
		{"pr", "create", "--title", "x"},
		{"pr", "create", "--draft"},
		{"pr", "create", "--work-item", "1"},
		{"pr", "create", "--no-push"},
		{"pr", "create", "--fill-commits"},
	} {
		if _, err := runCLI(t, args...); err == nil || !strings.Contains(err.Error(), "requires --submit") {
			t.Errorf("%v: %v", args, err)
		}
	}
	if _, err := runCLI(t, "pr", "create", "--submit", "--print"); err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Errorf("--submit --print: %v", err)
	}
}
