package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestBranchFromDefault(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	tmp := t.TempDir()
	origin, clone := filepath.Join(tmp, "origin"), filepath.Join(tmp, "clone")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, origin, "init", "-q", "-b", "trunk")
	run(t, origin, "commit", "-q", "--allow-empty", "-m", "init")
	run(t, tmp, "clone", "-q", origin, clone)
	run(t, clone, "switch", "-q", "-c", "elsewhere")

	g := Git{Dir: clone}
	def, err := g.DefaultBranch(ctx, "origin")
	if err != nil || def != "trunk" {
		t.Fatalf("default %q %v", def, err)
	}
	created, err := g.SwitchNewBranch(ctx, "feature/1-x", "origin/trunk")
	if err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	if b, _ := g.CurrentBranch(ctx); b != "feature/1-x" {
		t.Errorf("on %q", b)
	}
	if up, err := g.run(ctx, "config", "--get", "branch.feature/1-x.merge"); err == nil {
		t.Errorf("new branch should not track the base, tracks %q", up)
	}
	run(t, clone, "switch", "-q", "elsewhere")
	created, err = g.SwitchNewBranch(ctx, "feature/1-x", "origin/trunk")
	if err != nil || created {
		t.Fatalf("existing branch should just be switched to: %v %v", created, err)
	}
	if b, _ := g.CurrentBranch(ctx); b != "feature/1-x" {
		t.Errorf("on %q", b)
	}

	// Without origin/HEAD, fall back to a well-known branch name that exists on the remote.
	run(t, clone, "remote", "set-head", "origin", "--delete")
	if _, err := g.DefaultBranch(ctx, "origin"); err == nil {
		t.Error("trunk is not a well-known name; expected an error asking for --base")
	}
}

func TestPush(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	tmp := t.TempDir()
	origin, clone, other := filepath.Join(tmp, "origin"), filepath.Join(tmp, "clone"), filepath.Join(tmp, "other")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, origin, "init", "-q", "--bare", "-b", "main")
	run(t, tmp, "clone", "-q", origin, clone)
	run(t, clone, "commit", "-q", "--allow-empty", "-m", "init")
	run(t, clone, "push", "-q", "origin", "main")
	run(t, clone, "switch", "-q", "-c", "feature/a")
	run(t, clone, "config", "log.showSignature", "true")
	run(t, clone, "commit", "-q", "--allow-empty", "-m", "Add a thing")

	g := Git{Dir: clone, NoPrompt: true}
	run(t, clone, "commit", "-q", "--allow-empty", "-m", "Then another")
	if s, err := g.CommitSubjects(ctx, "origin/main"); err != nil || strings.Join(s, "|") != "Add a thing|Then another" {
		t.Fatalf("subjects %q %v", s, err)
	}
	if s, err := g.CommitSubjects(ctx, "HEAD"); err != nil || s != nil {
		t.Fatalf("no commits: %q %v", s, err)
	}

	push := func(want PushResult) {
		t.Helper()
		if got, err := g.Push(ctx, "origin", "feature/a"); err != nil || got != want {
			t.Fatalf("push = %v, %v; want %v", got, err, want)
		}
	}
	push(PushCreated)
	if up, err := g.run(ctx, "rev-parse", "--abbrev-ref", "@{upstream}"); err != nil || up != "origin/feature/a" {
		t.Errorf("upstream %q %v", up, err)
	}
	push(PushUpToDate)
	run(t, clone, "commit", "-q", "--allow-empty", "-m", "More")
	push(PushUpdated)

	// Someone else pushes to the branch: the remote is ahead, then the branches have diverged.
	run(t, tmp, "clone", "-q", "-b", "feature/a", origin, other)
	run(t, other, "commit", "-q", "--allow-empty", "-m", "Theirs")
	run(t, other, "push", "-q", "origin", "feature/a")
	push(PushRejected)
	run(t, clone, "commit", "-q", "--allow-empty", "-m", "Mine")
	push(PushRejected)
	run(t, clone, "fetch", "-q", "origin")
	push(PushRejected) // non-fast-forward rather than fetch first, now the clone knows the commit

	// A refusal by the server is an error, not a rejection to skip past.
	hook := filepath.Join(origin, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho no pushes today >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, clone, "switch", "-q", "-c", "feature/b")
	if _, err := g.Push(ctx, "origin", "feature/b"); err == nil || !strings.Contains(err.Error(), "no pushes today") {
		t.Errorf("hook refusal: %v", err)
	}
}
