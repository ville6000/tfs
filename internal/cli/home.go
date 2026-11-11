package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ville6000/tfs/internal/ado"
	"github.com/ville6000/tfs/internal/browser"
	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/output"
	"github.com/ville6000/tfs/internal/tui"
)

// How many of the user's work items and recent builds the home screen reads.
const (
	homeWorkItems = 100
	homeBuilds    = 50
)

func (a *App) homeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "home",
		Short: "What waits for you, and how your pull requests, builds and work items are doing",
		Long: `The home screen: what waits for you, and how your own work is doing, in the project.

  Waiting for your review  active pull requests with you, or a group or team you belong to,
                           as a reviewer that has not voted yet (not drafts or your own)
  Release approvals        release approvals waiting for you or your groups
  Your pull requests       your active pull requests and their approval
  Your builds              your newest build of each pipeline and branch, last 7 days
  Your work items          open work items assigned to you

Running tfs on its own in a terminal opens it too. Finding reviews requested from your groups
needs a token with the Identity "Read" scope, to read which groups you belong to.`,
		Example: `  tfs
  tfs home --no-tui
  tfs home --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.home(cmd.Context()) },
	}
}

// configured reports whether a collection URL is set, so tfs on its own can open the home screen.
func (a *App) configured() bool {
	cfg, err := a.config()
	return err == nil && cfg.String("collectionUrl") != ""
}

func (a *App) home(ctx context.Context) error {
	client, err := a.api()
	if err != nil {
		return err
	}
	project, err := a.resolveProject(ctx)
	if err != nil {
		return err
	}
	cfg, err := a.config()
	if err != nil {
		return err
	}
	patterns := cfg.List("releasePipelines")
	// An authorization failure of one section, with a token the server accepts, most likely
	// means the token lacks that section's scope.
	hint := func(ctx context.Context, err error, scope string) error {
		var e *ado.APIError
		if !errors.As(err, &e) || (e.Kind != ado.ErrUnauthorized && e.Kind != ado.ErrForbidden) {
			return err
		}
		if _, werr := client.MyDisplayName(ctx); werr != nil {
			return werr // the token itself is refused
		}
		return fmt.Errorf("server returned %d: %s (the token may lack the %s scope)", e.StatusCode, e.Message, scope)
	}
	src := tui.HomeSources{
		PullRequests: func(ctx context.Context) (domain.MyPullRequests, error) {
			prs, err := client.MyPullRequests(ctx, project)
			return prs, hint(ctx, err, `Code "Read"`)
		},
		Approvals: func(ctx context.Context) ([]domain.ReleaseApproval, error) {
			all, err := client.MyPendingApprovals(ctx, project)
			if err != nil {
				return nil, hint(ctx, err, `Release "Read"`)
			}
			as := []domain.ReleaseApproval{}
			for _, ap := range all {
				if domain.MatchPipeline(patterns, domain.ReleasePipeline{Name: ap.Pipeline}) {
					as = append(as, ap)
				}
			}
			return as, nil
		},
		Builds: func(ctx context.Context) ([]domain.Build, error) {
			me, err := client.MyDisplayName(ctx)
			if err != nil {
				return nil, err
			}
			bs, err := client.ListBuilds(ctx, domain.BuildQuery{Project: project, RequestedFor: me, Top: homeBuilds})
			if err != nil {
				return nil, hint(ctx, err, `Build "Read"`)
			}
			return domain.LatestBuilds(bs, time.Now().AddDate(0, 0, -domain.HomeBuildDays)), nil
		},
		WorkItems: func(ctx context.Context) ([]domain.WorkItem, error) {
			items, err := client.ListWorkItems(ctx, domain.WorkItemQuery{
				Project: project, AssignedTo: "@me", ExcludedStates: domain.NotOpenStates, Top: homeWorkItems,
			})
			return items, hint(ctx, err, `Work Items "Read"`)
		},
	}
	if a.interactive() {
		act := a.actions()
		builds := &tui.Builds{Client: client, Query: domain.BuildQuery{Project: project}, Open: browser.Open}
		rel := &tui.Releases{Client: client, Project: project, Patterns: patterns, Open: browser.Open, PerPipeline: 10, Actions: act}
		return tui.Run(tui.NewHome(project, act, builds, rel, src))
	}
	h := src.Load(ctx, project)
	if a.jsonOut {
		if err := output.JSON(a.out, h); err != nil {
			return err
		}
	} else {
		output.Home(a.out, h, time.Now(), a.width())
	}
	// Some sections failing (say, for a token without the Release scope) still leaves a useful
	// screen; nothing loading at all is an error.
	if len(h.Errors) == 4 {
		return errors.New("could not load the home screen: " + h.Errors["workItems"])
	}
	return nil
}
