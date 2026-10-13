package domain

import (
	"slices"
	"strings"
	"time"
)

// BuildPipeline is a build definition (classic or YAML).
type BuildPipeline struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

// Build is one run of a build pipeline.
type Build struct {
	ID           int       `json:"id"`
	Number       string    `json:"number"`
	PipelineID   int       `json:"pipelineId"`
	Pipeline     string    `json:"pipeline"`
	Status       string    `json:"status"`
	Result       string    `json:"result,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	Commit       string    `json:"commit,omitempty"`
	Repository   string    `json:"repository,omitempty"`
	RequestedFor string    `json:"requestedFor,omitempty"`
	Queued       time.Time `json:"queued,omitempty"`
	Started      time.Time `json:"started,omitempty"`
	Finished     time.Time `json:"finished,omitempty"`
	URL          string    `json:"url"`
}

// BuildState is a simplified build or timeline status for display.
type BuildState int

// Build states.
const (
	BuildQueued BuildState = iota
	BuildRunning
	BuildCancelling
	BuildSucceeded
	BuildPartiallySucceeded
	BuildFailed
	BuildCanceled
	BuildSkipped
)

func (s BuildState) String() string {
	return [...]string{"queued", "running", "cancelling", "succeeded", "partially succeeded", "failed", "canceled", "skipped"}[s]
}

// Done reports whether the state is final.
func (s BuildState) Done() bool { return s >= BuildSucceeded }

// State classifies the build from its status and result.
func (b Build) State() BuildState {
	switch strings.ToLower(b.Status) {
	case "inprogress":
		return BuildRunning
	case "cancelling":
		return BuildCancelling
	case "completed":
		return resultState(b.Result)
	}
	return BuildQueued
}

func resultState(result string) BuildState {
	switch strings.ToLower(result) {
	case "succeeded":
		return BuildSucceeded
	case "partiallysucceeded", "succeededwithissues":
		return BuildPartiallySucceeded
	case "failed", "abandoned":
		return BuildFailed
	case "canceled":
		return BuildCanceled
	case "skipped":
		return BuildSkipped
	}
	return BuildSucceeded
}

// Duration is how long the build ran (so far, when still running); 0 before it starts.
func (b Build) Duration(now time.Time) time.Duration { return span(b.Started, b.Finished, now) }

func span(start, end, now time.Time) time.Duration {
	if start.IsZero() {
		return 0
	}
	if end.IsZero() || end.Before(start) {
		end = now
	}
	return end.Sub(start).Round(time.Second)
}

// BuildQuery filters a build listing.
type BuildQuery struct {
	Project      string
	PipelineIDs  []int
	Branch       string // refs/heads/... or a plain branch name
	RequestedFor string // display name
	Result       string // succeeded, partiallySucceeded, failed, canceled
	Top          int
}

// BuildIssue is an error or warning a timeline record reported.
type BuildIssue struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// TimelineRecord is a stage, job or task of a build.
type TimelineRecord struct {
	ID       string       `json:"id"`
	ParentID string       `json:"parentId,omitempty"`
	Type     string       `json:"type"` // Stage, Phase, Job, Task, Checkpoint
	Name     string       `json:"name"`
	Order    int          `json:"order"`
	State    string       `json:"state"` // pending, inProgress, completed
	Result   string       `json:"result,omitempty"`
	Started  time.Time    `json:"started,omitempty"`
	Finished time.Time    `json:"finished,omitempty"`
	LogID    int          `json:"logId,omitempty"`
	Errors   int          `json:"errors,omitempty"`
	Warnings int          `json:"warnings,omitempty"`
	Issues   []BuildIssue `json:"issues,omitempty"`
}

// Status classifies the record like a build.
func (r TimelineRecord) Status() BuildState {
	switch strings.ToLower(r.State) {
	case "inprogress":
		return BuildRunning
	case "completed":
		return resultState(r.Result)
	}
	return BuildQueued
}

// Duration is how long the record ran (so far).
func (r TimelineRecord) Duration(now time.Time) time.Duration {
	return span(r.Started, r.Finished, now)
}

// TimelineRow is a record placed in the build's stage → job → task outline.
type TimelineRow struct {
	Record TimelineRecord
	Depth  int
}

// TimelineOutline orders records as an outline: stages, then their jobs, then tasks, each level
// by its order. Phase records (an internal level between stages and jobs) are left out and their
// children take their place.
func TimelineOutline(records []TimelineRecord) []TimelineRow {
	children := map[string][]TimelineRecord{}
	ids := map[string]bool{}
	for _, r := range records {
		ids[r.ID] = true
	}
	for _, r := range records {
		parent := r.ParentID
		if !ids[parent] {
			parent = ""
		}
		children[parent] = append(children[parent], r)
	}
	for _, list := range children {
		slices.SortStableFunc(list, func(a, b TimelineRecord) int {
			if a.Order != b.Order {
				return a.Order - b.Order
			}
			return a.Started.Compare(b.Started)
		})
	}
	var out []TimelineRow
	seen := map[string]bool{}
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, r := range children[parent] {
			if seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			if strings.EqualFold(r.Type, "Phase") {
				walk(r.ID, depth)
				continue
			}
			out = append(out, TimelineRow{Record: r, Depth: depth})
			walk(r.ID, depth+1)
		}
	}
	walk("", 0)
	return out
}

// FailedTasks returns the tasks that failed, in outline order.
func FailedTasks(records []TimelineRecord) []TimelineRecord {
	var out []TimelineRecord
	for _, row := range TimelineOutline(records) {
		if strings.EqualFold(row.Record.Type, "Task") && row.Record.Status() == BuildFailed {
			out = append(out, row.Record)
		}
	}
	return out
}

// MatchBuildPipeline reports whether a build pipeline matches any of the patterns (see
// MatchPipeline).
func MatchBuildPipeline(patterns []string, p BuildPipeline) bool {
	return MatchPipeline(patterns, ReleasePipeline{Name: p.Name, Path: p.Path})
}
