package domain

import (
	"strings"
	"time"
)

// ReleasePipeline is a classic release definition and its stages in order.
type ReleasePipeline struct {
	ID     int             `json:"id"`
	Name   string          `json:"name"`
	Path   string          `json:"path,omitempty"`
	Stages []PipelineStage `json:"stages"`
	URL    string          `json:"url,omitempty"`
}

// PipelineStage is a stage (environment) of a release pipeline.
type PipelineStage struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Rank int    `json:"rank"`
}

// Release is one release of a pipeline and the state of each of its stages.
type Release struct {
	ID          int            `json:"id"`
	Name        string         `json:"name"`
	PipelineID  int            `json:"pipelineId"`
	Pipeline    string         `json:"pipeline"`
	Status      string         `json:"status"`
	Description string         `json:"description,omitempty"`
	CreatedBy   string         `json:"createdBy"`
	Created     time.Time      `json:"created"`
	Stages      []ReleaseStage `json:"stages"`
	URL         string         `json:"url"`
}

// ReleaseStage is a stage of a specific release.
type ReleaseStage struct {
	ID               int               `json:"id"`
	PipelineStageID  int               `json:"pipelineStageId"`
	Name             string            `json:"name"`
	Rank             int               `json:"rank"`
	Status           string            `json:"status"`
	Modified         time.Time         `json:"modified,omitempty"`
	PendingApprovals []ReleaseApproval `json:"pendingApprovals,omitempty"`
}

// ReleaseApproval is a pre- or post-deployment approval of a release stage.
type ReleaseApproval struct {
	ID         int       `json:"id"`
	Type       string    `json:"type"` // preDeploy or postDeploy
	Status     string    `json:"status"`
	Approver   string    `json:"approver"`
	ReleaseID  int       `json:"releaseId"`
	Release    string    `json:"release"`
	PipelineID int       `json:"pipelineId"`
	Pipeline   string    `json:"pipeline"`
	StageID    int       `json:"stageId"`
	Stage      string    `json:"stage"`
	Created    time.Time `json:"created"`
}

// StageState is a simplified stage status for display.
type StageState int

// Stage states.
const (
	StageNotStarted StageState = iota
	StageQueued
	StageDeploying
	StageAwaitingApproval
	StageSucceeded
	StagePartiallySucceeded
	StageFailed
	StageCanceled
)

// State classifies the stage. An outstanding approval wins over the raw status, because a stage
// waiting for approval reports itself as in progress or not started.
func (s ReleaseStage) State() StageState {
	for _, a := range s.PendingApprovals {
		if strings.EqualFold(a.Status, "pending") {
			return StageAwaitingApproval
		}
	}
	switch strings.ToLower(s.Status) {
	case "succeeded":
		return StageSucceeded
	case "partiallysucceeded":
		return StagePartiallySucceeded
	case "inprogress":
		return StageDeploying
	case "queued", "scheduled":
		return StageQueued
	case "rejected":
		return StageFailed
	case "canceled":
		return StageCanceled
	}
	return StageNotStarted
}

func (s StageState) String() string {
	return [...]string{"not started", "queued", "deploying", "awaiting approval", "succeeded",
		"partially succeeded", "failed", "canceled"}[s]
}

// Stage returns the release's stage with the given name (case-insensitive).
func (r Release) Stage(name string) (ReleaseStage, bool) {
	for _, s := range r.Stages {
		if strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	return ReleaseStage{}, false
}

// StageDeployment is the newest release that reached a pipeline stage.
type StageDeployment struct {
	Stage   PipelineStage
	Release *Release     // nil when no listed release has touched the stage
	State   ReleaseStage // the release's copy of the stage
}

// LatestPerStage returns, for each pipeline stage in order, the newest release (releases are
// newest first) that has started on that stage — what is deployed, or being deployed, where.
func LatestPerStage(p ReleasePipeline, releases []Release) []StageDeployment {
	out := make([]StageDeployment, len(p.Stages))
	for i, ps := range p.Stages {
		out[i].Stage = ps
		for ri := range releases {
			r := &releases[ri]
			for _, s := range r.Stages {
				if (s.PipelineStageID != 0 && s.PipelineStageID == ps.ID) || (s.PipelineStageID == 0 && strings.EqualFold(s.Name, ps.Name)) {
					if s.State() != StageNotStarted {
						out[i].Release, out[i].State = r, s
					}
				}
			}
			if out[i].Release != nil {
				break
			}
		}
	}
	return out
}

// MatchPipeline reports whether a pipeline matches any of the patterns. A pattern matches the
// name or the folder-qualified name (\folder\name), case-insensitively; '*' matches any run of
// characters. No patterns match everything.
func MatchPipeline(patterns []string, p ReleasePipeline) bool {
	if len(patterns) == 0 {
		return true
	}
	full := strings.TrimRight(p.Path, `\`) + `\` + p.Name
	for _, pat := range patterns {
		pat = strings.ToLower(strings.TrimSpace(pat))
		if pat == "" {
			continue
		}
		if wildcard(pat, strings.ToLower(p.Name)) || wildcard(pat, strings.ToLower(full)) {
			return true
		}
	}
	return false
}

// wildcard matches s against a pattern where '*' matches any run of characters.
func wildcard(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(s, mid)
		if i < 0 {
			return false
		}
		s = s[i+len(mid):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// PipelineOverview is one dashboard row: a pipeline, its newest release, and the newest release
// on each of its stages.
type PipelineOverview struct {
	Pipeline    ReleasePipeline   `json:"pipeline"`
	Latest      *Release          `json:"latest,omitempty"`
	Deployments []StageDeployment `json:"deployments"`
}
