package ado

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/ville6000/tfs/internal/domain"
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

func buildArtifact(alias, definition, buildID, number string) map[string]any {
	return map[string]any{"alias": alias, "type": "Build", "isPrimary": alias == "_web", "definitionReference": map[string]any{
		"project":    map[string]any{"id": "proj-guid", "name": "Other"},
		"definition": map[string]any{"id": definition, "name": alias},
		"version":    map[string]any{"id": buildID, "name": number},
		"branch":     map[string]any{"id": "refs/heads/main", "name": "refs/heads/main"},
		// values vary by artifact type; odd ones must not break decoding
		"IsMultiDefinitionType": map[string]any{"id": false, "name": nil},
	}}
}

func TestReleaseChanges(t *testing.T) {
	stages := func(prod string) []any {
		return []any{
			map[string]any{"id": 1, "definitionEnvironmentId": 20, "name": "Dev", "rank": 1, "status": "succeeded"},
			map[string]any{"id": 2, "definitionEnvironmentId": 21, "name": "Prod", "rank": 2, "status": prod},
		}
	}
	filtered := 0
	c, reqs := server(t, func(r *http.Request, _ string) (int, any) {
		p, q := r.URL.Path, r.URL.Query()
		switch {
		case strings.HasSuffix(p, "/_apis/release/releases/12"):
			return 200, map[string]any{"id": 12, "name": "Release-12", "releaseDefinition": map[string]any{"id": 2, "name": "web"},
				"createdOn": "2026-10-16T08:00:00Z", "environments": stages("notStarted"),
				"artifacts": []any{
					buildArtifact("_api", "8", "140", "20261016.2"),
					buildArtifact("_web", "7", "130", "20261016.1"),
					map[string]any{"alias": "_scripts", "type": "Git", "definitionReference": map[string]any{"version": map[string]any{"id": "abc"}}},
				}}
		case strings.HasSuffix(p, "/_apis/release/releases"):
			if q.Get("environmentStatusFilter") != "" {
				filtered++
				return 200, map[string]any{"value": []any{}} // a server that filters differently
			}
			return 200, map[string]any{"value": []any{
				map[string]any{"id": 11, "name": "Release-11", "environments": stages("rejected"),
					"artifacts": []any{buildArtifact("_web", "7", "125", "x"), buildArtifact("_api", "8", "135", "x")}},
				map[string]any{"id": 10, "name": "Release-10", "environments": stages("succeeded"),
					"artifacts": []any{buildArtifact("_web", "7", "120", "20261010.1"), buildArtifact("_api", "8", "140", "20261016.2")}},
			}}
		case strings.HasSuffix(p, "/proj-guid/_apis/build/workitems"):
			return 200, map[string]any{"count": 3, "value": []any{
				map[string]any{"id": "501", "url": "u"}, map[string]any{"id": "502", "url": "u"}, map[string]any{"id": "503", "url": "u"}}}
		case strings.HasSuffix(p, "/_apis/wit/workitems"):
			return 200, map[string]any{"value": []any{
				map[string]any{"id": 502, "fields": map[string]any{"System.Title": "Fix login", "System.WorkItemType": "Bug", "System.State": "Resolved"}},
				map[string]any{"id": 501, "fields": map[string]any{"System.Title": "Sign in with SSO", "System.WorkItemType": "User Story", "System.State": "Closed"}},
				nil, // 503 cannot be read
			}}
		}
		t.Errorf("unexpected request %s", r.URL)
		return 404, map[string]any{"message": "not found"}
	})
	ctx := context.Background()
	rel, err := c.GetRelease(ctx, "P", 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(rel.Artifacts) != 3 || rel.Artifacts[0].Alias != "_web" || rel.Artifacts[0].Branch != "main" || rel.Artifacts[0].BuildID() != 130 || rel.Artifacts[0].Project != "proj-guid" {
		t.Fatalf("artifacts %+v", rel.Artifacts)
	}

	ch, err := c.ReleaseChanges(ctx, "P", rel, rel.Stages[1])
	if err != nil {
		t.Fatal(err)
	}
	if ch.SinceID != 10 || ch.Since != "Release-10" || ch.Stage != "Prod" {
		t.Errorf("baseline %+v", ch)
	}
	if filtered != 1 {
		t.Errorf("the server-filtered query should be tried first, %d", filtered)
	}
	if len(ch.WorkItems) != 2 || ch.WorkItems[0].Title != "Fix login" || ch.WorkItems[1].ID != 501 {
		t.Errorf("work items %+v", ch.WorkItems)
	}
	if want := []string{"_scripts: Git artifacts are not compared", "1 work item(s) could not be read"}; strings.Join(ch.Notes, "|") != strings.Join(want, "|") {
		t.Errorf("notes %q", ch.Notes)
	}
	var between, list []recorded
	for _, r := range *reqs {
		switch {
		case strings.HasSuffix(r.Path, "/_apis/build/workitems"):
			between = append(between, r)
		case strings.HasSuffix(r.Path, "/_apis/release/releases"):
			list = append(list, r)
		}
	}
	// _api deploys the build already on Prod, so only _web is compared.
	if len(between) != 1 || !strings.Contains(between[0].Query, "fromBuildId=120") || !strings.Contains(between[0].Query, "toBuildId=130") ||
		!strings.Contains(between[0].Query, "api-version=6.0-preview.2") {
		t.Errorf("work items between builds: %+v", between)
	}
	if len(list) != 2 || !strings.Contains(list[0].Query, "definitionEnvironmentId=21") || !strings.Contains(list[0].Query, "maxCreatedTime=2026-10-16T08") ||
		!strings.Contains(list[1].Query, "$expand=environments,artifacts") || strings.Contains(list[1].Query, "environmentStatusFilter") {
		t.Errorf("release queries: %+v", list)
	}
}

func TestReleaseChangesFirstDeployment(t *testing.T) {
	c, reqs := server(t, func(r *http.Request, _ string) (int, any) {
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/_apis/release/releases"):
			return 200, map[string]any{"value": []any{}}
		case strings.HasSuffix(p, "/_apis/build/builds/130/workitems"):
			return 200, map[string]any{"value": []any{map[string]any{"id": "501"}}}
		case strings.HasSuffix(p, "/_apis/wit/workitems"):
			return 200, map[string]any{"value": []any{map[string]any{"id": 501, "fields": map[string]any{"System.Title": "First"}}}}
		}
		t.Errorf("unexpected request %s", r.URL)
		return 404, nil
	})
	rel := domain.Release{ID: 12, PipelineID: 2, Stages: []domain.ReleaseStage{{ID: 2, Name: "Prod"}},
		Artifacts: []domain.ReleaseArtifact{{Alias: "_web", Type: "Build", VersionID: "130"}}}
	ch, err := c.ReleaseChanges(context.Background(), "P", rel, rel.Stages[0])
	if err != nil || ch.SinceID != 0 || len(ch.WorkItems) != 1 || ch.WorkItems[0].Title != "First" {
		t.Fatalf("%+v %v", ch, err)
	}
	if !strings.Contains(ch.Heading(), "no earlier deployment to Prod") {
		t.Errorf("heading %q", ch.Heading())
	}
	// Without a pipeline stage id there is no server-side filter to try.
	if n := len(*reqs); n != 3 || !strings.Contains((*reqs)[1].Path, "/P/_apis/build/builds/130/workitems") {
		t.Errorf("requests %+v", *reqs)
	}
}

func TestRejectSuperseded(t *testing.T) {
	release := func(id int, approvalID int) map[string]any {
		return map[string]any{
			"id": id, "name": "Release-" + strconv.Itoa(id), "releaseDefinition": map[string]any{"id": 2, "name": "frontend"},
			"environments": []any{map[string]any{"id": id * 10, "definitionEnvironmentId": 21, "name": "Prod", "status": "notStarted",
				"preDeployApprovals": []any{map[string]any{"id": approvalID, "status": "pending"}}}},
		}
	}
	c, reqs := server(t, func(r *http.Request, _ string) (int, any) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_apis/release/releases"):
			return 200, map[string]any{"value": []any{release(12, 912), release(11, 911), release(9, 909)}}
		case strings.HasSuffix(r.URL.Path, "/_apis/connectionData"):
			return 200, map[string]any{"authenticatedUser": map[string]string{"id": "u1", "providerDisplayName": "Me"}}
		case strings.HasSuffix(r.URL.Path, "/_apis/release/approvals"):
			return 200, map[string]any{"value": []any{}}
		case strings.HasSuffix(r.URL.Path, "/approvals/911"):
			return 403, map[string]any{"message": "nope"}
		}
		return 200, map[string]any{}
	})
	ctx := context.Background()
	rel := domain.Release{ID: 12, Name: "Release-12", PipelineID: 2}
	stage := domain.ReleaseStage{PipelineStageID: 21, Name: "Prod"}
	older, err := c.PendingBefore(ctx, "P", rel, stage)
	if err != nil || len(older) != 2 || older[0].ID != 909 || older[1].ID != 911 || older[1].Release != "Release-11" {
		t.Fatalf("older %+v %v", older, err)
	}
	if q := (*reqs)[0].Query; !strings.Contains(q, "definitionId=2") {
		t.Errorf("releases query %s", q)
	}
	n := len(*reqs)
	err = c.RejectSuperseded(ctx, "P", "Release-12", older, "go")
	if err == nil || !strings.Contains(err.Error(), "Release-11") || strings.Contains(err.Error(), "Release-9") {
		t.Errorf("a failed rejection is reported: %v", err)
	}
	patches := (*reqs)[n:]
	if len(patches) != 2 || !strings.HasSuffix(patches[0].Path, "/approvals/909") ||
		!strings.Contains(patches[0].Body, `"Superseded by Release-12: go"`) || !strings.Contains(patches[0].Body, `"rejected"`) {
		t.Errorf("updates %+v", patches)
	}
}
