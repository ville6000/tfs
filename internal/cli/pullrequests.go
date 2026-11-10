package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ville6000/tfs/internal/ado"
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
	return &cobra.Command{
		Use:   "show [id]",
		Short: "Show a pull request (picker when id is omitted)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pr, err := a.pickPullRequest(cmd.Context(), args, "select a pull request")
			if err != nil {
				return err
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
	var target, title, description string
	var printOnly, submit, draft, noPush, fillCommits bool
	var workItems []int
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Open the create-pull-request page for the current branch, or create the PR with --submit",
		Long: `Open the create-pull-request page for the current branch in the browser, or create the pull
request on the server with --submit.

The repository and project are read from the git remote (the 'remote' config, default origin).
The target is the repository's default branch unless --target is given.

With --submit:
  - the branch is pushed to the remote first when the remote lacks it or is behind (never
    forced; --no-push skips this)
  - the title defaults to the branch name without its prefix (feature/42-add-reset → Add reset)
  - --fill-commits adds the branch's commits that are not on the target branch to the
    description as a Markdown list (after --description, when both are given)
  - the work item in the branch name (per the branchTemplate config, e.g. feature/{id}-{slug})
    is linked, unless --work-item names the work items to link instead

In the interactive pull request list (tfs pr list), press n to fill in the same details in a
form and create the pull request.`,
		Example: `  tfs pr create
  tfs pr create --target develop
  tfs pr create --submit
  tfs pr create --submit --draft --title "Add reset endpoint" --description "Closes the API part"
  tfs pr create --submit --fill-commits
  tfs pr create --submit --work-item 123 --work-item 124`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !submit {
				for _, f := range []string{"title", "description", "fill-commits", "draft", "work-item", "no-push"} {
					if cmd.Flags().Changed(f) {
						return fmt.Errorf("--%s requires --submit", f)
					}
				}
			} else if printOnly {
				return fmt.Errorf("--print and --submit cannot be used together")
			}
			ctx := cmd.Context()
			client, err := a.api()
			if err != nil {
				return err
			}
			pc, err := a.pullRequestContext(ctx, client, target)
			if err != nil {
				return err
			}
			if submit {
				return a.submitPullRequest(ctx, client, pc, submitOptions{
					title: title, description: description, fillCommits: fillCommits, draft: draft,
					workItems: workItems, explicitWorkItems: cmd.Flags().Changed("work-item"), noPush: noPush,
				})
			}
			u := domain.PullRequestCreateURL(client.Collection(), pc.ref, pc.source, pc.target, pc.repo.ID)
			if printOnly {
				a.printf("%s\n", u)
				return nil
			}
			a.printf("%s → %s in %s\n", pc.source, pc.target, pc.ref.Repository)
			return a.openURL(u)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&target, "target", "", "target branch (default: the repository's default branch)")
	fl.BoolVar(&printOnly, "print", false, "print the URL instead of opening it")
	fl.BoolVar(&submit, "submit", false, "create the pull request on the server instead of opening the browser")
	fl.StringVar(&title, "title", "", "pull request title (default: from the branch name; with --submit)")
	fl.StringVar(&description, "description", "", "pull request description (with --submit)")
	fl.BoolVar(&fillCommits, "fill-commits", false, "add the branch's commits to the description as a Markdown list (with --submit)")
	fl.BoolVar(&draft, "draft", false, "create the pull request as a draft (with --submit)")
	fl.IntSliceVar(&workItems, "work-item", nil, "work item to link, repeatable (default: the one in the branch name; with --submit)")
	fl.BoolVar(&noPush, "no-push", false, "do not push the branch before creating the pull request (with --submit)")
	return cmd
}

// prContext is the current branch and the server repository a new pull request is opened in.
type prContext struct {
	g      git.Git
	remote string
	ref    domain.RepositoryRef
	repo   ado.Repository
	source string
	target string
}

// pullRequestContext reads the current branch and remote and looks up the repository. target
// defaults to the repository's default branch.
func (a *App) pullRequestContext(ctx context.Context, client *ado.Client, target string) (prContext, error) {
	cfg, _ := a.config()
	pc := prContext{remote: cfg.String("remote")}
	source, err := pc.g.CurrentBranch(ctx)
	if err != nil {
		return pc, err
	}
	if source == "" || source == "HEAD" {
		return pc, fmt.Errorf("not on a branch (detached HEAD); check out the branch to open a pull request from")
	}
	remoteURL, err := pc.g.RemoteURL(ctx, pc.remote)
	if err != nil {
		return pc, err
	}
	if pc.ref, err = domain.ParseRemote(remoteURL); err != nil {
		return pc, err
	}
	if pc.repo, err = client.GetRepository(ctx, pc.ref.Project, pc.ref.Repository); err != nil {
		return pc, err
	}
	if target = strings.TrimSpace(target); target == "" {
		target = pc.repo.DefaultBranch
	}
	if target == "" {
		target = "main"
	}
	if source == target {
		return pc, fmt.Errorf("the current branch %q is the target branch; nothing to open a pull request for", source)
	}
	pc.source, pc.target = source, target
	return pc, nil
}

// branchWorkItem returns the work item named in the branch per the branchTemplate config, or 0
// with a warning when the branch names one that cannot be read.
func (a *App) branchWorkItem(ctx context.Context, client *ado.Client, branch string) (int, string) {
	cfg, _ := a.config()
	id, ok := domain.WorkItemIDFromBranch(cfg.String("branchTemplate"), branch)
	if !ok {
		return 0, ""
	}
	if _, err := client.GetWorkItem(ctx, id); err != nil {
		return 0, fmt.Sprintf("not linking work item #%d from the branch name: %v", id, err)
	}
	return id, ""
}

// pullRequestDraft is a draft for the context: a title from the branch name and, when
// linkBranchWorkItem, the branch's work item linked.
func (a *App) pullRequestDraft(ctx context.Context, client *ado.Client, pc prContext, linkBranchWorkItem bool) (domain.PullRequestDraft, string, error) {
	cfg, _ := a.config()
	d := domain.PullRequestDraft{
		Project:      pc.ref.Project,
		RepositoryID: pc.repo.ID,
		SourceBranch: pc.source,
		TargetBranch: pc.target,
		Title:        domain.PullRequestTitleFromBranch(cfg.String("branchTemplate"), pc.source),
	}
	if d.Title == "" {
		d.Title = pc.source
	}
	var warning string
	if linkBranchWorkItem {
		var id int
		if id, warning = a.branchWorkItem(ctx, client, pc.source); id > 0 {
			d.WorkItemIDs = []int{id}
		}
	}
	return d, warning, nil
}

// branchCommits returns the commits on the source branch that are not on the remote's target
// branch as a Markdown list, fetching the target first so the comparison is current.
func (a *App) branchCommits(ctx context.Context, pc prContext) (string, error) {
	fetchErr := pc.g.Fetch(ctx, pc.remote, pc.target)
	subjects, err := pc.g.CommitSubjects(ctx, "refs/remotes/"+pc.remote+"/"+pc.target)
	if err != nil && fetchErr != nil {
		return "", fetchErr
	}
	return domain.CommitListMarkdown(subjects), err
}

// createPullRequest pushes the source branch when needed (unless noPush) and creates the pull
// request. pushed reports whether a push happened.
func (a *App) createPullRequest(ctx context.Context, client *ado.Client, pc prContext, d domain.PullRequestDraft, noPush bool) (pr domain.PullRequest, pushed bool, err error) {
	if err := d.Validate(); err != nil {
		return pr, false, err
	}
	if !noPush {
		if pushed, err = pushIfNeeded(ctx, pc.g, pc.remote, d.SourceBranch); err != nil {
			return pr, false, err
		}
	}
	pr, err = client.CreatePullRequest(ctx, d)
	return pr, pushed, err
}

// submitOptions are the `pr create --submit` flags.
type submitOptions struct {
	title, description string
	fillCommits, draft bool
	workItems          []int
	explicitWorkItems  bool // --work-item given: link workItems instead of the branch's
	noPush             bool
}

// submitPullRequest creates the pull request for `pr create --submit`.
func (a *App) submitPullRequest(ctx context.Context, client *ado.Client, pc prContext, o submitOptions) error {
	d, warning, err := a.pullRequestDraft(ctx, client, pc, !o.explicitWorkItems)
	if err != nil {
		return err
	}
	if strings.TrimSpace(o.title) != "" {
		d.Title = o.title
	}
	d.Description = strings.TrimSpace(o.description)
	if o.fillCommits {
		commits, err := a.branchCommits(ctx, pc)
		if err != nil {
			return fmt.Errorf("could not list the branch's commits: %w", err)
		}
		d.Description = domain.JoinDescription(d.Description, commits)
	}
	d.IsDraft = o.draft
	if o.explicitWorkItems {
		d.WorkItemIDs = o.workItems
	}
	if warning != "" {
		fmt.Fprintln(a.errOut, "warning:", warning)
	}
	if err := d.Validate(); err != nil {
		return err
	}
	pr, pushed, err := a.createPullRequest(ctx, client, pc, d, o.noPush)
	if pushed {
		fmt.Fprintf(a.errOut, "Pushed %s to %s\n", d.SourceBranch, pc.remote)
	}
	if err != nil {
		return err
	}
	if a.jsonOut {
		return output.JSON(a.out, pr)
	}
	kind := "pull request"
	if pr.IsDraft {
		kind = "draft pull request"
	}
	a.printf("Created %s !%d: %s\n", kind, pr.ID, pr.Title)
	a.printf("%s → %s in %s\n", pr.SourceBranch, pr.TargetBranch, pr.Repository)
	if len(d.WorkItemIDs) > 0 {
		a.printf("Linked work items: %s\n", domain.FormatWorkItemIDs(d.WorkItemIDs))
	}
	a.printf("%s\n", pr.URL)
	return nil
}

// pushIfNeeded pushes branch when the remote does not have it or points at another commit.
func pushIfNeeded(ctx context.Context, g git.Git, remote, branch string) (bool, error) {
	remoteCommit, err := g.RemoteBranchCommit(ctx, remote, branch)
	if err != nil {
		return false, err
	}
	head, err := g.HeadCommit(ctx)
	if err != nil {
		return false, err
	}
	if remoteCommit == head {
		return false, nil
	}
	return true, g.Push(ctx, remote, branch, remoteCommit == "")
}

// preparePullRequestForTUI is the TUI's view of a new pull request for the current branch.
func (a *App) preparePullRequestForTUI(ctx context.Context) (tui.PullRequestPrep, error) {
	pc, err := a.pullRequestContext(ctx, a.client, "")
	if err != nil {
		return tui.PullRequestPrep{}, err
	}
	d, warning, err := a.pullRequestDraft(ctx, a.client, pc, true)
	if err != nil {
		return tui.PullRequestPrep{}, err
	}
	commits, err := a.branchCommits(ctx, pc)
	if err != nil {
		warning = strings.TrimPrefix(warning+"; could not list the branch's commits: "+err.Error(), "; ")
	}
	return tui.PullRequestPrep{Draft: d, Commits: commits, Repository: pc.repo.Name, Warning: warning}, nil
}

// createPullRequestForTUI creates the TUI's draft from the current branch, which it re-reads so
// a branch switched since the form opened is not pushed under the old name.
func (a *App) createPullRequestForTUI(ctx context.Context, d domain.PullRequestDraft) (domain.PullRequest, error) {
	pc, err := a.pullRequestContext(ctx, a.client, d.TargetBranch)
	if err != nil {
		return domain.PullRequest{}, err
	}
	if pc.source != d.SourceBranch || pc.repo.ID != d.RepositoryID {
		return domain.PullRequest{}, fmt.Errorf("the current branch changed to %s since the form opened; try again", pc.source)
	}
	pr, _, err := a.createPullRequest(ctx, a.client, pc, d, false)
	return pr, err
}
