package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// `pr list` and `pr show` mark an active pull request with merge conflicts.
func TestPullRequestListAndShowMarkConflicts(t *testing.T) {
	pr := func(id int, status, title, merge string) map[string]any {
		return map[string]any{"pullRequestId": id, "status": status, "title": title, "mergeStatus": merge,
			"sourceRefName": "refs/heads/feature/x", "targetRefName": "refs/heads/main",
			"createdBy": map[string]string{"displayName": "Bob"}, "repository": map[string]any{"name": "web"}}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/P/_apis/git/pullrequests"):
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []any{
				pr(5, "active", "Fix login", "conflicts"), pr(6, "abandoned", "Old idea", "conflicts"),
			}})
		case strings.HasSuffix(r.URL.Path, "/_apis/git/pullrequests/5"):
			_ = json.NewEncoder(w).Encode(pr(5, "active", "Fix login", "conflicts"))
		default:
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	t.Chdir(t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TFS_PAT", "x")
	t.Setenv("TFS_ALLOW_INSECURE", "1")
	for _, kv := range [][]string{{"collectionUrl", srv.URL + "/Col"}, {"defaultProject", "P"}} {
		if _, err := runCLI(t, "config", "set", kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runCLI(t, "pr", "list", "--status", "all", "--no-tui")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[conflicts] Fix login") || strings.Contains(out, "[conflicts] Old idea") {
		t.Errorf("list:\n%s", out)
	}
	out, err = runCLI(t, "pr", "list", "--json")
	if err != nil || !strings.Contains(out, `"mergeStatus": "conflicts"`) {
		t.Errorf("json: %v\n%s", err, out)
	}

	out, err = runCLI(t, "pr", "show", "5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "!5 Fix login [conflicts]\n") {
		t.Errorf("show:\n%s", out)
	}
}
