package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ville6000/tfs/internal/domain"
)

// `pr checkout`, with or without --worktree, brings a stale local copy of the source branch up
// to date.
func TestPullRequestCheckoutFastForwards(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		branch := map[string]string{"5": "feature/pr", "6": "feature/other"}[path.Base(r.URL.Path)]
		if !strings.Contains(r.URL.Path, "/_apis/git/pullrequests/") || branch == "" {
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"pullRequestId": map[string]int{"feature/pr": 5, "feature/other": 6}[branch],
			"status": "active", "title": "Review me", "sourceRefName": "refs/heads/" + branch, "targetRefName": "refs/heads/main"})
	}))
	defer srv.Close()
	gitTestEnv(t)

	tmp := t.TempDir()
	origin, clone := filepath.Join(tmp, "origin"), filepath.Join(tmp, "clone")
	gitRun(t, tmp, "init", "-q", "-b", "main", origin)
	gitRun(t, origin, "commit", "-q", "--allow-empty", "-m", "init")
	for _, b := range []string{"feature/pr", "feature/other"} {
		gitRun(t, origin, "branch", b)
	}
	gitRun(t, tmp, "clone", "-q", origin, clone)
	for _, b := range []string{"feature/pr", "feature/other"} {
		gitRun(t, clone, "branch", "-q", "--track", b, "origin/"+b)
		gitRun(t, origin, "switch", "-q", b)
		gitRun(t, origin, "commit", "-q", "--allow-empty", "-m", "review fix on "+b)
	}
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
	if s := gitRun(t, wt, "log", "-1", "--format=%s"); s != "review fix on feature/pr" {
		t.Errorf("worktree at %q, want the pull request's latest commit", s)
	}

	out, err = runCLI(t, "pr", "checkout", "6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Checked out feature/other (!6), fast-forwarded to origin/feature/other") {
		t.Errorf("output: %s", out)
	}
	if s := gitRun(t, clone, "log", "-1", "--format=%s"); s != "review fix on feature/other" {
		t.Errorf("checkout at %q, want the pull request's latest commit", s)
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

// The TUI's git actions never wait for a password: the TUI owns the terminal, so git must fail
// instead of prompting on it.
func TestTUIGitActionsDoNotPrompt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// A remote that asks for credentials.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="tfs"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	gitTestEnv(t)
	// No credential helper or askpass program may answer in git's place.
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_ASKPASS", "")
	t.Setenv("SSH_ASKPASS", "")

	tmp := t.TempDir()
	origin, clone := filepath.Join(tmp, "origin"), filepath.Join(tmp, "clone")
	gitRun(t, tmp, "init", "-q", "-b", "main", origin)
	gitRun(t, origin, "commit", "-q", "--allow-empty", "-m", "init")
	gitRun(t, tmp, "clone", "-q", origin, clone)
	gitRun(t, clone, "remote", "set-url", "origin", srv.URL+"/repo.git")
	t.Chdir(clone)

	act := (&App{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}).actions()
	w := domain.WorkItem{ID: 7, Title: "Fix login", Type: "User Story"}
	pr := domain.PullRequest{ID: 5, SourceBranch: "feature/pr"}
	for name, run := range map[string]func(context.Context) error{
		"branch": func(ctx context.Context) error { _, err := act.Branch(ctx, w); return err },
		"worktree": func(ctx context.Context) error {
			_, _, err := act.Worktree(ctx, w)
			return err
		},
		"checkout":    func(ctx context.Context) error { _, err := act.CheckoutPR(ctx, pr, false); return err },
		"pr worktree": func(ctx context.Context) error { _, err := act.CheckoutPR(ctx, pr, true); return err },
	} {
		// A prompt would block until the timeout kills git.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := run(ctx)
		cancel()
		if err == nil || !strings.Contains(err.Error(), "terminal prompts disabled") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
