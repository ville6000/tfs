package ado

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestReleases(t *testing.T) {
	c, reqs := server(t, func(r *http.Request, _ string) (int, any) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/_apis/release/definitions"):
			return 200, map[string]any{"value": []any{
				map[string]any{"id": 2, "name": "frontend", "path": `\`, "environments": []any{
					map[string]any{"id": 21, "name": "Prod", "rank": 2}, map[string]any{"id": 20, "name": "Dev", "rank": 1}}},
				map[string]any{"id": 1, "name": "backend", "path": `\`},
			}}
		case strings.HasSuffix(p, "/_apis/release/releases"):
			return 200, map[string]any{"value": []any{map[string]any{
				"id": 7, "name": "Release-7", "createdBy": map[string]string{"displayName": "Alice"},
				"releaseDefinition": map[string]any{"id": 2, "name": "frontend"},
				"environments": []any{
					map[string]any{"id": 71, "definitionEnvironmentId": 21, "name": "Prod", "rank": 2, "status": "notStarted",
						"preDeployApprovals": []any{map[string]any{"id": 900, "status": "pending", "approvalType": "preDeploy", "approver": map[string]string{"displayName": "Release Managers"}}}},
					map[string]any{"id": 70, "definitionEnvironmentId": 20, "name": "Dev", "rank": 1, "status": "succeeded",
						"preDeployApprovals": []any{map[string]any{"id": 899, "status": "approved"}}},
				},
			}}}
		case strings.HasSuffix(p, "/_apis/connectionData"):
			return 200, map[string]any{"authenticatedUser": map[string]string{"id": "u1", "providerDisplayName": "Me Myself", "uniqueName": "me@example.com"}}
		case strings.HasSuffix(p, "/_apis/release/approvals"):
			return 200, map[string]any{"value": []any{map[string]any{"id": 900, "status": "pending", "approvalType": "preDeploy",
				"release": map[string]any{"id": 7, "name": "Release-7"}, "releaseDefinition": map[string]any{"id": 2, "name": "frontend"},
				"releaseEnvironment": map[string]any{"id": 71, "name": "Prod"}}}}
		}
		return 200, map[string]any{}
	})
	ctx := context.Background()

	pipes, err := c.ListReleasePipelines(ctx, "P")
	if err != nil || len(pipes) != 2 || pipes[0].Name != "backend" || pipes[1].Stages[0].Name != "Dev" {
		t.Fatalf("pipelines %+v %v", pipes, err)
	}
	if q := (*reqs)[0].Query; !strings.Contains(q, "$expand=environments") {
		t.Errorf("definitions query %s", q)
	}

	rels, err := c.ListReleases(ctx, "P", 2, 10)
	if err != nil || len(rels) != 1 {
		t.Fatalf("releases %+v %v", rels, err)
	}
	r := rels[0]
	if r.Stages[0].Name != "Dev" || r.Stages[1].PipelineStageID != 21 || len(r.Stages[1].PendingApprovals) != 1 || len(r.Stages[0].PendingApprovals) != 0 {
		t.Errorf("stages %+v", r.Stages)
	}
	if a := r.Stages[1].PendingApprovals[0]; a.ID != 900 || a.ReleaseID != 7 || a.Stage != "Prod" || a.Pipeline != "frontend" {
		t.Errorf("approval %+v", a)
	}
	if r.URL != c.Collection()+"/P/_releaseProgress?releaseId=7&_a=release-pipeline-progress" {
		t.Errorf("url %s", r.URL)
	}
	if q := (*reqs)[1].Query; !strings.Contains(q, "definitionId=2") || !strings.Contains(q, "$top=10") {
		t.Errorf("releases query %s", q)
	}

	mine, err := c.MyPendingApprovals(ctx, "P")
	if err != nil || len(mine) != 1 || mine[0].Stage != "Prod" {
		t.Fatalf("approvals %+v %v", mine, err)
	}
	if q := (*reqs)[3].Query; !strings.Contains(q, "assignedToFilter=Me+Myself") || !strings.Contains(q, "includeMyGroupApprovals=true") || !strings.Contains(q, "statusFilter=pending") {
		t.Errorf("approvals query %s", q)
	}
	a, err := c.PickApproval(ctx, "P", r, r.Stages[1])
	if err != nil || a.ID != 900 {
		t.Errorf("pick %+v %v", a, err)
	}
	if _, err := c.PickApproval(ctx, "P", r, r.Stages[0]); err == nil {
		t.Error("a stage without pending approvals has nothing to pick")
	}

	if err := c.SetApproval(ctx, "P", 900, false, "not today"); err != nil {
		t.Fatal(err)
	}
	last := (*reqs)[len(*reqs)-1]
	if last.Method != "PATCH" || last.Path != "/Col/P/_apis/release/approvals/900" || strings.TrimSpace(last.Body) != `{"comments":"not today","status":"rejected"}` {
		t.Errorf("approval update %+v", last)
	}
}
