package ado

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ville6000/tfs/internal/domain"
)

func TestBuilds(t *testing.T) {
	c, reqs := server(t, func(r *http.Request, _ string) (int, any) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/_apis/build/definitions"):
			return 200, map[string]any{"value": []any{map[string]any{"id": 3, "name": "frontend-ci", "path": `\web`}}}
		case strings.HasSuffix(p, "/_apis/build/builds"):
			return 200, map[string]any{"value": []any{map[string]any{
				"id": 50, "buildNumber": "20261013.4", "status": "completed", "result": "failed", "reason": "pullRequest",
				"definition": map[string]any{"id": 3, "name": "frontend-ci"}, "sourceBranch": "refs/heads/feature/x",
				"sourceVersion": "abc123", "requestedFor": map[string]string{"displayName": "Alice"},
				"startTime": "2026-10-13T09:00:00Z", "finishTime": "2026-10-13T09:03:30Z",
			}}}
		case strings.HasSuffix(p, "/timeline"):
			return 200, map[string]any{"records": []any{
				map[string]any{"id": "j", "type": "Job", "name": "Build", "state": "completed", "result": "failed"},
				map[string]any{"id": "t", "parentId": "j", "type": "Task", "name": "npm test", "state": "completed", "result": "failed",
					"errorCount": 1, "log": map[string]int{"id": 7}, "issues": []any{map[string]string{"type": "error", "message": "2 tests failed"}}},
			}}
		}
		return 404, nil
	})
	ctx := context.Background()
	pipes, err := c.ListBuildPipelines(ctx, "P")
	if err != nil || len(pipes) != 1 || pipes[0].Path != `\web` {
		t.Fatalf("pipelines %+v %v", pipes, err)
	}
	builds, err := c.ListBuilds(ctx, domain.BuildQuery{Project: "P", PipelineIDs: []int{3, 4}, Branch: "main", RequestedFor: "Alice", Result: "failed", Top: 5})
	if err != nil || len(builds) != 1 {
		t.Fatalf("builds %+v %v", builds, err)
	}
	b := builds[0]
	if b.Branch != "feature/x" || b.State() != domain.BuildFailed || b.URL != c.Collection()+"/P/_build/results?buildId=50" || b.RequestedFor != "Alice" {
		t.Errorf("build %+v", b)
	}
	q := (*reqs)[1].Query
	for _, want := range []string{"definitions=3,4", "branchName=refs%2Fheads%2Fmain", "requestedFor=Alice", "resultFilter=failed", "statusFilter=completed", "$top=5", "queryOrder=queueTimeDescending"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %s missing %s", q, want)
		}
	}
	recs, err := c.BuildTimeline(ctx, "P", 50)
	if err != nil || len(recs) != 2 || recs[1].LogID != 7 || recs[1].Issues[0].Message != "2 tests failed" {
		t.Fatalf("timeline %+v %v", recs, err)
	}
	if p := (*reqs)[2].Path; p != "/Col/P/_apis/build/builds/50/timeline" {
		t.Errorf("timeline path %s", p)
	}
}

func TestBuildLogIsPlainText(t *testing.T) {
	var accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		if !strings.HasSuffix(r.URL.Path, "/_apis/build/builds/50/logs/7") {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "no"})
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("line 1\n##[error]boom\n"))
	}))
	defer srv.Close()
	c := New(srv.URL+"/Col", "6.0", "pat")
	text, err := c.BuildLog(context.Background(), "P", 50, 7)
	if err != nil || text != "line 1\n##[error]boom\n" || accept != "text/plain" {
		t.Errorf("log %q %v accept=%q", text, err, accept)
	}
	if _, err := c.BuildLog(context.Background(), "P", 50, 8); err == nil {
		t.Error("missing log should fail")
	}
}
