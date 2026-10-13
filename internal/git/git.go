// Package git wraps the git commands the CLI needs.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git runs git in Dir (the current directory when empty).
type Git struct {
	Dir string
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

func (g Git) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return "", fmt.Errorf("could not run git: %w", err)
		}
		return "", &Error{Args: args, ExitCode: ee.ExitCode(), Stderr: stderr.String()}
	}
	return strings.TrimSpace(stdout.String()), nil
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

// SiblingPath returns <parent of repo root>/<name>, the default location for new worktrees.
func (g Git) SiblingPath(ctx context.Context, name string) (string, error) {
	root, err := g.Root(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(root), name), nil
}
