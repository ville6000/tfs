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

// `pr checkout --worktree` brings a stale local copy of the source branch up to date.
func TestPullRequestWorktreeFastForwards(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasSuffix(r.URL.Path, "/_apis/git/pullrequests/5") {
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"pullRequestId": 5, "status": "active", "title": "Review me",
			"sourceRefName": "refs/heads/feature/pr", "targetRefName": "refs/heads/main"})
	}))
	defer srv.Close()
	gitTestEnv(t)

	tmp := t.TempDir()
	origin, clone := filepath.Join(tmp, "origin"), filepath.Join(tmp, "clone")
	gitRun(t, tmp, "init", "-q", "-b", "main", origin)
	gitRun(t, origin, "commit", "-q", "--allow-empty", "-m", "init")
	gitRun(t, origin, "branch", "feature/pr")
	gitRun(t, tmp, "clone", "-q", origin, clone)
	gitRun(t, clone, "branch", "-q", "--track", "feature/pr", "origin/feature/pr")
	gitRun(t, origin, "switch", "-q", "feature/pr")
	gitRun(t, origin, "commit", "-q", "--allow-empty", "-m", "review fix")
	t.Chdir(clone)
	if _, err := runCLI(t, "config", "set", "collectionUrl", srv.URL+"/Col"); err != nil {
		t.Fatal(err)
	}

	wt := filepath.Join(tmp, "review")
	out, err := runCLI(t, "pr", "checkout", "5", "--path", wt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "on feature/pr (!5), fast-forwarded to origin/feature/pr") {
		t.Errorf("output: %s", out)
	}
	if s := gitRun(t, wt, "log", "-1", "--format=%s"); s != "review fix" {
		t.Errorf("worktree at %q, want the pull request's latest commit", s)
	}
}

// gitTestEnv points HOME at a temporary directory, which also keeps the user's git config out,
// and sets the token, insecure-server and git identity variables the tests need.
func gitTestEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TFS_PAT", "x")
	t.Setenv("TFS_ALLOW_INSECURE", "1")
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com"} {
		t.Setenv(k, v)
	}
}

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
	gitTestEnv(t)

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
