package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestWorktreeFlows(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin")
	clone := filepath.Join(tmp, "clone")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, origin, "init", "-q", "-b", "main")
	run(t, origin, "commit", "-q", "--allow-empty", "-m", "init")
	run(t, origin, "branch", "feature/pr")
	run(t, tmp, "clone", "-q", origin, clone)

	g := Git{Dir: clone}
	if b, err := g.CurrentBranch(ctx); err != nil || b != "main" {
		t.Fatalf("branch %q %v", b, err)
	}
	if err := g.Fetch(ctx, "origin", "feature/pr"); err != nil {
		t.Fatal(err)
	}
	wt, err := g.SiblingPath(ctx, "clone-pr-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.AddWorktree(ctx, wt, "origin", "feature/pr"); err != nil {
		t.Fatal(err)
	}
	if b, _ := (Git{Dir: wt}).CurrentBranch(ctx); b != "feature/pr" {
		t.Errorf("worktree branch %q", b)
	}
	if err := g.AddWorktreeNewBranch(ctx, filepath.Join(tmp, "new"), "feature/1-x"); err != nil {
		t.Fatal(err)
	}
	if err := g.Checkout(ctx, "origin", "feature/pr"); err == nil {
		t.Error("checkout of a branch used by a worktree should fail")
	}
	if _, err := g.RemoteURL(ctx, "nope"); err == nil {
		t.Error("expected error for missing remote")
	}
}
