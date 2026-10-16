package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/git"
	"github.com/ville6000/tfs/internal/output"
	"github.com/ville6000/tfs/internal/tui"
	"github.com/ville6000/tfs/internal/ui"
)

func (a *App) pullRequestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pr",
		Aliases: []string{"pullrequest", "pullrequests"},
		Short:   "Pull requests: list, show, open, checkout, create",
	}
	cmd.AddCommand(a.prListCmd(), a.prShowCmd(), a.prOpenCmd(), a.prCheckoutCmd(), a.prCreateCmd())
	return cmd
}

func (a *App) prListCmd() *cobra.Command {
	q := domain.PullRequestQuery{Status: "Active", Top: domain.DefaultPullRequestTop}
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List pull requests (interactive in a terminal)",
		Example: `  tfs pr list
  tfs pr list --status all --creator alice
  tfs pr list --repo my-repo -n 100`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			client, err := a.api()
			if err != nil {
				return err
			}
			if q.Project, err = a.resolveProject(ctx); err != nil {
				return err
			}
			if err := q.Validate(); err != nil {
				return err
			}
			load := func(ctx context.Context) ([]domain.PullRequest, error) { return client.ListPullRequests(ctx, q) }
			if a.interactive() {
				return tui.Run(tui.NewPullRequestList(q.Project+" pull requests", a.actions(), load))
			}
			prs, err := load(ctx)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return output.JSON(a.out, prs)
			}
			output.PullRequests(a.out, prs, a.width())
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&q.Status, "status", q.Status, "Active, Abandoned, Completed or All")
	fl.StringVar(&q.Creator, "creator", "", "only PRs whose author name contains this text")
	fl.StringVar(&q.Repository, "repo", "", "only PRs in this repository")
	fl.IntVarP(&q.Top, "top", "n", q.Top, fmt.Sprintf("maximum PRs to fetch (1-%d)", domain.MaxPullRequestTop))
	return cmd
}

func (a *App) pickPullRequest(ctx context.Context, args []string, title string) (domain.PullRequest, error) {
	client, err := a.api()
	if err != nil {
		return domain.PullRequest{}, err
	}
	var id int
	if len(args) == 1 {
		if id, err = parseID(args[0]); err != nil {
			return domain.PullRequest{}, err
		}
	} else {
		if !a.interactive() {
			return domain.PullRequest{}, fmt.Errorf("a pull request id is required")
		}
		project, err := a.resolveProject(ctx)
		if err != nil {
			return domain.PullRequest{}, err
		}
		q := domain.PullRequestQuery{Project: project, Status: "Active", Top: domain.MaxPullRequestTop}
		var picked int
		if id, err = tui.Pick(tui.NewPullRequestPicker(title, a.actions(), func(ctx context.Context) ([]domain.PullRequest, error) {
			return client.ListPullRequests(ctx, q)
		}, &picked), &picked); err != nil {
			return domain.PullRequest{}, err
		}
	}
	return client.GetPullRequest(ctx, id)
}

func (a *App) prShowCmd() *cobra.Command {
	var comments bool
	cmd := &cobra.Command{
		Use:   "show [id]",
		Short: "Show a pull request (picker when id is omitted)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pr, err := a.pickPullRequest(cmd.Context(), args, "select a pull request")
			if err != nil {
				return err
			}
			if comments {
				client, err := a.api()
				if err != nil {
					return err
				}
				if pr.Threads, err = client.PullRequestThreads(cmd.Context(), pr.Project, pr.Repository, pr.ID); err != nil {
					return err
				}
			}
			if a.interactive() {
				return tui.Run(tui.NewPullRequestDetail(a.actions(), pr))
			}
			if a.jsonOut {
				return output.JSON(a.out, pr)
			}
			width := a.width()
			if width == 0 {
				width = 100
			}
			ui.Println(a.out, ui.RenderPullRequest(pr, width, a.styled()))
			return nil
		},
	}
	cmd.Flags().BoolVar(&comments, "comments", false, "include comment threads")
	return cmd
}

func (a *App) prOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open [id]",
		Short: "Open a pull request in the browser (picker when id is omitted)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pr, err := a.pickPullRequest(cmd.Context(), args, "open a pull request")
			if err != nil {
				return err
			}
			return a.openURL(pr.URL)
		},
	}
}

func (a *App) prCheckoutCmd() *cobra.Command {
	var worktree bool
	var path string
	cmd := &cobra.Command{
		Use:   "checkout [id]",
		Short: "Fetch and check out a pull request's source branch",
		Example: `  tfs pr checkout 4567
  tfs pr checkout 4567 --worktree            # ../<repo>-pr-4567
  tfs pr checkout 4567 --worktree --path ../review`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pr, err := a.pickPullRequest(cmd.Context(), args, "check out a pull request")
			if err != nil {
				return err
			}
			msg, err := a.checkoutPullRequest(cmd.Context(), pr, worktree || path != "", path)
			if err != nil {
				return err
			}
			a.printf("%s\n", msg)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&worktree, "worktree", "w", false, "create a worktree instead of switching branches")
	cmd.Flags().StringVar(&path, "path", "", "worktree location (implies --worktree)")
	return cmd
}

func (a *App) checkoutPullRequest(ctx context.Context, pr domain.PullRequest, worktree bool, path string) (string, error) {
	cfg, err := a.config()
	if err != nil {
		return "", err
	}
	branch := pr.SourceBranch
	if strings.TrimSpace(branch) == "" {
		return "", fmt.Errorf("pull request !%d has no source branch", pr.ID)
	}
	remote := cfg.String("remote")
	g := git.Git{}
	if err := g.Fetch(ctx, remote, branch); err != nil {
		return "", err
	}
	if !worktree {
		if err := g.Checkout(ctx, remote, branch); err != nil {
			return "", err
		}
		return fmt.Sprintf("Checked out %s (!%d)", branch, pr.ID), nil
	}
	if path == "" {
		root, err := g.Root(ctx)
		if err != nil {
			return "", err
		}
		path = filepath.Join(filepath.Dir(root), fmt.Sprintf("%s-pr-%d", filepath.Base(root), pr.ID))
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if err := g.AddWorktree(ctx, path, remote, branch); err != nil {
		return "", err
	}
	return fmt.Sprintf("Created worktree %s on %s (!%d)", path, branch, pr.ID), nil
}

func (a *App) prCreateCmd() *cobra.Command {
	var target string
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Open the create-pull-request page for the current branch",
		Long: `Open the create-pull-request page for the current branch in the browser.

The repository and project are read from the git remote (the 'remote' config, default origin).
The target is the repository's default branch unless --target is given.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			client, err := a.api()
			if err != nil {
				return err
			}
			cfg, _ := a.config()
			g := git.Git{}
			source, err := g.CurrentBranch(ctx)
			if err != nil {
				return err
			}
			if source == "" || source == "HEAD" {
				return fmt.Errorf("not on a branch (detached HEAD); check out the branch to open a pull request from")
			}
			remoteURL, err := g.RemoteURL(ctx, cfg.String("remote"))
			if err != nil {
				return err
			}
			ref, err := domain.ParseRemote(remoteURL)
			if err != nil {
				return err
			}
			repo, err := client.GetRepository(ctx, ref.Project, ref.Repository)
			if err != nil {
				return err
			}
			if target = strings.TrimSpace(target); target == "" {
				target = repo.DefaultBranch
			}
			if target == "" {
				target = "main"
			}
			if source == target {
				return fmt.Errorf("the current branch %q is the target branch; nothing to open a pull request for", source)
			}
			u := domain.PullRequestCreateURL(client.Collection(), ref, source, target, repo.ID)
			if printOnly {
				a.printf("%s\n", u)
				return nil
			}
			a.printf("%s → %s in %s\n", source, target, ref.Repository)
			return a.openURL(u)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "target branch (default: the repository's default branch)")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the URL instead of opening it")
	return cmd
}
