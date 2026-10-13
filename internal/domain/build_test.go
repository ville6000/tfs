package domain

import (
	"strings"
	"testing"
	"time"
)

func TestBuildState(t *testing.T) {
	cases := map[[2]string]BuildState{
		{"notStarted", ""}:                  BuildQueued,
		{"postponed", ""}:                   BuildQueued,
		{"inProgress", ""}:                  BuildRunning,
		{"cancelling", ""}:                  BuildCancelling,
		{"completed", "succeeded"}:          BuildSucceeded,
		{"completed", "partiallySucceeded"}: BuildPartiallySucceeded,
		{"completed", "failed"}:             BuildFailed,
		{"completed", "canceled"}:           BuildCanceled,
	}
	for in, want := range cases {
		if got := (Build{Status: in[0], Result: in[1]}).State(); got != want {
			t.Errorf("%v: got %v want %v", in, got, want)
		}
	}
	now := time.Date(2026, 1, 1, 10, 5, 0, 0, time.UTC)
	b := Build{Started: now.Add(-3 * time.Minute)}
	if d := b.Duration(now); d != 3*time.Minute {
		t.Errorf("running duration %v", d)
	}
	if d := (Build{}).Duration(now); d != 0 {
		t.Errorf("not started %v", d)
	}
}

func TestTimelineOutline(t *testing.T) {
	recs := []TimelineRecord{
		{ID: "t2", ParentID: "j1", Type: "Task", Name: "Test", Order: 2, State: "completed", Result: "failed"},
		{ID: "s1", Type: "Stage", Name: "Build", Order: 1},
		{ID: "p1", ParentID: "s1", Type: "Phase", Name: "phase", Order: 1},
		{ID: "j1", ParentID: "p1", Type: "Job", Name: "Linux", Order: 1},
		{ID: "t1", ParentID: "j1", Type: "Task", Name: "Checkout", Order: 1, State: "completed", Result: "succeeded"},
		{ID: "s2", Type: "Stage", Name: "Deploy", Order: 2},
		{ID: "x", ParentID: "missing", Type: "Task", Name: "Orphan", Order: 9},
	}
	var got []string
	for _, r := range TimelineOutline(recs) {
		got = append(got, strings.Repeat(">", r.Depth)+r.Record.Name)
	}
	if strings.Join(got, ",") != "Build,>Linux,>>Checkout,>>Test,Deploy,Orphan" {
		t.Errorf("outline %v", got)
	}
	failed := FailedTasks(recs)
	if len(failed) != 1 || failed[0].Name != "Test" {
		t.Errorf("failed %+v", failed)
	}
}
