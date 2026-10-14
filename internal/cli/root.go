// Package cli defines the tfs command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/ville6000/tfs/internal/ado"
	"github.com/ville6000/tfs/internal/browser"
	"github.com/ville6000/tfs/internal/config"
	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/git"
	"github.com/ville6000/tfs/internal/tui"
)

// App carries state shared by all commands.
type App struct {
	out     io.Writer
	errOut  io.Writer
	noTUI   bool
	jsonOut bool
	project string

	cfg    *config.Config
	client *ado.Client
}

// Execute runs the CLI and returns the process exit code.
func Execute(version string) int {
	app := &App{out: os.Stdout, errOut: os.Stderr}
	root := app.rootCmd(version)
	if err := root.Execute(); err != nil {
		if errors.Is(err, tui.ErrAborted) {
			return 130
		}
		fmt.Fprintln(app.errOut, "error:", err)
		return 1
	}
	return 0
}

func (a *App) rootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "tfs",
		Short: "A terminal client for Azure DevOps Server work items, boards and pull requests",
		Long: `tfs is a terminal client for on-premises Azure DevOps Server (formerly TFS).

Browse, create and update work items, view the backlog hierarchy, list and check out pull
requests, and create git worktrees straight from a work item. Lists, trees and detail views
are interactive when run in a terminal; pass --no-tui or --json for scripting.

Authentication uses a personal access token from the TFS_PAT environment variable.
Run 'tfs config wizard' to get started.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&a.noTUI, "no-tui", false, "print static output instead of the interactive view")
	root.PersistentFlags().BoolVar(&a.jsonOut, "json", false, "print JSON (implies --no-tui)")
	root.PersistentFlags().StringVarP(&a.project, "project", "p", "", "team project (default: defaultProject config, then the git remote)")
	root.AddCommand(a.workItemCmd(), a.pullRequestCmd(), a.buildCmd(), a.releaseCmd(), a.boardCmd(), a.configCmd())
	return root
}

func (a *App) config() (*config.Config, error) {
	if a.cfg != nil {
		return a.cfg, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintln(a.errOut, "warning:", w)
	}
	a.cfg = cfg
	return cfg, nil
}

func (a *App) api() (*ado.Client, error) {
	if a.client != nil {
		return a.client, nil
	}
	cfg, err := a.config()
	if err != nil {
		return nil, err
	}
	conn, err := cfg.Connection()
	if err != nil {
		return nil, err
	}
	a.client = ado.New(conn.CollectionURL, conn.APIVersion, conn.PAT)
	a.client.UserIdentity = cfg.String("userIdentity")
	return a.client, nil
}

// resolveProject returns --project, the configured default, or the project of the git remote.
func (a *App) resolveProject(ctx context.Context) (string, error) {
	if p := strings.TrimSpace(a.project); p != "" {
		return p, nil
	}
	cfg, err := a.config()
	if err != nil {
		return "", err
	}
	if p := cfg.String("defaultProject"); p != "" {
		return p, nil
	}
	if remote, err := (git.Git{}).RemoteURL(ctx, cfg.String("remote")); err == nil {
		if ref, err := domain.ParseRemote(remote); err == nil {
			return ref.Project, nil
		}
	}
	return "", errors.New("no project: pass --project, or set one with `tfs config set defaultProject <name>`")
}

func (a *App) interactive() bool {
	return !a.noTUI && !a.jsonOut && term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

func (a *App) width() int {
	if f, ok := a.out.(*os.File); ok && term.IsTerminal(f.Fd()) {
		if w, _, err := term.GetSize(f.Fd()); err == nil {
			return w
		}
	}
	return 0
}

func (a *App) styled() bool {
	f, ok := a.out.(*os.File)
	return ok && term.IsTerminal(f.Fd()) && os.Getenv("NO_COLOR") == ""
}

func (a *App) actions() *tui.Actions {
	return &tui.Actions{
		Client: a.client,
		Open:   browser.Open,
		Worktree: func(ctx context.Context, w domain.WorkItem) (string, string, error) {
			return a.workItemWorktree(ctx, w, "")
		},
		Branch: func(ctx context.Context, w domain.WorkItem) (string, error) {
			return a.workItemBranch(ctx, w, "")
		},
		CheckoutPR: func(ctx context.Context, pr domain.PullRequest, worktree bool) (string, error) {
			return a.checkoutPullRequest(ctx, pr, worktree, "")
		},
		Team: a.team,
	}
}

// team returns the configured team, or the project's default team.
func (a *App) team(ctx context.Context, project string) (string, error) {
	cfg, err := a.config()
	if err != nil {
		return "", err
	}
	if t := cfg.String("team"); t != "" {
		return t, nil
	}
	return a.client.DefaultTeam(ctx, project)
}

func (a *App) printf(format string, args ...any) { fmt.Fprintf(a.out, format, args...) }

// parseID parses "123" or "#123" / "!123".
func parseID(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimLeft(strings.TrimSpace(s), "#!"))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid id %q: must be a positive number", s)
	}
	return n, nil
}

func (a *App) openURL(url string) error {
	a.printf("Opening %s\n", url)
	return browser.Open(url)
}
