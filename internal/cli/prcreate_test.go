package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// `pr create --submit` against a real remote whose URL looks like an Azure DevOps one.
func TestPullRequestCreateSubmit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the file://localhost remote needs a Unix path")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	var mu sync.Mutex
	var created []map[string]any
	workItemGets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		var resp any
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/P/_apis/git/repositories/Repo"):
			resp = map[string]any{"id": "rid", "name": "Repo", "defaultBranch": "refs/heads/main"}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/workitems/42"):
			workItemGets++
			resp = map[string]any{"id": 42, "fields": map[string]any{"System.Title": "Reset", "System.WorkItemType": "Task"}}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/P/_apis/git/repositories/rid/pullrequests"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			created = append(created, body)
			resp = map[string]any{"pullRequestId": len(created), "title": body["title"],
				"sourceRefName": body["sourceRefName"], "targetRefName": body["targetRefName"],
				"repository": map[string]any{"name": "Repo", "project": map[string]string{"name": "P"}}}
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(resp)
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
	origin, clone, other := filepath.Join(tmp, "Col", "P", "_git", "Repo"), filepath.Join(tmp, "clone"), filepath.Join(tmp, "other")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, origin, "init", "-q", "--bare", "-b", "main")
	gitRun(t, tmp, "clone", "-q", "file://localhost"+origin, clone)
	gitRun(t, clone, "commit", "-q", "--allow-empty", "-m", "init")
	gitRun(t, clone, "push", "-q", "origin", "main")
	gitRun(t, clone, "switch", "-q", "-c", "feature/42-add-reset")
	gitRun(t, clone, "commit", "-q", "--allow-empty", "-m", "Add reset")
	gitRun(t, clone, "commit", "-q", "--allow-empty", "-m", "Fix reset")
	t.Chdir(clone)
	if _, err := runCLI(t, "config", "set", "collectionUrl", srv.URL+"/Col"); err != nil {
		t.Fatal(err)
	}

	if _, err := runCLI(t, "pr", "create", "--submit", "--work-item", "x"); err == nil || !strings.Contains(err.Error(), "--work-item: invalid work item id") {
		t.Errorf("bad --work-item: %v", err)
	}

	// The branch is pushed and becomes tracked; the named work item replaces the branch's.
	out, errOut, err := runCLIWith(t, nil, "pr", "create", "--submit", "--fill-commits", "--work-item", "#7", "--target", "refs/heads/main")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(out, "Created pull request !1: Add reset") || !strings.Contains(errOut, "Pushed feature/42-add-reset to origin") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", out, errOut)
	}
	want := map[string]any{"title": "Add reset", "description": "- Add reset\n- Fix reset",
		"sourceRefName": "refs/heads/feature/42-add-reset", "targetRefName": "refs/heads/main",
		"workItemRefs": []any{map[string]any{"id": "7"}}}
	if !reflect.DeepEqual(created[0], want) || workItemGets != 0 {
		t.Errorf("request %v (work item reads %d)\nwant %v", created[0], workItemGets, want)
	}
	if up := gitRun(t, clone, "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/feature/42-add-reset" {
		t.Errorf("upstream %q", up)
	}

	// Someone else pushed to the branch: it is not pushed, the pull request is still created.
	gitRun(t, tmp, "clone", "-q", "-b", "feature/42-add-reset", "file://localhost"+origin, other)
	gitRun(t, other, "commit", "-q", "--allow-empty", "-m", "Theirs")
	gitRun(t, other, "push", "-q", "origin", "feature/42-add-reset")
	out, errOut, err = runCLIWith(t, nil, "pr", "create", "--submit")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(out, "Created pull request !2") || strings.Contains(errOut, "Pushed") ||
		!strings.Contains(errOut, "warning: did not push feature/42-add-reset: origin/feature/42-add-reset has commits this branch lacks") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", out, errOut)
	}
	if refs := created[1]["workItemRefs"]; !reflect.DeepEqual(refs, []any{map[string]any{"id": "42"}}) || workItemGets != 1 {
		t.Errorf("branch work item: %v (reads %d)", refs, workItemGets)
	}

	// The TUI's form: prepared from the branch, created only while the branch is unchanged.
	app := &App{out: io.Discard, errOut: io.Discard}
	if _, err := app.api(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prep, err := app.preparePullRequestForTUI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if prep.Commits != 2 || prep.Draft.Title != "Add reset" || !reflect.DeepEqual(prep.Draft.WorkItemIDs, []int{42}) {
		t.Errorf("prep %+v", prep)
	}
	gitRun(t, clone, "switch", "-q", "-c", "elsewhere")
	if _, _, err := prep.Create(ctx, prep.Draft, false); err == nil || !strings.Contains(err.Error(), "changed from feature/42-add-reset to elsewhere") {
		t.Errorf("switched branch: %v", err)
	}
	gitRun(t, clone, "switch", "-q", "feature/42-add-reset")
	d := prep.Draft
	d.Description = "Body"
	pr, warning, err := prep.Create(ctx, d, true)
	if err != nil || pr.ID != 3 || !strings.HasPrefix(warning, "did not push feature/42-add-reset") {
		t.Errorf("create: %+v %q %v", pr, warning, err)
	}
	if got := created[2]["description"]; got != "Body\n\n- Add reset\n- Fix reset" {
		t.Errorf("description %q", got)
	}
}
