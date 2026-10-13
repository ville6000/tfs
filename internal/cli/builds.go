package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ville6000/tfs/internal/browser"
	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/git"
	"github.com/ville6000/tfs/internal/output"
	"github.com/ville6000/tfs/internal/tui"
	"github.com/ville6000/tfs/internal/ui"
)

type buildFlags struct {
	pipelines []string
	branch    string
	mine      bool
	result    string
	top       int
}

func (f *buildFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringArrayVar(&f.pipelines, "pipeline", nil, "only these build pipelines (names, \\folder\\name, * wildcards); repeatable")
	fl.StringVarP(&f.branch, "branch", "b", "", "only builds of this branch ('.' for the current git branch)")
	fl.BoolVar(&f.mine, "mine", false, "only builds requested for you")
	fl.StringVar(&f.result, "result", "", "only finished builds with this result: succeeded, partially-succeeded, failed, canceled")
	fl.IntVarP(&f.top, "top", "n", 30, "maximum builds to list")
}

var buildResults = map[string]string{
	"succeeded": "succeeded", "partiallysucceeded": "partiallySucceeded", "failed": "failed", "canceled": "canceled", "cancelled": "canceled",
}

func (a *App) buildCmd() *cobra.Command {
	var f buildFlags
	list := func(cmd *cobra.Command, _ []string) error { return a.buildList(cmd.Context(), f) }
	cmd := &cobra.Command{
		Use:     "build",
		Aliases: []string{"builds"},
		Short:   "Builds: list runs, show stages and tasks, read logs",
		Long: `Builds (Pipelines → Builds / Runs), for both classic and YAML pipelines.

Without a subcommand, lists recent builds; in a terminal the list and build views refresh
while builds are running. Limit to your pipelines with --pipeline, or permanently with
'tfs config set buildPipelines "frontend-ci, backend-ci"'.

Viewing builds needs a token with the Build "Read" scope.`,
		Example: `  tfs build
  tfs build --pipeline frontend-ci --branch main
  tfs build -b . --mine              # your builds of the current branch
  tfs build --result failed
  tfs build show 1234
  tfs build log 1234                 # logs of the failed tasks`,
		Args: cobra.NoArgs,
		RunE: list,
	}
	f.register(cmd)
	listCmd := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List recent builds (same as tfs build)", Args: cobra.NoArgs, RunE: list}
	f.register(listCmd)
	cmd.AddCommand(listCmd, a.buildShowCmd(), a.buildLogCmd(), a.buildOpenCmd())
	return cmd
}

func (a *App) builds(ctx context.Context) (*tui.Builds, error) {
	client, err := a.api()
	if err != nil {
		return nil, err
	}
	project, err := a.resolveProject(ctx)
	if err != nil {
		return nil, err
	}
	return &tui.Builds{Client: client, Query: domain.BuildQuery{Project: project}, Open: browser.Open}, nil
}

func (a *App) buildList(ctx context.Context, f buildFlags) error {
	b, err := a.builds(ctx)
	if err != nil {
		return err
	}
	q := &b.Query
	q.Top = f.top
	var title []string
	patterns := f.pipelines
	if len(patterns) == 0 {
		cfg, _ := a.config()
		patterns = cfg.List("buildPipelines")
	}
	if len(patterns) > 0 {
		all, err := b.Client.ListBuildPipelines(ctx, q.Project)
		if err != nil {
			return err
		}
		var names []string
		for _, p := range all {
			if domain.MatchBuildPipeline(patterns, p) {
				q.PipelineIDs = append(q.PipelineIDs, p.ID)
				names = append(names, p.Name)
			}
		}
		if len(q.PipelineIDs) == 0 {
			avail := make([]string, len(all))
			for i, p := range all {
				avail[i] = p.Name
			}
			return fmt.Errorf("no build pipeline matches %s (available: %s)", strings.Join(patterns, ", "), strings.Join(avail, ", "))
		}
		title = append(title, strings.Join(names, ", "))
	}
	if f.branch == "." {
		if f.branch, err = (git.Git{}).CurrentBranch(ctx); err != nil {
			return err
		}
	}
	if q.Branch = f.branch; q.Branch != "" {
		title = append(title, q.Branch)
	}
	if f.mine {
		if q.RequestedFor, err = b.Client.MyDisplayName(ctx); err != nil {
			return err
		}
		title = append(title, "mine")
	}
	if f.result != "" {
		r, ok := buildResults[strings.ReplaceAll(strings.ToLower(f.result), "-", "")]
		if !ok {
			return fmt.Errorf("--result must be succeeded, partially-succeeded, failed or canceled")
		}
		q.Result = r
		title = append(title, f.result)
	}
	if a.interactive() {
		t := q.Project + " builds"
		if len(title) > 0 {
			t += " (" + strings.Join(title, " · ") + ")"
		}
		return tui.Run(tui.NewBuildList(b, t))
	}
	builds, err := b.Client.ListBuilds(ctx, *q)
	if err != nil {
		return err
	}
	if a.jsonOut {
		return output.JSON(a.out, builds)
	}
	output.Builds(a.out, builds, time.Now(), a.width())
	return nil
}

func (a *App) buildShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <build-id>",
		Short: "Show a build's stages, jobs and tasks (enter opens a task's log)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			b, err := a.builds(cmd.Context())
			if err != nil {
				return err
			}
			if a.interactive() {
				return tui.Run(tui.NewBuildView(b, id))
			}
			build, err := b.Client.GetBuild(cmd.Context(), b.Query.Project, id)
			if err != nil {
				return err
			}
			timeline, err := b.Client.BuildTimeline(cmd.Context(), b.Query.Project, id)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return output.JSON(a.out, map[string]any{"build": build, "timeline": domain.TimelineOutline(timeline)})
			}
			output.Build(a.out, build, timeline, time.Now())
			return nil
		},
	}
}

func (a *App) buildLogCmd() *cobra.Command {
	var task string
	cmd := &cobra.Command{
		Use:   "log <build-id>",
		Short: "Print task logs (by default those of the failed tasks)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			b, err := a.builds(ctx)
			if err != nil {
				return err
			}
			timeline, err := b.Client.BuildTimeline(ctx, b.Query.Project, id)
			if err != nil {
				return err
			}
			var picked []domain.TimelineRecord
			if task == "" {
				picked = domain.FailedTasks(timeline)
				if len(picked) == 0 {
					return fmt.Errorf("build %d has no failed task; pick one with --task <name>", id)
				}
			} else {
				for _, row := range domain.TimelineOutline(timeline) {
					if row.Record.LogID != 0 && strings.Contains(strings.ToLower(row.Record.Name), strings.ToLower(task)) {
						picked = append(picked, row.Record)
					}
				}
				if len(picked) == 0 {
					return fmt.Errorf("no step of build %d with a log matches %q", id, task)
				}
			}
			styled := a.styled()
			for i, r := range picked {
				if r.LogID == 0 {
					continue
				}
				text, err := b.Client.BuildLog(ctx, b.Query.Project, id, r.LogID)
				if err != nil {
					return err
				}
				if i > 0 {
					a.printf("\n")
				}
				ui.Println(a.out, ui.Bold.Render("── "+r.Name+" "+strings.Repeat("─", max(3, 60-len(r.Name)))))
				ui.Println(a.out, ui.FormatLog(text, styled))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&task, "task", "", "print the logs of steps whose name contains this text")
	return cmd
}

func (a *App) buildOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open <build-id>",
		Short: "Open a build in the browser",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			b, err := a.builds(cmd.Context())
			if err != nil {
				return err
			}
			return a.openURL(b.Client.BuildWebURL(b.Query.Project, id))
		},
	}
}
