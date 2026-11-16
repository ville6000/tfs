package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHome(t *testing.T) {
	var buildQuery, wiql string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/_apis/connectionData"):
			reply(map[string]any{"authenticatedUser": map[string]string{"id": "me", "providerDisplayName": "Me"}})
		case strings.HasSuffix(p, "/_apis/git/pullrequests"):
			reply(map[string]any{"value": []any{
				map[string]any{"pullRequestId": 41, "status": "active", "title": "Add reset endpoint",
					"createdBy": map[string]string{"id": "bob", "displayName": "Bob"}, "repository": map[string]string{"name": "web"},
					"reviewers": []any{map[string]any{"id": "g-web", "displayName": "Web Team", "isContainer": true}}},
				map[string]any{"pullRequestId": 40, "status": "active", "title": "My change",
					"createdBy": map[string]string{"id": "me", "displayName": "Me"}, "repository": map[string]string{"name": "web"}},
			}})
		case strings.HasSuffix(p, "/_apis/identities"):
			if r.URL.Query().Get("queryMembership") != "" {
				reply(map[string]any{"value": []any{map[string]any{"id": "me", "memberOf": []string{"x;s-web"}}}})
				return
			}
			reply(map[string]any{"value": []any{map[string]any{"id": "g-web", "descriptor": "X;S-WEB"}}})
		case strings.HasSuffix(p, "/_apis/release/approvals"):
			w.WriteHeader(http.StatusUnauthorized)
			reply(map[string]string{"message": "no Release scope"})
		case strings.HasSuffix(p, "/_apis/build/builds"):
			buildQuery = r.URL.RawQuery
			reply(map[string]any{"value": []any{map[string]any{"id": 9, "status": "completed", "result": "failed",
				"definition": map[string]any{"id": 1, "name": "web-ci"}, "sourceBranch": "refs/heads/main",
				"queueTime": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}}})
		case strings.HasSuffix(p, "/_apis/wit/workitemtypecategories"):
			reply(map[string]any{"value": []any{
				map[string]any{"referenceName": "Microsoft.FeatureCategory", "workItemTypes": []any{map[string]string{"name": "Feature"}}},
				map[string]any{"referenceName": "Microsoft.RequirementCategory",
					"workItemTypes": []any{map[string]string{"name": "User Story"}, map[string]string{"name": "Story Spike"}}},
			}})
		case strings.HasSuffix(p, "/wiql"):
			var body struct{ Query string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			wiql = body.Query
			reply(map[string]any{"workItems": []map[string]int{{"id": 7}}})
		case strings.HasSuffix(p, "/_apis/wit/workitems"):
			reply(map[string]any{"value": []any{map[string]any{"id": 7, "fields": map[string]any{
				"System.Title": "Fix the login page", "System.WorkItemType": "User Story", "System.State": "Active", "System.TeamProject": "P"}}}})
		default:
			w.WriteHeader(http.StatusNotFound)
			reply(map[string]string{"message": "unexpected " + p})
		}
	}))
	defer srv.Close()

	t.Chdir(t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TFS_PAT", "x")
	t.Setenv("TFS_ALLOW_INSECURE", "1")

	// Not configured yet: tfs on its own shows help, whatever the flags.
	if out, err := runCLI(t, "--no-tui"); err != nil || !strings.Contains(out, "Usage:") {
		t.Fatalf("unconfigured: %v\n%s", err, out)
	}
	if _, err := runCLI(t, "config", "set", "collectionUrl", srv.URL+"/Col"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "config", "set", "defaultProject", "P"); err != nil {
		t.Fatal(err)
	}
	// Piped without --no-tui or --json, it still shows help, so scripts never hit the server.
	if out, err := runCLI(t); err != nil || !strings.Contains(out, "Usage:") {
		t.Fatalf("piped: %v\n%s", err, out)
	}

	out, err := runCLI(t, "home", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var h struct {
		ReviewRequests []struct {
			ID  int    `json:"id"`
			Via string `json:"via"`
		} `json:"reviewRequests"`
		PullRequests []struct{ ID int } `json:"pullRequests"`
		Builds       []struct{ ID int } `json:"builds"`
		WorkItems    []struct{ ID int } `json:"workItems"`
		Approvals    []any              `json:"approvals"`
		Errors       map[string]string  `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out), &h); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(h.ReviewRequests) != 1 || h.ReviewRequests[0].ID != 41 || h.ReviewRequests[0].Via != "Web Team" {
		t.Errorf("review requests %+v", h.ReviewRequests)
	}
	if len(h.PullRequests) != 1 || h.PullRequests[0].ID != 40 || len(h.Builds) != 1 || len(h.WorkItems) != 1 {
		t.Errorf("home %+v", h)
	}
	if h.Approvals == nil || !strings.Contains(h.Errors["approvals"], "no Release scope") || len(h.Errors) != 1 {
		t.Errorf("approvals %v, errors %v", h.Approvals, h.Errors)
	}
	if !strings.Contains(buildQuery, "requestedFor=Me") {
		t.Errorf("builds query %s", buildQuery)
	}
	if !strings.Contains(wiql, "[System.AssignedTo] = @Me") || !strings.Contains(wiql, "'Resolved'") ||
		!strings.Contains(wiql, "[System.WorkItemType] IN ('User Story', 'Story Spike')") {
		t.Errorf("wiql %s", wiql)
	}

	out, err = runCLI(t, "--no-tui")
	if err != nil {
		t.Fatal(err)
	}
	// In the order shown: pull requests first, then work items, then the rest.
	at := -1
	for _, want := range []string{
		"Waiting for your review (1)", "41  web   Bob     Web Team", "Your pull requests (1)",
		"Your user stories (1)", "Fix the login page",
		"Release approvals", `error: server returned 401: no Release scope (the token may lack the Release "Read" scope)`,
		"Your builds (1)", "web-ci",
	} {
		i := strings.Index(out, want)
		switch {
		case i < 0:
			t.Errorf("missing %q in:\n%s", want, out)
		case i < at:
			t.Errorf("%q out of order in:\n%s", want, out)
		}
		at = max(at, i)
	}
}
