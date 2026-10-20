package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ville6000/tfs/internal/ado"
	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/git"
	"github.com/ville6000/tfs/internal/output"
	"github.com/ville6000/tfs/internal/tui"
	"github.com/ville6000/tfs/internal/ui"
)

// filterFlags are the type/state filters shared by `wi list` and `wi tree`.
type filterFlags struct {
	types, noTypes, states, noStates []string
	all, open, resolved              bool
	top                              int
	team                             string
}

func (f *filterFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringArrayVarP(&f.types, "type", "t", nil, "only these types (slugs ok: user-story); repeatable")
	fl.StringArrayVarP(&f.noTypes, "no-type", "T", nil, "exclude these types; repeatable")
	fl.StringArrayVarP(&f.states, "state", "s", nil, "only these states (slugs ok: in-progress); repeatable")
	fl.StringArrayVarP(&f.noStates, "no-state", "S", nil, "exclude these states; repeatable")
	fl.BoolVar(&f.all, "all", false, "include every state (default hides Closed and Removed)")
	fl.BoolVar(&f.open, "open", false, "only open items (not Resolved/Closed/Done/Completed/Removed)")
	fl.BoolVar(&f.resolved, "resolved", false, "only finished items (Resolved/Closed/Done/Completed)")
	fl.IntVarP(&f.top, "top", "n", domain.MaxWorkItemTop, fmt.Sprintf("maximum items to fetch (1-%d)", domain.MaxWorkItemTop))
	fl.StringVar(&f.team, "team", "", "only items on this team's board (alias --board; default: the team config; --team= for the whole project)")
	fl.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "board" {
			name = "team"
		}
		return pflag.NormalizedName(name)
	})
}

// scopeTeam returns the team whose board the query is limited to: --team when given (empty means
// the whole project), otherwise the configured team.
func (f *filterFlags) scopeTeam(cmd *cobra.Command, a *App) (string, error) {
	if cmd.Flags().Changed("team") {
		return strings.TrimSpace(f.team), nil
	}
	cfg, err := a.config()
	if err != nil {
		return "", err
	}
	return cfg.String("team"), nil
}

// teamScope fetches the board scope of team, or nil when team is empty.
func teamScope(ctx context.Context, client *ado.Client, project, team string) (*domain.TeamScope, error) {
	if team == "" {
		return nil, nil
	}
	s, err := client.TeamScope(ctx, project, team)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (f *filterFlags) stateFilters(hideByDefault bool) (inc, exc []string, err error) {
	scope, err := domain.ScopeFromFlags(f.all, f.open, f.resolved, f.states, f.noStates)
	if err != nil {
		return nil, nil, err
	}
	inc, exc = domain.ResolveStateFilters(scope, f.states, f.noStates, hideByDefault)
	return inc, exc, nil
}

func (a *App) workItemCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "wi",
		Aliases: []string{"workitem", "workitems"},
		Short:   "Work items: list, tree, show, create, update, comment, branch, worktree",
	}
	cmd.AddCommand(a.wiListCmd(), a.wiTreeCmd(), a.wiShowCmd(), a.wiOpenCmd(), a.wiCreateCmd(),
		a.wiUpdateCmd(), a.wiCommentCmd(), a.wiBranchCmd(), a.wiWorktreeCmd())
	return cmd
}

func (a *App) wiListCmd() *cobra.Command {
	var f filterFlags
	var assigned string
	var tags []string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List work items (interactive in a terminal)",
		Example: `  tfs wi list
  tfs wi list -t bug -s active -a @me
  tfs wi list --tag backend --resolved --json
  tfs wi list --board "Team A"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			client, err := a.api()
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx)
			if err != nil {
				return err
			}
			inc, exc, err := f.stateFilters(true)
			if err != nil {
				return err
			}
			q := domain.WorkItemQuery{
				Project: project, Types: domain.ResolveSlugs(f.types), ExcludedTypes: domain.ResolveSlugs(f.noTypes),
				States: inc, ExcludedStates: exc, AssignedTo: assigned, Tags: tags, Top: f.top,
			}
			if err := q.Validate(); err != nil {
				return err
			}
			team, err := f.scopeTeam(cmd, a)
			if err != nil {
				return err
			}
			load := func(ctx context.Context) ([]domain.WorkItem, error) {
				if q.Scope == nil {
					scope, err := teamScope(ctx, client, project, team)
					if err != nil {
						return nil, err
					}
					q.Scope = scope
				}
				return client.ListWorkItems(ctx, q)
			}
			if a.interactive() {
				return tui.Run(tui.NewWorkItemList(project, a.actions(), load))
			}
			items, err := load(ctx)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return output.JSON(a.out, items)
			}
			output.WorkItems(a.out, items, a.width())
			return nil
		},
	}
	f.register(cmd)
	cmd.Flags().StringVarP(&assigned, "assigned-to", "a", "", "filter by assignee ('@me' for yourself)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "require tag; repeatable (AND)")
	return cmd
}

func (a *App) wiTreeCmd() *cobra.Command {
	var f filterFlags
	cmd := &cobra.Command{
		Use:   "tree [id]",
		Short: "Show the work item hierarchy (Epic → Feature → Story → Task)",
		Long: `Show the work item hierarchy.

Without an id, the whole project is shown, or with --team (alias --board) the items on that
team's board; with an id, that item and its ancestors.
Ancestors of matching items stay visible (dimmed) so the hierarchy stays connected.
Test Case, Test Plan and Test Suite are hidden unless requested with --type.`,
		Example: `  tfs wi tree
  tfs wi tree 1234
  tfs wi tree -t epic -t feature -t user-story
  tfs wi tree -T task -S removed
  tfs wi tree --board "Team A"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, err := a.api()
			if err != nil {
				return err
			}
			id := 0
			if len(args) == 1 {
				if id, err = parseID(args[0]); err != nil {
					return err
				}
			}
			inc, exc, err := f.stateFilters(id == 0)
			if err != nil {
				return err
			}
			types := domain.ResolveSlugs(f.types)
			noTypes := domain.ResolveSlugs(f.noTypes)
			for _, d := range domain.DefaultTreeExcludedTypes {
				if !containsFold(types, d) {
					noTypes = append(noTypes, d)
				}
			}
			filter := domain.NewFilter(types, noTypes, inc, exc)

			var load func(context.Context) (domain.Tree, error)
			actions := a.actions()
			title := ""
			if id > 0 {
				title = fmt.Sprintf("tree #%d", id)
				load = func(ctx context.Context) (domain.Tree, error) {
					chain, err := client.AncestorChain(ctx, id)
					if err != nil {
						return domain.Tree{}, err
					}
					return domain.BuildChain(chain, filter), nil
				}
			} else {
				project, err := a.resolveProject(ctx)
				if err != nil {
					return err
				}
				title = project + " tree"
				q := domain.WorkItemQuery{Project: project, Types: types, ExcludedTypes: noTypes, States: inc, ExcludedStates: exc, Top: f.top}
				if err := q.Validate(); err != nil {
					return err
				}
				team, err := f.scopeTeam(cmd, a)
				if err != nil {
					return err
				}
				if team != "" {
					title = project + " · " + team + " tree"
					// Reorder on the backlog of the team whose board is shown.
					actions.Team = func(context.Context, string) (string, error) { return team, nil }
				}
				load = func(ctx context.Context) (domain.Tree, error) {
					if q.Scope == nil {
						scope, err := teamScope(ctx, client, project, team)
						if err != nil {
							return domain.Tree{}, err
						}
						q.Scope = scope
					}
					// Siblings follow the backlog order when the server reports which field holds
					// it; otherwise they fall back to id order and cannot be reordered.
					if q.OrderField == "" {
						q.OrderField, _ = client.BacklogOrderField(ctx, project, team)
					}
					items, err := client.ListHierarchy(ctx, q)
					if err != nil {
						return domain.Tree{}, err
					}
					tree := domain.BuildForest(items, filter)
					tree.Ordered = q.OrderField != ""
					return tree, nil
				}
			}
			if a.interactive() {
				return tui.Run(tui.NewTreeView(title, actions, load))
			}
			tree, err := load(ctx)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return output.JSON(a.out, tree)
			}
			if tree.IsEmpty() {
				fmt.Fprintln(a.errOut, "No work items match.")
				return nil
			}
			output.Tree(a.out, tree)
			return nil
		},
	}
	f.register(cmd)
	return cmd
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

// pickWorkItem resolves an id argument, or shows a picker of open items when it is omitted.
func (a *App) pickWorkItem(ctx context.Context, args []string, title string) (int, error) {
	if len(args) == 1 {
		return parseID(args[0])
	}
	if !a.interactive() {
		return 0, fmt.Errorf("a work item id is required")
	}
	client, err := a.api()
	if err != nil {
		return 0, err
	}
	project, err := a.resolveProject(ctx)
	if err != nil {
		return 0, err
	}
	q := domain.WorkItemQuery{Project: project, ExcludedStates: domain.DefaultHiddenStates, Top: 200}
	var picked int
	return tui.Pick(tui.NewWorkItemPicker(title, a.actions(), func(ctx context.Context) ([]domain.WorkItem, error) {
		return client.ListWorkItems(ctx, q)
	}, &picked), &picked)
}

func (a *App) wiShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [id]",
		Short: "Show a work item with description and comments (picker when id is omitted)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, err := a.api()
			if err != nil {
				return err
			}
			id, err := a.pickWorkItem(ctx, args, "select a work item")
			if err != nil {
				return err
			}
			if a.interactive() {
				return tui.Run(tui.NewWorkItemDetail(a.actions(), id))
			}
			d, err := client.GetWorkItemDetail(ctx, id)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return output.JSON(a.out, d)
			}
			width := a.width()
			if width == 0 {
				width = 100
			}
			ui.Println(a.out, ui.RenderWorkItem(d, client.Collection(), width, a.styled()))
			return nil
		},
	}
}

func (a *App) wiOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open [id]",
		Short: "Open a work item in the browser (picker when id is omitted)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.api()
			if err != nil {
				return err
			}
			id, err := a.pickWorkItem(cmd.Context(), args, "open a work item")
			if err != nil {
				return err
			}
			w, err := client.GetWorkItem(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.openURL(w.URL)
		},
	}
}

func (a *App) wiCreateCmd() *cobra.Command {
	var typ, title, desc, assigned string
	var tags []string
	var parent int
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a work item (interactive form when --title is omitted)",
		Example: `  tfs wi create -t bug --title "Login fails on Safari"
  tfs wi create -t task --title "Write docs" --parent 1234 -a @me
  tfs wi create`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			client, err := a.api()
			if err != nil {
				return err
			}
			cfg, _ := a.config()
			project, err := a.resolveProject(ctx)
			if err != nil {
				return err
			}
			if typ == "" && parent > 0 {
				// Pick the type that naturally sits under the parent (Epic → Feature → User Story/PBI
				// → Task) unless the user configured a defaultType.
				if v, _ := cfg.Get("defaultType"); v.Source == "default" {
					if typ, err = childTypeFor(ctx, client, parent); err != nil {
						return err
					}
				}
			}
			if typ == "" {
				typ = cfg.String("defaultType")
			}
			if strings.TrimSpace(title) == "" {
				if !a.interactive() {
					return fmt.Errorf("--title is required")
				}
				v := tui.CreateValues{Type: domain.ResolveSlug(typ), Description: desc, Tags: strings.Join(tags, ", "), AssignToMe: ado.IsMe(assigned)}
				if parent > 0 {
					v.Parent = strconv.Itoa(parent)
				}
				types := []string{"Task", "Bug", "User Story", "Product Backlog Item", "Feature", "Epic", "Issue"}
				if !containsFold(types, v.Type) {
					types = append([]string{v.Type}, types...)
				}
				if err := tui.RunCreateForm(&v, types); err != nil {
					return err
				}
				typ, title, desc = v.Type, v.Title, v.Description
				tags = strings.Split(v.Tags, ",")
				if v.AssignToMe {
					assigned = "@me"
				}
				parent = 0
				if v.Parent != "" {
					parent, _ = parseID(v.Parent)
				}
			}
			if assigned != "" {
				if assigned, err = client.ResolveIdentity(ctx, assigned); err != nil {
					return err
				}
			}
			var cleanTags []string
			for _, t := range tags {
				if t = strings.TrimSpace(t); t != "" {
					cleanTags = append(cleanTags, t)
				}
			}
			draft := domain.WorkItemDraft{
				Project: project, Type: domain.ResolveSlug(typ), Title: title, Description: textToHTML(desc),
				AssignedTo: assigned, Tags: cleanTags, ParentID: parent,
			}
			if err := draft.Validate(cfg.List("requireParentFor")); err != nil {
				return err
			}
			w, err := client.CreateWorkItem(ctx, draft)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return output.JSON(a.out, w)
			}
			a.printf("Created %s #%d: %s\n%s\n", w.Type, w.ID, w.Title, w.URL)
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&typ, "type", "t", "", "work item type (default: the usual child type of --parent, else defaultType config, else Task)")
	fl.StringVar(&title, "title", "", "title")
	fl.StringVarP(&desc, "description", "d", "", "description (plain text)")
	fl.StringVarP(&assigned, "assigned-to", "a", "", "assignee ('@me' for yourself)")
	fl.StringArrayVar(&tags, "tag", nil, "tag; repeatable")
	fl.IntVar(&parent, "parent", 0, "parent work item id")
	return cmd
}

func childTypeFor(ctx context.Context, client *ado.Client, parentID int) (string, error) {
	p, err := client.GetWorkItem(ctx, parentID)
	if err != nil {
		return "", fmt.Errorf("parent: %w", err)
	}
	cats, _ := client.TypeCategories(ctx, p.Project) // fall back to the generic mapping on error
	return domain.ChildTypeOptions(cats, p.Type)[0], nil
}

// textToHTML converts plain text to the HTML rich-text fields expect.
func textToHTML(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return ado.EncodeComment(s)
}

func (a *App) wiUpdateCmd() *cobra.Command {
	var state, title, assigned string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a work item's state, title or assignee",
		Example: `  tfs wi update 1234 --state active
  tfs wi update 1234 --assigned-to @me
  tfs wi update 1234 --assigned-to ""   # unassign`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, err := a.api()
			if err != nil {
				return err
			}
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			var upd domain.WorkItemUpdate
			if cmd.Flags().Changed("state") {
				s := domain.ResolveSlug(state)
				upd.State = &s
			}
			if cmd.Flags().Changed("title") {
				t := strings.TrimSpace(title)
				if t == "" {
					return fmt.Errorf("--title must not be empty")
				}
				upd.Title = &t
			}
			if cmd.Flags().Changed("assigned-to") {
				who, err := client.ResolveIdentity(ctx, assigned)
				if err != nil {
					return err
				}
				upd.AssignedTo = &who
			}
			if upd.IsEmpty() {
				return fmt.Errorf("nothing to update: pass --state, --title or --assigned-to")
			}
			w, err := client.UpdateWorkItem(ctx, id, upd)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return output.JSON(a.out, w)
			}
			a.printf("Updated #%d: %s · %s · %s\n", w.ID, w.State, ui.OrDash(w.AssignedTo), w.Title)
			return nil
		},
	}
	cmd.Flags().StringVarP(&state, "state", "s", "", "new state (slugs ok)")
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVarP(&assigned, "assigned-to", "a", "", "new assignee ('@me' for yourself, empty to unassign)")
	return cmd
}

func (a *App) wiCommentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "comment <id> [text|-]",
		Short: "Add a comment (use '-' to read from stdin; form when text is omitted)",
		Example: `  tfs wi comment 1234 "Retested on staging, all green."
  tfs wi comment 1234 - < notes.md`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.api()
			if err != nil {
				return err
			}
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			var text string
			switch {
			case len(args) == 2 && args[1] == "-":
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				text = string(b)
			case len(args) == 2:
				text = args[1]
			case a.interactive():
				if text, err = tui.PromptText(fmt.Sprintf("Comment on #%d", id)); err != nil {
					return err
				}
			default:
				return fmt.Errorf("comment text is required (or '-' to read stdin)")
			}
			c, err := client.AddComment(cmd.Context(), id, text)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return output.JSON(a.out, c)
			}
			a.printf("Added comment %d to #%d\n", c.ID, id)
			return nil
		},
	}
}

func (a *App) wiBranchCmd() *cobra.Command {
	var base string
	cmd := &cobra.Command{
		Use:   "branch [id]",
		Short: "Create a git branch for a work item and switch to it",
		Long: `Create a git branch named after the work item and switch to it.

The branch name comes from the branchTemplate config (default feature/{id}-{slug}). It starts
from the remote's default branch, fetched first so it is up to date, unless --base is given.
If the branch already exists, tfs just switches to it.`,
		Example: `  tfs wi branch 1234
  tfs wi branch 1234 --base origin/develop
  tfs wi branch          # pick a work item`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, err := a.api()
			if err != nil {
				return err
			}
			id, err := a.pickWorkItem(ctx, args, "create a branch for")
			if err != nil {
				return err
			}
			w, err := client.GetWorkItem(ctx, id)
			if err != nil {
				return err
			}
			msg, err := a.workItemBranch(ctx, w, base)
			if err != nil {
				return err
			}
			a.printf("%s\n", msg)
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "start point (default: the remote's default branch, e.g. origin/main)")
	return cmd
}

// workItemBranch creates (or switches to) the work item's branch and describes what it did.
func (a *App) workItemBranch(ctx context.Context, w domain.WorkItem, base string) (string, error) {
	cfg, err := a.config()
	if err != nil {
		return "", err
	}
	if domain.TitleSlug(w.Title) == "" {
		return "", fmt.Errorf("work item #%d has no usable title to derive a branch name from", w.ID)
	}
	branch := domain.BranchName(cfg.String("branchTemplate"), w)
	g := git.Git{}
	if g.LocalBranchExists(ctx, branch) {
		if _, err := g.SwitchNewBranch(ctx, branch, ""); err != nil {
			return "", err
		}
		return "Switched to existing branch " + branch, nil
	}
	if base == "" {
		remote := cfg.String("remote")
		def, err := g.DefaultBranch(ctx, remote)
		if err != nil {
			return "", err
		}
		if err := g.Fetch(ctx, remote, def); err != nil {
			return "", err
		}
		base = remote + "/" + def
	}
	if _, err := g.SwitchNewBranch(ctx, branch, base); err != nil {
		return "", err
	}
	return fmt.Sprintf("Switched to new branch %s (from %s)", branch, base), nil
}

func (a *App) wiWorktreeCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "worktree [id]",
		Short: "Create a git worktree and branch for a work item",
		Long: `Create a git worktree with a new branch named after the work item.

The branch name comes from the branchTemplate config (default feature/{id}-{slug}) and the
worktree is created next to the repository as ../<id>-<slug> unless --path is given.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, err := a.api()
			if err != nil {
				return err
			}
			id, err := a.pickWorkItem(ctx, args, "create a worktree for")
			if err != nil {
				return err
			}
			w, err := client.GetWorkItem(ctx, id)
			if err != nil {
				return err
			}
			branch, wt, err := a.workItemWorktree(ctx, w, path)
			if err != nil {
				return err
			}
			a.printf("Created worktree %s on new branch %s\n", wt, branch)
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "worktree location (default ../<id>-<slug>)")
	return cmd
}

func (a *App) workItemWorktree(ctx context.Context, w domain.WorkItem, path string) (branch, wt string, err error) {
	cfg, err := a.config()
	if err != nil {
		return "", "", err
	}
	slug := domain.TitleSlug(w.Title)
	if slug == "" {
		return "", "", fmt.Errorf("work item #%d has no usable title to derive a branch name from", w.ID)
	}
	branch = domain.BranchName(cfg.String("branchTemplate"), w)
	g := git.Git{}
	if path == "" {
		if path, err = g.SiblingPath(ctx, fmt.Sprintf("%d-%s", w.ID, slug)); err != nil {
			return "", "", err
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return branch, path, g.AddWorktreeNewBranch(ctx, path, branch)
}
