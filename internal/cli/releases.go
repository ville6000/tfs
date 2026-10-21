package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ville6000/tfs/internal/browser"
	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/output"
	"github.com/ville6000/tfs/internal/tui"
)

func (a *App) releaseCmd() *cobra.Command {
	var pipelines []string
	var top int
	list := func(cmd *cobra.Command, _ []string) error { return a.releaseList(cmd.Context(), pipelines, top) }
	flags := func(c *cobra.Command) {
		c.Flags().StringArrayVar(&pipelines, "pipeline", nil, "only these release pipelines (names, \\folder\\name, * wildcards); repeatable")
		c.Flags().IntVarP(&top, "top", "n", 10, "recent releases to look at per pipeline")
	}
	cmd := &cobra.Command{
		Use:     "release",
		Aliases: []string{"releases", "rel"},
		Short:   "Classic release pipelines: dashboard, history, approve, reject",
		Long: `Classic release pipelines (Pipelines → Releases).

Without a subcommand, shows a dashboard of your release pipelines with the newest release on
each stage. Limit it to the pipelines you care about with --pipeline, or permanently with
'tfs config set releasePipelines "frontend, backend"'. With a single --pipeline, shows that
pipeline's release history instead.

Approving and rejecting ask for confirmation and need a token with the Release
"Read, write, execute & manage" scope. 'approve --reject-older' also rejects the older releases
waiting for an approval on the same stage. Releases are never started manually; they move on
through their pipeline's own triggers and approvals.`,
		Example: `  tfs release
  tfs release --pipeline frontend
  tfs release approvals
  tfs release approve 142 --stage Prod --comment "Smoke tests green"
  tfs release approve 142 --stage Prod --reject-older
  tfs release reject 142 --stage Prod -m "Blocked by incident"`,
		Args: cobra.NoArgs,
		RunE: list,
	}
	flags(cmd)
	listCmd := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "Show the release dashboard (same as tfs release)", Args: cobra.NoArgs, RunE: list}
	flags(listCmd)
	cmd.AddCommand(listCmd, a.releaseShowCmd(), a.releaseApprovalsCmd(),
		a.releaseDecideCmd(true), a.releaseDecideCmd(false), a.releaseOpenCmd())
	return cmd
}

func (a *App) releases(ctx context.Context, patterns []string, perPipeline int) (*tui.Releases, error) {
	client, err := a.api()
	if err != nil {
		return nil, err
	}
	project, err := a.resolveProject(ctx)
	if err != nil {
		return nil, err
	}
	if len(patterns) == 0 {
		cfg, _ := a.config()
		patterns = cfg.List("releasePipelines")
	}
	return &tui.Releases{Client: client, Project: project, Patterns: patterns, Open: browser.Open, PerPipeline: max(1, perPipeline),
		Actions: a.actions()}, nil
}

func (a *App) releaseList(ctx context.Context, flagPipelines []string, top int) error {
	r, err := a.releases(ctx, flagPipelines, top)
	if err != nil {
		return err
	}
	// A single --pipeline shows that pipeline's history rather than a one-row dashboard.
	if len(flagPipelines) > 0 {
		all, err := r.Client.ListReleasePipelines(ctx, r.Project)
		if err != nil {
			return err
		}
		var matched []domain.ReleasePipeline
		for _, p := range all {
			if domain.MatchPipeline(r.Patterns, p) {
				matched = append(matched, p)
			}
		}
		if len(matched) == 0 {
			names := make([]string, len(all))
			for i, p := range all {
				names[i] = p.Name
			}
			return fmt.Errorf("no release pipeline matches %s (available: %s)", strings.Join(flagPipelines, ", "), strings.Join(names, ", "))
		}
		if len(matched) == 1 {
			p := matched[0]
			if a.interactive() {
				return tui.Run(tui.NewReleaseHistory(r, p))
			}
			rels, err := r.Client.ListReleases(ctx, r.Project, p.ID, max(top, 20))
			if err != nil {
				return err
			}
			if a.jsonOut {
				return output.JSON(a.out, rels)
			}
			output.Releases(a.out, rels, a.width())
			return nil
		}
	}
	if a.interactive() {
		return tui.Run(tui.NewReleaseDashboard(r))
	}
	rows, err := r.Client.ReleaseOverview(ctx, r.Project, r.Patterns, r.PerPipeline)
	if a.jsonOut {
		if jerr := output.JSON(a.out, rows); jerr != nil {
			return jerr
		}
		return err
	}
	var ok []domain.PipelineOverview
	for _, o := range rows {
		if o.Pipeline.ID != 0 {
			ok = append(ok, o)
		}
	}
	output.ReleaseOverview(a.out, ok, a.width())
	return err
}

// releaseDetail is a release with the work items one of its stages gets, for JSON output.
type releaseDetail struct {
	domain.Release
	Changes *domain.ReleaseChanges `json:"changes,omitempty"`
}

func (a *App) releaseShowCmd() *cobra.Command {
	var stageName string
	cmd := &cobra.Command{
		Use:   "show <release-id>",
		Short: "Show a release, its stages and the work items it brings (approve and reject from here)",
		Long: `Show a release, its stages, and the work items that deploying it brings to a stage: those of
its builds since the release the stage last deployed. The stage is the first one awaiting
approval (or not deployed yet) unless --stage names another.

Listing work items also needs the Build and Work Items read scopes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			r, err := a.releases(cmd.Context(), nil, 1)
			if err != nil {
				return err
			}
			if a.interactive() {
				v := tui.NewReleaseView(r, id)
				v.Stage = stageName
				return tui.Run(v)
			}
			rel, err := r.Client.GetRelease(cmd.Context(), r.Project, id)
			if err != nil {
				return err
			}
			detail := releaseDetail{Release: rel}
			stage, ok := rel.Stage(stageName)
			switch {
			case stageName != "" && !ok:
				return fmt.Errorf("%s has no stage %q (stages: %s)", rel.Name, stageName, stageNames(rel))
			case stageName == "" && rel.FocusStage() >= 0:
				stage, ok = rel.Stages[rel.FocusStage()], true
			}
			if ok {
				ch, err := r.Client.ReleaseChanges(cmd.Context(), r.Project, rel, stage)
				if err != nil {
					fmt.Fprintf(a.errOut, "Could not list the work items for %s: %v\n", stage.Name, err)
				} else {
					detail.Changes = &ch
				}
			}
			if a.jsonOut {
				return output.JSON(a.out, detail)
			}
			output.Release(a.out, rel)
			if detail.Changes != nil {
				output.ReleaseChanges(a.out, *detail.Changes, a.width())
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&stageName, "stage", "s", "", "list the work items this stage gets (default: the first awaiting approval)")
	return cmd
}

func stageNames(rel domain.Release) string {
	names := make([]string, len(rel.Stages))
	for i, s := range rel.Stages {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}

func (a *App) releaseApprovalsCmd() *cobra.Command {
	var pipelines []string
	cmd := &cobra.Command{
		Use:   "approvals",
		Short: "List release approvals waiting for you",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := a.releases(cmd.Context(), pipelines, 1)
			if err != nil {
				return err
			}
			if a.interactive() {
				return tui.Run(tui.NewApprovalsList(r))
			}
			all, err := r.Client.MyPendingApprovals(cmd.Context(), r.Project)
			if err != nil {
				return err
			}
			var as []domain.ReleaseApproval
			for _, ap := range all {
				if domain.MatchPipeline(r.Patterns, domain.ReleasePipeline{Name: ap.Pipeline}) {
					as = append(as, ap)
				}
			}
			if a.jsonOut {
				return output.JSON(a.out, as)
			}
			if len(as) == 0 {
				fmt.Fprintln(a.errOut, "Nothing is waiting for your approval.")
				return nil
			}
			output.Approvals(a.out, as, a.width())
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&pipelines, "pipeline", nil, "only these release pipelines; repeatable")
	return cmd
}

// stageFlags are shared by approve and reject.
type stageFlags struct {
	stage, comment string
	yes            bool
	rejectOlder    bool
}

func (f *stageFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.stage, "stage", "s", "", "stage (environment) name, e.g. Prod")
	cmd.Flags().StringVarP(&f.comment, "comment", "m", "", "comment recorded with the action")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "do not ask for confirmation")
	_ = cmd.MarkFlagRequired("stage")
}

func (a *App) releaseStage(ctx context.Context, arg, stageName string) (*tui.Releases, domain.Release, domain.ReleaseStage, error) {
	id, err := parseID(arg)
	if err != nil {
		return nil, domain.Release{}, domain.ReleaseStage{}, err
	}
	r, err := a.releases(ctx, nil, 1)
	if err != nil {
		return nil, domain.Release{}, domain.ReleaseStage{}, err
	}
	rel, err := r.Client.GetRelease(ctx, r.Project, id)
	if err != nil {
		return nil, domain.Release{}, domain.ReleaseStage{}, err
	}
	stage, ok := rel.Stage(stageName)
	if !ok {
		return nil, rel, domain.ReleaseStage{}, fmt.Errorf("%s has no stage %q (stages: %s)", rel.Name, stageName, stageNames(rel))
	}
	return r, rel, stage, nil
}

// confirm asks before a release action unless --yes was given.
func (a *App) confirm(yes bool, question, detail, verb string) error {
	if yes {
		return nil
	}
	if !a.interactive() {
		return errors.New("refusing to change a release without confirmation; pass --yes")
	}
	ok, err := tui.ConfirmAction(question, detail, verb)
	if err != nil {
		return err
	}
	if !ok {
		return tui.ErrAborted
	}
	return nil
}

func (a *App) releaseDecideCmd(approve bool) *cobra.Command {
	var f stageFlags
	use, verb, done := "reject", "Reject", "Rejected"
	if approve {
		use, verb, done = "approve", "Approve", "Approved"
	}
	cmd := &cobra.Command{
		Use:   use + " <release-id> --stage <name>",
		Short: verb + " a stage's pending approval",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			r, rel, stage, err := a.releaseStage(ctx, args[0], f.stage)
			if err != nil {
				return err
			}
			ap, err := r.Client.PickApproval(ctx, r.Project, rel, stage)
			if err != nil {
				return err
			}
			detail := fmt.Sprintf("%s · %s → %s", rel.Pipeline, rel.Name, stage.Name)
			question := fmt.Sprintf("%s %s for %s?", verb, rel.Name, stage.Name)
			if f.rejectOlder {
				return a.promote(ctx, r, rel, stage, ap, f, detail)
			}
			if err := a.confirm(f.yes, question, detail, verb); err != nil {
				return err
			}
			if err := r.Client.SetApproval(ctx, r.Project, ap.ID, approve, f.comment); err != nil {
				return err
			}
			a.printf("%s %s for %s (%s)\n", done, rel.Name, stage.Name, rel.Pipeline)
			return nil
		},
	}
	f.register(cmd)
	if approve {
		cmd.Flags().BoolVar(&f.rejectOlder, "reject-older", false, "also reject the older releases waiting for an approval on the stage")
	}
	return cmd
}

// promote approves ap and rejects the approvals of the older releases waiting for the stage.
func (a *App) promote(ctx context.Context, r *tui.Releases, rel domain.Release, stage domain.ReleaseStage, ap domain.ReleaseApproval, f stageFlags, detail string) error {
	older, err := r.Client.PendingBefore(ctx, r.Project, rel, stage)
	if err != nil {
		return err
	}
	names := make([]string, len(older))
	for i, o := range older {
		names[i] = o.Release
	}
	question := fmt.Sprintf("Approve %s for %s?", rel.Name, stage.Name)
	if len(older) > 0 {
		question = fmt.Sprintf("Approve %s and reject %d older for %s?", rel.Name, len(older), stage.Name)
		detail += "\nAlso rejects: " + strings.Join(names, ", ")
	}
	if err := a.confirm(f.yes, question, detail, "Approve"); err != nil {
		return err
	}
	// Approve first, so a failed rejection never leaves the chosen release unapproved.
	if err := r.Client.SetApproval(ctx, r.Project, ap.ID, true, f.comment); err != nil {
		return err
	}
	a.printf("Approved %s for %s (%s)\n", rel.Name, stage.Name, rel.Pipeline)
	if err := r.Client.RejectSuperseded(ctx, r.Project, rel.Name, older, f.comment); err != nil {
		return err
	}
	if len(older) > 0 {
		a.printf("Rejected %s as superseded\n", strings.Join(names, ", "))
	}
	return nil
}

func (a *App) releaseOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open <release-id>",
		Short: "Open a release in the browser",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			client, err := a.api()
			if err != nil {
				return err
			}
			project, err := a.resolveProject(cmd.Context())
			if err != nil {
				return err
			}
			return a.openURL(client.ReleaseWebURL(project, id))
		},
	}
}
