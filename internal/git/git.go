// Package git wraps the git commands the CLI needs.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git runs git in Dir (the current directory when empty).
type Git struct {
	Dir string
	// NoPrompt keeps git and ssh from asking for a password or passphrase on the terminal, for
	// running under a full-screen UI that owns it. Commands that need one fail instead.
	NoPrompt bool
}

// Error is a failed git invocation.
type Error struct {
	Args     []string
	ExitCode int
	Stderr   string
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = fmt.Sprintf("exit code %d", e.ExitCode)
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

// run runs git and returns its trimmed stdout, which is also returned when git fails.
func (g Git) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir
	if g.NoPrompt {
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		detachFromTerminal(cmd)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return out, fmt.Errorf("could not run git: %w", err)
		}
		return out, &Error{Args: args, ExitCode: ee.ExitCode(), Stderr: stderr.String()}
	}
	return out, nil
}

// Root returns the top-level directory of the repository.
func (g Git) Root(ctx context.Context) (string, error) {
	return g.run(ctx, "rev-parse", "--show-toplevel")
}

// CurrentBranch returns the checked-out branch name ("HEAD" when detached).
func (g Git) CurrentBranch(ctx context.Context) (string, error) {
	return g.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
}

// RemoteURL returns the URL of a remote.
func (g Git) RemoteURL(ctx context.Context, remote string) (string, error) {
	return g.run(ctx, "remote", "get-url", remote)
}

// CommitSubjects returns the subjects of the non-merge commits in HEAD but not in base,
// oldest first.
func (g Git) CommitSubjects(ctx context.Context, base string) ([]string, error) {
	// --no-show-signature: log.showSignature would mix gpg output into the subjects.
	out, err := g.run(ctx, "log", "--reverse", "--no-merges", "--no-show-signature", "--format=%s", base+"..HEAD")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// PushResult is what a push did to the remote branch.
type PushResult int

// Push results.
const (
	PushUpToDate PushResult = iota // the remote branch already had the commit
	PushCreated                    // the remote branch was created
	PushUpdated                    // the remote branch was fast-forwarded
	PushRejected                   // the remote branch has commits the local one lacks; nothing was pushed
)

// Push pushes branch to the same-named branch on remote, never forced. A branch new on the
// remote gets it as its upstream. When the remote branch has commits the local branch lacks the
// push is refused, which is reported as PushRejected rather than an error.
func (g Git) Push(ctx context.Context, remote, branch string) (PushResult, error) {
	ref := "refs/heads/" + branch
	out, err := g.run(ctx, "push", "--porcelain", remote, ref+":"+ref)
	// --porcelain prints "<flag>\t<from>:<to>\t<summary>" for each ref.
	for _, line := range strings.Split(out, "\n") {
		flag, rest, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasPrefix(rest, ref+":"+ref+"\t") {
			continue
		}
		switch strings.TrimSpace(flag) {
		case "=":
			return PushUpToDate, nil
		case "*":
			return PushCreated, g.setUpstream(ctx, remote, branch)
		case "": // fast-forward
			return PushUpdated, nil
		case "!":
			if strings.Contains(rest, "(fetch first)") || strings.Contains(rest, "(non-fast-forward)") {
				return PushRejected, nil
			}
		}
	}
	if err == nil {
		err = fmt.Errorf("git push did not report on %s: %s", ref, out)
	}
	return PushUpToDate, err
}

// setUpstream makes remote's branch the upstream of the local branch, as push --set-upstream
// does, without needing a remote-tracking ref.
func (g Git) setUpstream(ctx context.Context, remote, branch string) error {
	if _, err := g.run(ctx, "config", "branch."+branch+".remote", remote); err != nil {
		return err
	}
	_, err := g.run(ctx, "config", "branch."+branch+".merge", "refs/heads/"+branch)
	return err
}

// Fetch fetches a branch from a remote, updating the remote-tracking ref.
func (g Git) Fetch(ctx context.Context, remote, branch string) error {
	_, err := g.run(ctx, "fetch", remote, fmt.Sprintf("+refs/heads/%s:refs/remotes/%s/%s", branch, remote, branch))
	return err
}

// LocalBranchExists reports whether refs/heads/<branch> exists.
func (g Git) LocalBranchExists(ctx context.Context, branch string) bool {
	_, err := g.run(ctx, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// Checkout switches to branch, creating a tracking branch from remote when it does not exist
// locally.
func (g Git) Checkout(ctx context.Context, remote, branch string) error {
	if g.LocalBranchExists(ctx, branch) {
		_, err := g.run(ctx, "checkout", branch)
		return err
	}
	_, err := g.run(ctx, "checkout", "-b", branch, "--track", remote+"/"+branch)
	return err
}

// AddWorktree adds a worktree at path for an existing remote branch.
func (g Git) AddWorktree(ctx context.Context, path, remote, branch string) error {
	if g.LocalBranchExists(ctx, branch) {
		_, err := g.run(ctx, "worktree", "add", path, branch)
		return err
	}
	_, err := g.run(ctx, "worktree", "add", "--track", "-b", branch, path, remote+"/"+branch)
	return err
}

// AddWorktreeNewBranch adds a worktree at path with a new branch from HEAD.
func (g Git) AddWorktreeNewBranch(ctx context.Context, path, branch string) error {
	_, err := g.run(ctx, "worktree", "add", path, "-b", branch)
	return err
}

// DefaultBranch returns the remote's default branch name (e.g. "main"), from the remote's HEAD
// when the clone knows it, otherwise the first of main/master/develop that exists on the remote.
func (g Git) DefaultBranch(ctx context.Context, remote string) (string, error) {
	if ref, err := g.run(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD"); err == nil && ref != "" {
		return strings.TrimPrefix(ref, remote+"/"), nil
	}
	for _, b := range []string{"main", "master", "develop"} {
		if _, err := g.run(ctx, "show-ref", "--verify", "--quiet", "refs/remotes/"+remote+"/"+b); err == nil {
			return b, nil
		}
	}
	return "", fmt.Errorf("cannot tell the default branch of %s; run `git remote set-head %s --auto` or pass --base", remote, remote)
}

// SwitchNewBranch creates branch from base and switches to it. When branch already exists it
// only switches to it, and created is false.
func (g Git) SwitchNewBranch(ctx context.Context, branch, base string) (created bool, err error) {
	if g.LocalBranchExists(ctx, branch) {
		_, err := g.run(ctx, "switch", branch)
		return false, err
	}
	// --no-track: the new branch must not track the base (e.g. origin/main) for pushes.
	_, err = g.run(ctx, "switch", "--no-track", "-c", branch, base)
	return err == nil, err
}

// SiblingPath returns <parent of repo root>/<name>, the default location for new worktrees.
func (g Git) SiblingPath(ctx context.Context, name string) (string, error) {
	root, err := g.Root(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(root), name), nil
}
