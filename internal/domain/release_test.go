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
