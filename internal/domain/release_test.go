package domain

import "testing"

func TestStageState(t *testing.T) {
	cases := []struct {
		stage ReleaseStage
		want  StageState
	}{
		{ReleaseStage{Status: "succeeded"}, StageSucceeded},
		{ReleaseStage{Status: "partiallySucceeded"}, StagePartiallySucceeded},
		{ReleaseStage{Status: "inProgress"}, StageDeploying},
		{ReleaseStage{Status: "inProgress", PendingApprovals: []ReleaseApproval{{Status: "pending"}}}, StageAwaitingApproval},
		{ReleaseStage{Status: "notStarted", PendingApprovals: []ReleaseApproval{{Status: "approved"}}}, StageNotStarted},
		{ReleaseStage{Status: "queued"}, StageQueued},
		{ReleaseStage{Status: "rejected"}, StageFailed},
		{ReleaseStage{Status: "canceled"}, StageCanceled},
		{ReleaseStage{Status: "undefined"}, StageNotStarted},
	}
	for _, c := range cases {
		if got := c.stage.State(); got != c.want {
			t.Errorf("%+v: got %v want %v", c.stage, got, c.want)
		}
	}
}

func TestLatestPerStage(t *testing.T) {
	p := ReleasePipeline{Stages: []PipelineStage{{ID: 1, Name: "Dev"}, {ID: 2, Name: "Test"}, {ID: 3, Name: "Prod"}}}
	releases := []Release{ // newest first
		{ID: 12, Name: "R-12", Stages: []ReleaseStage{{PipelineStageID: 1, Status: "inProgress"}, {PipelineStageID: 2, Status: "notStarted"}, {PipelineStageID: 3, Status: "notStarted"}}},
		{ID: 11, Name: "R-11", Stages: []ReleaseStage{{PipelineStageID: 1, Status: "succeeded"}, {PipelineStageID: 2, Status: "succeeded"},
			{PipelineStageID: 3, Status: "notStarted", PendingApprovals: []ReleaseApproval{{Status: "pending"}}}}},
		{ID: 10, Name: "R-10", Stages: []ReleaseStage{{PipelineStageID: 1, Status: "succeeded"}, {PipelineStageID: 2, Status: "succeeded"}, {PipelineStageID: 3, Status: "succeeded"}}},
	}
	got := LatestPerStage(p, releases)
	want := []struct {
		id    int
		state StageState
	}{{12, StageDeploying}, {11, StageSucceeded}, {11, StageAwaitingApproval}}
	for i, w := range want {
		if got[i].Release == nil || got[i].Release.ID != w.id || got[i].State.State() != w.state {
			t.Errorf("stage %s: %+v", p.Stages[i].Name, got[i])
		}
	}
	if none := LatestPerStage(ReleasePipeline{Stages: []PipelineStage{{ID: 9, Name: "QA"}}}, releases); none[0].Release != nil {
		t.Errorf("untouched stage should have no release: %+v", none[0])
	}
}

func TestMatchPipeline(t *testing.T) {
	p := ReleasePipeline{Name: "Frontend", Path: `\web`}
	for pats, want := range map[string]bool{
		"":                 true,
		"frontend":         true,
		"front*":           true,
		`\web\*`:           true,
		`\api\*`:           false,
		"backend":          false,
		"*end":             true,
		"f*t*d":            true,
		"frontend-preview": false,
	} {
		var list []string
		if pats != "" {
			list = []string{pats}
		}
		if got := MatchPipeline(list, p); got != want {
			t.Errorf("%q: got %v", pats, got)
		}
	}
	if !MatchPipeline([]string{"backend", "frontend"}, p) {
		t.Error("any pattern should match")
	}
}

func TestFocusStage(t *testing.T) {
	stages := func(statuses ...string) Release {
		r := Release{}
		for _, s := range statuses {
			r.Stages = append(r.Stages, ReleaseStage{Status: s})
		}
		return r
	}
	awaiting := stages("succeeded", "notStarted", "notStarted")
	awaiting.Stages[2].PendingApprovals = []ReleaseApproval{{Status: "pending"}}
	cases := []struct {
		rel  Release
		want int
	}{
		{awaiting, 2},
		{stages("succeeded", "rejected", "notStarted"), 1},
		{stages("succeeded", "partiallySucceeded"), 1},
		{Release{}, -1},
	}
	for i, c := range cases {
		if got := c.rel.FocusStage(); got != c.want {
			t.Errorf("case %d: got %d want %d", i, got, c.want)
		}
	}
}

func TestDeployedBefore(t *testing.T) {
	prod := func(status string) []ReleaseStage {
		return []ReleaseStage{{PipelineStageID: 1, Name: "Dev", Status: "succeeded"}, {PipelineStageID: 3, Name: "Prod", Status: status}}
	}
	rel := Release{ID: 12, Stages: prod("notStarted")}
	releases := []Release{
		{ID: 13, Stages: prod("succeeded")}, // newer than rel
		rel,
		{ID: 11, Stages: prod("rejected")},
		{ID: 9, Stages: prod("succeeded")},
		{ID: 10, Stages: prod("partiallySucceeded")},
	}
	if got := DeployedBefore(rel, rel.Stages[1], releases); got == nil || got.ID != 10 {
		t.Errorf("Prod: got %+v, want release 10", got)
	}
	if got := DeployedBefore(rel, rel.Stages[0], releases); got == nil || got.ID != 11 {
		t.Errorf("Dev: got %+v, want release 11", got)
	}
	// Stages without pipeline ids match by name.
	byName := ReleaseStage{Name: "prod"}
	if got := DeployedBefore(rel, byName, []Release{{ID: 5, Stages: []ReleaseStage{{Name: "Prod", Status: "succeeded"}}}}); got == nil || got.ID != 5 {
		t.Errorf("by name: got %+v", got)
	}
	if got := DeployedBefore(rel, rel.Stages[1], []Release{{ID: 11, Stages: prod("notStarted")}}); got != nil {
		t.Errorf("never deployed: got %+v", got)
	}
}

func TestArtifactBuildID(t *testing.T) {
	if id := (ReleaseArtifact{Type: "Build", VersionID: "42"}).BuildID(); id != 42 {
		t.Errorf("build: %d", id)
	}
	if id := (ReleaseArtifact{Type: "Git", VersionID: "abc"}).BuildID(); id != 0 {
		t.Errorf("git: %d", id)
	}
}
