package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// `wi worktree` starts the new branch from the remote's latest default branch, fetched first,
// not from the clone's HEAD.
func TestWorkItemWorktreeFromLatestDefault(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := path.Base(r.URL.Path)
		if !strings.Contains(r.URL.Path, "/_apis/wit/workitems/") || (id != "7" && id != "8") {
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": map[string]int{"7": 7, "8": 8}[id], "fields": map[string]any{
			"System.Title": "Fix login " + id, "System.WorkItemType": "User Story", "System.TeamProject": "P"}})
	}))
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home) // also keeps the user's git config out
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TFS_PAT", "x")
	t.Setenv("TFS_ALLOW_INSECURE", "1")
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com"} {
		t.Setenv(k, v)
	}

	tmp := t.TempDir()
	origin, clone := filepath.Join(tmp, "origin"), filepath.Join(tmp, "clone")
	gitRun(t, tmp, "init", "-q", "-b", "main", origin)
	gitRun(t, origin, "commit", "-q", "--allow-empty", "-m", "init")
	gitRun(t, tmp, "clone", "-q", origin, clone)
	gitRun(t, origin, "commit", "-q", "--allow-empty", "-m", "later") // not fetched by the clone yet
	t.Chdir(clone)
	if _, err := runCLI(t, "config", "set", "collectionUrl", srv.URL+"/Col"); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "wi", "worktree", "7")
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(filepath.Dir(gitRun(t, clone, "rev-parse", "--show-toplevel")), "7-fix-login-7")
	if !strings.Contains(out, "Created worktree "+wt+" on new branch feature/7-fix-login-7") {
		t.Errorf("output: %s", out)
	}
	if s := gitRun(t, wt, "log", "-1", "--format=%s"); s != "later" {
		t.Errorf("worktree starts at %q, want the remote's latest commit", s)
	}
	if s := gitRun(t, clone, "log", "-1", "--format=%s"); s != "init" {
		t.Errorf("the clone's own branch moved to %q", s)
	}
	if up, err := exec.Command("git", "-C", clone, "config", "--get", "branch.feature/7-fix-login-7.merge").Output(); err == nil {
		t.Errorf("new branch should not track the base, tracks %q", up)
	}

	// --base starts it elsewhere, without needing the remote.
	gitRun(t, clone, "remote", "remove", "origin")
	other := filepath.Join(tmp, "other")
	if _, err := runCLI(t, "wi", "worktree", "8", "--base", "main", "--path", other); err != nil {
		t.Fatal(err)
	}
	if s := gitRun(t, other, "log", "-1", "--format=%s"); s != "init" {
		t.Errorf("--base main worktree starts at %q", s)
	}
}
