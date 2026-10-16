package domain

import (
	"fmt"
	"strconv"
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
	ID          int               `json:"id"`
	Name        string            `json:"name"`
	PipelineID  int               `json:"pipelineId"`
	Pipeline    string            `json:"pipeline"`
	Status      string            `json:"status"`
	Description string            `json:"description,omitempty"`
	CreatedBy   string            `json:"createdBy"`
	Created     time.Time         `json:"created"`
	Stages      []ReleaseStage    `json:"stages"`
	Artifacts   []ReleaseArtifact `json:"artifacts,omitempty"`
	URL         string            `json:"url"`
}

// ReleaseArtifact is a build (or other source) that a release deploys.
type ReleaseArtifact struct {
	Alias        string `json:"alias"`
	Type         string `json:"type"` // Build, Git, GitHub, Jenkins, …
	Primary      bool   `json:"primary,omitempty"`
	Project      string `json:"project,omitempty"` // id of the project the source lives in
	DefinitionID string `json:"definitionId,omitempty"`
	Definition   string `json:"definition,omitempty"`
	VersionID    string `json:"versionId,omitempty"` // the build id for build artifacts
	Version      string `json:"version,omitempty"`   // build number, commit, …
	Branch       string `json:"branch,omitempty"`
}

// BuildID returns the build a build artifact deploys, or 0 for other artifacts.
func (a ReleaseArtifact) BuildID() int {
	if !strings.EqualFold(a.Type, "Build") {
		return 0
	}
	id, _ := strconv.Atoi(a.VersionID)
	return id
}

// Artifact returns the release's artifact with the given alias.
func (r Release) Artifact(alias string) (ReleaseArtifact, bool) {
	for _, a := range r.Artifacts {
		if strings.EqualFold(a.Alias, alias) {
			return a, true
		}
	}
	return ReleaseArtifact{}, false
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

// sameAs reports whether s is the pipeline stage with the given id or, when an id is unknown, name.
func (s ReleaseStage) sameAs(pipelineStageID int, name string) bool {
	if s.PipelineStageID != 0 && pipelineStageID != 0 {
		return s.PipelineStageID == pipelineStageID
	}
	return strings.EqualFold(s.Name, name)
}

// deployed reports whether the release's deployment to the stage went through.
func (s ReleaseStage) deployed() bool {
	return strings.EqualFold(s.Status, "succeeded") || strings.EqualFold(s.Status, "partiallySucceeded")
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

// FocusStage returns the index of the stage a reviewer most likely cares about: the first one
// awaiting approval, else the first one not deployed yet, else the last. -1 when there are none.
func (r Release) FocusStage() int {
	for i, s := range r.Stages {
		if s.State() == StageAwaitingApproval {
			return i
		}
	}
	for i, s := range r.Stages {
		if st := s.State(); st != StageSucceeded && st != StagePartiallySucceeded {
			return i
		}
	}
	return len(r.Stages) - 1
}

// DeployedBefore returns the newest of releases created before rel that deployed to the stage:
// what the stage runs until rel is deployed there. nil when none of them did.
func DeployedBefore(rel Release, stage ReleaseStage, releases []Release) *Release {
	var best *Release
	for i := range releases {
		r := &releases[i]
		if r.ID >= rel.ID || (best != nil && r.ID <= best.ID) {
			continue
		}
		for _, s := range r.Stages {
			if s.sameAs(stage.PipelineStageID, stage.Name) && s.deployed() {
				best = r
				break
			}
		}
	}
	return best
}

// ReleaseChanges is what deploying a release to a stage brings there: the work items of its
// builds since the release the stage last deployed.
type ReleaseChanges struct {
	Stage string `json:"stage"`
	// SinceID and Since name the release deployed on the stage before; 0 and "" when none was
	// found, and the work items are then those of the release's own builds.
	SinceID   int        `json:"sinceId,omitempty"`
	Since     string     `json:"since,omitempty"`
	WorkItems []WorkItem `json:"workItems"`
	// Notes tell what the list leaves out, e.g. artifacts that are not builds.
	Notes []string `json:"notes,omitempty"`
}

// Heading describes what the work items are compared against.
func (c ReleaseChanges) Heading() string {
	if c.SinceID == 0 {
		return fmt.Sprintf("Work items in this release's builds (no earlier deployment to %s found)", c.Stage)
	}
	return fmt.Sprintf("Work items new to %s since %s", c.Stage, c.Since)
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
				if s.sameAs(ps.ID, ps.Name) {
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
