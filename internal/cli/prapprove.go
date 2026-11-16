package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/output"
)

// completionFlags are the auto-complete options of `pr approve --auto-complete` and
// `pr autocomplete`.
type completionFlags struct {
	strategy           string
	deleteSourceBranch bool
	completeWorkItems  bool
}

var completionFlagNames = []string{"merge-strategy", "delete-source-branch", "complete-work-items"}

// register adds the flags to cmd; needs, when set, is the flag they need (e.g. "--auto-complete").
func (f *completionFlags) register(cmd *cobra.Command, needs string) {
	note := func(def string) string {
		if needs != "" {
			return " (with " + needs + "; default: " + def + ")"
		}
		return " (default: " + def + ")"
	}
	fl := cmd.Flags()
	fl.StringVar(&f.strategy, "merge-strategy", "", "merge, squash, rebase or semilinear"+note("the pull request's, else the first the target branch allows"))
	fl.BoolVar(&f.deleteSourceBranch, "delete-source-branch", false, "delete the source branch after merging"+note("as set on the pull request"))
	fl.BoolVar(&f.completeWorkItems, "complete-work-items", false, "complete the linked work items after merging"+note("as set on the pull request"))
}

// validate checks the flags before anything is read from the server.
func (f completionFlags) validate() error {
	if f.strategy == "" {
		return nil
	}
	if _, err := domain.ParseMergeStrategy(f.strategy); err != nil {
		return fmt.Errorf("--merge-strategy: %w", err)
	}
	return nil
}

// completionOptions are the options auto-complete gets: the pull request's own, changed by the
// flags given. The merge strategy must be one the target branch's policies allow; when not
// given, it is the pull request's if they allow it, else the first they allow.
func (a *App) completionOptions(ctx context.Context, cmd *cobra.Command, pr domain.PullRequest, f completionFlags) (domain.CompletionOptions, error) {
	opts := pr.CompletionOptions
	if cmd.Flags().Changed("delete-source-branch") {
		opts.DeleteSourceBranch = f.deleteSourceBranch
	}
	if cmd.Flags().Changed("complete-work-items") {
		opts.CompleteWorkItems = f.completeWorkItems
	}
	client, err := a.api()
	if err != nil {
		return opts, err
	}
	allowed, err := client.AllowedMergeStrategies(ctx, pr)
	if err != nil {
		fmt.Fprintf(a.errOut, "warning: could not read the merge types %s allows, so the merge strategy is not checked: %v\n", pr.TargetBranch, err)
	}
	if f.strategy == "" {
		opts.MergeStrategy = domain.PickMergeStrategy(allowed, opts.MergeStrategy)
		return opts, nil
	}
	m, _ := domain.ParseMergeStrategy(f.strategy) // validated before
	if err := domain.CheckMergeStrategy(allowed, m, pr.TargetBranch); err != nil {
		return opts, err
	}
	opts.MergeStrategy = m
	return opts, nil
}

// pullRequestDetail describes pr for a confirmation.
func pullRequestDetail(pr domain.PullRequest) string {
	return fmt.Sprintf("!%d %s\n%s · %s → %s", pr.ID, pr.Title, pr.Repository, pr.SourceBranch, pr.TargetBranch)
}

// printPullRequest prints the pull request as the server has it now, for --json.
func (a *App) printPullRequest(ctx context.Context, id int) error {
	client, err := a.api()
	if err != nil {
		return err
	}
	pr, err := client.GetPullRequest(ctx, id)
	if err != nil {
		return err
	}
	return output.JSON(a.out, pr)
}

func (a *App) prApproveCmd() *cobra.Command {
	var yes, autoComplete bool
	var f completionFlags
	cmd := &cobra.Command{
		Use:   "approve [id]",
		Short: "Approve a pull request, and set it to auto-complete with --auto-complete",
		Long: `Approve a pull request as yourself (picker when id is omitted). You become an optional
reviewer if you are not one yet, as in the web UI.

With --auto-complete the pull request is also set to complete by itself once its policies pass,
as 'tfs pr autocomplete' does; see there for the merge options.

Asks for confirmation unless --yes is given, and needs a token with the Code "Read & write"
scope. In the interactive pull request views, press a to approve and A to approve and set
auto-complete.`,
		Example: `  tfs pr approve 4567
  tfs pr approve 4567 --auto-complete
  tfs pr approve 4567 --auto-complete --merge-strategy squash --delete-source-branch --yes`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !autoComplete {
				for _, n := range completionFlagNames {
					if cmd.Flags().Changed(n) {
						return fmt.Errorf("--%s requires --auto-complete", n)
					}
				}
			}
			if err := f.validate(); err != nil {
				return err
			}
			ctx := cmd.Context()
			pr, err := a.pickPullRequest(ctx, args, "approve a pull request")
			if err != nil {
				return err
			}
			if err := pr.CheckOpen(); err != nil {
				return err
			}
			question, detail := fmt.Sprintf("Approve !%d?", pr.ID), pullRequestDetail(pr)
			var opts domain.CompletionOptions
			if autoComplete {
				if opts, err = a.completionOptions(ctx, cmd, pr, f); err != nil {
					return err
				}
				question = fmt.Sprintf("Approve !%d and set it to auto-complete?", pr.ID)
				detail += "\nauto-complete: " + opts.Summary()
			}
			if err := a.confirm(yes, "a pull request", question, detail, "Approve"); err != nil {
				return err
			}
			client, err := a.api()
			if err != nil {
				return err
			}
			if err := client.Vote(ctx, pr, domain.VoteApprove); err != nil {
				return err
			}
			if !a.jsonOut {
				a.printf("Approved !%d: %s\n", pr.ID, pr.Title)
			}
			if autoComplete {
				if _, err := client.SetAutoComplete(ctx, pr, opts); err != nil {
					return fmt.Errorf("approved !%d, but could not set auto-complete: %w", pr.ID, err)
				}
				if !a.jsonOut {
					a.printf("Set to auto-complete: %s\n", opts.Summary())
				}
			}
			if a.jsonOut {
				return a.printPullRequest(ctx, pr.ID)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&autoComplete, "auto-complete", false, "also set the pull request to complete once its policies pass")
	fl.BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	f.register(cmd, "--auto-complete")
	return cmd
}

func (a *App) prAutoCompleteCmd() *cobra.Command {
	var yes, off bool
	var f completionFlags
	cmd := &cobra.Command{
		Use:     "autocomplete [id]",
		Aliases: []string{"auto-complete"},
		Short:   "Set a pull request to complete once its policies pass, or turn that off with --off",
		Long: `Set a pull request to complete by itself once its required reviewers have approved and
its other policies pass (picker when id is omitted), or turn auto-complete off with --off.
Setting it again changes the options.

The options are the pull request's own, changed by the flags given:

  --merge-strategy         merge (no fast-forward), squash, rebase (and fast-forward) or
                           semilinear. It must be one the target branch's "Limit merge types"
                           policy allows; when not given, the pull request's own is kept if
                           allowed, else the first allowed one is used, else merge.
  --delete-source-branch   delete the source branch after merging
  --complete-work-items    complete the linked work items after merging

Turn an option off with --delete-source-branch=false. Asks for confirmation unless --yes is
given, and needs a token with the Code "Read & write" scope. In the interactive pull request
views, press m.`,
		Example: `  tfs pr autocomplete 4567
  tfs pr autocomplete 4567 --merge-strategy squash --delete-source-branch --complete-work-items
  tfs pr autocomplete 4567 --off`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if off {
				for _, n := range completionFlagNames {
					if cmd.Flags().Changed(n) {
						return fmt.Errorf("--%s cannot be used with --off", n)
					}
				}
			}
			if err := f.validate(); err != nil {
				return err
			}
			ctx := cmd.Context()
			pr, err := a.pickPullRequest(ctx, args, "set a pull request to auto-complete")
			if err != nil {
				return err
			}
			if err := pr.CheckOpen(); err != nil {
				return err
			}
			client, err := a.api()
			if err != nil {
				return err
			}
			if off {
				if !pr.HasAutoComplete() {
					if a.jsonOut {
						return output.JSON(a.out, pr)
					}
					a.printf("Auto-complete of !%d is already off\n", pr.ID)
					return nil
				}
				if err := a.confirm(yes, "a pull request", fmt.Sprintf("Turn off auto-complete of !%d?", pr.ID), pullRequestDetail(pr), "Turn off"); err != nil {
					return err
				}
				updated, err := client.CancelAutoComplete(ctx, pr)
				if err != nil {
					return err
				}
				if a.jsonOut {
					return output.JSON(a.out, updated)
				}
				a.printf("Turned off auto-complete of !%d: %s\n", pr.ID, pr.Title)
				return nil
			}
			opts, err := a.completionOptions(ctx, cmd, pr, f)
			if err != nil {
				return err
			}
			question := fmt.Sprintf("Set !%d to auto-complete?", pr.ID)
			if pr.HasAutoComplete() {
				question = fmt.Sprintf("Change how !%d auto-completes?", pr.ID)
			}
			if err := a.confirm(yes, "a pull request", question, pullRequestDetail(pr)+"\nauto-complete: "+opts.Summary(), "Set auto-complete"); err != nil {
				return err
			}
			updated, err := client.SetAutoComplete(ctx, pr, opts)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return output.JSON(a.out, updated)
			}
			a.printf("Set !%d to auto-complete: %s\n", pr.ID, opts.Summary())
			return nil
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&off, "off", false, "turn auto-complete off")
	fl.BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	f.register(cmd, "")
	return cmd
}
