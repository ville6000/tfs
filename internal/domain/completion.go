package domain

import (
	"fmt"
	"slices"
	"strings"
)

// Reviewer votes.
const (
	VoteApprove                = 10
	VoteApproveWithSuggestions = 5
	VoteNone                   = 0
	VoteWaitForAuthor          = -5
	VoteReject                 = -10
)

// MergeStrategy is how a completed pull request is merged into its target, as the server names
// it.
type MergeStrategy string

// Merge strategies.
const (
	MergeNoFastForward MergeStrategy = "noFastForward"
	MergeSquash        MergeStrategy = "squash"
	MergeRebase        MergeStrategy = "rebase"
	MergeRebaseMerge   MergeStrategy = "rebaseMerge"
)

// MergeStrategies lists the merge strategies in the order the web UI offers them.
var MergeStrategies = []MergeStrategy{MergeNoFastForward, MergeSquash, MergeRebase, MergeRebaseMerge}

var mergeStrategyNames = map[MergeStrategy]string{
	MergeNoFastForward: "merge", MergeSquash: "squash", MergeRebase: "rebase", MergeRebaseMerge: "semilinear",
}

// MergeStrategyNames are the names --merge-strategy accepts, in the web UI's order.
func MergeStrategyNames() []string {
	names := make([]string, len(MergeStrategies))
	for i, m := range MergeStrategies {
		names[i] = mergeStrategyNames[m]
	}
	return names
}

// Name is the strategy's short name, as --merge-strategy takes it.
func (m MergeStrategy) Name() string {
	if n, ok := mergeStrategyNames[m]; ok {
		return n
	}
	return string(m)
}

// Label describes the strategy the way the web UI does.
func (m MergeStrategy) Label() string {
	switch m {
	case MergeNoFastForward:
		return "Merge (no fast-forward)"
	case MergeSquash:
		return "Squash commit"
	case MergeRebase:
		return "Rebase and fast-forward"
	case MergeRebaseMerge:
		return "Semi-linear merge"
	}
	return string(m)
}

// ParseMergeStrategy reads a short name (merge, squash, rebase, semilinear) or the server's
// name of a strategy, ignoring case.
func ParseMergeStrategy(s string) (MergeStrategy, error) {
	s = strings.TrimSpace(s)
	for _, m := range MergeStrategies {
		if strings.EqualFold(s, m.Name()) || strings.EqualFold(s, string(m)) {
			return m, nil
		}
	}
	return "", fmt.Errorf("unknown merge strategy %q (valid: %s)", s, strings.Join(MergeStrategyNames(), ", "))
}

// CompletionOptions are how a pull request is completed, by hand or by auto-complete.
type CompletionOptions struct {
	MergeStrategy      MergeStrategy `json:"mergeStrategy,omitempty"`
	DeleteSourceBranch bool          `json:"deleteSourceBranch,omitempty"`
	CompleteWorkItems  bool          `json:"completeWorkItems,omitempty"`
	// MergeCommitMessage and IgnoreConfigIDs (optional policies auto-complete does not wait
	// for) are set in the web UI; tfs keeps them as they are.
	MergeCommitMessage string `json:"-"`
	IgnoreConfigIDs    []int  `json:"-"`
}

// Summary describes the options, e.g. "squash · delete source branch".
func (o CompletionOptions) Summary() string {
	parts := []string{o.MergeStrategy.Name()}
	if o.MergeStrategy == "" {
		parts[0] = MergeNoFastForward.Name()
	}
	if o.DeleteSourceBranch {
		parts = append(parts, "delete source branch")
	}
	if o.CompleteWorkItems {
		parts = append(parts, "complete work items")
	}
	return strings.Join(parts, " · ")
}

// HasAutoComplete reports whether the pull request completes by itself once its policies pass.
func (p PullRequest) HasAutoComplete() bool { return p.AutoCompleteSetBy != "" }

// IsClosed reports whether the pull request is completed or abandoned.
func (p PullRequest) IsClosed() bool {
	return strings.EqualFold(p.Status, "completed") || strings.EqualFold(p.Status, "abandoned")
}

// CheckOpen fails for a completed or abandoned pull request, which cannot be voted on or set to
// auto-complete.
func (p PullRequest) CheckOpen() error {
	if p.IsClosed() {
		return fmt.Errorf("pull request !%d is %s", p.ID, strings.ToLower(p.Status))
	}
	return nil
}

// MergePolicy is a "Limit merge types" branch policy: the strategies it allows.
type MergePolicy struct{ Allowed []MergeStrategy }

// AllowedMergeStrategies lists the strategies every policy allows, in the web UI's order. It is
// nil when no policy limits them, and also when the policies leave none in common, which the
// server reports better than tfs could.
func AllowedMergeStrategies(policies []MergePolicy) []MergeStrategy {
	if len(policies) == 0 {
		return nil
	}
	var out []MergeStrategy
	for _, m := range MergeStrategies {
		ok := true
		for _, p := range policies {
			ok = ok && slices.Contains(p.Allowed, m)
		}
		if ok {
			out = append(out, m)
		}
	}
	return out
}

// PickMergeStrategy is the first of preferred that allowed contains, or when none is, the first
// allowed. allowed is empty when every strategy is allowed; the fallback is then a merge, the
// server's own default.
func PickMergeStrategy(allowed []MergeStrategy, preferred ...MergeStrategy) MergeStrategy {
	for _, m := range preferred {
		if m != "" && (len(allowed) == 0 || slices.Contains(allowed, m)) {
			return m
		}
	}
	if len(allowed) > 0 {
		return allowed[0]
	}
	return MergeNoFastForward
}

// CheckMergeStrategy fails when the target branch's policies do not allow m, which would leave
// auto-complete stuck once the pull request is otherwise ready.
func CheckMergeStrategy(allowed []MergeStrategy, m MergeStrategy, target string) error {
	if len(allowed) == 0 || slices.Contains(allowed, m) {
		return nil
	}
	names := make([]string, len(allowed))
	for i, a := range allowed {
		names[i] = a.Name()
	}
	return fmt.Errorf("the policies of %s do not allow %s (allowed: %s)", target, m.Name(), strings.Join(names, ", "))
}
