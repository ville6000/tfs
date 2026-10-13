package ado

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ville6000/tfs/internal/domain"
)

type recorded struct {
	Method, Path, Query, ContentType, Auth, Body string
}

func server(t *testing.T, handler func(r *http.Request, body string) (int, any)) (*Client, *[]recorded) {
	t.Helper()
	var reqs []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs = append(reqs, recorded{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), r.Header.Get("Authorization"), string(b)})
		status, payload := handler(r, string(b))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL+"/Col/", "6.0", "pat"), &reqs
}

func TestBuildWIQL(t *testing.T) {
	q := domain.WorkItemQuery{
		Project: "O'Proj", Types: []string{"Bug"}, ExcludedStates: []string{"Closed", "Removed"},
		AssignedTo: "@me", Tags: []string{"a", "b"},
	}
	want := "SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = 'O''Proj' AND " +
		"[System.WorkItemType] IN ('Bug') AND [System.State] NOT IN ('Closed', 'Removed') AND " +
		"[System.AssignedTo] = @Me AND [System.Tags] CONTAINS 'a' AND [System.Tags] CONTAINS 'b' " +
		"ORDER BY [System.ChangedDate] DESC"
	if got := BuildWIQL(q); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

func TestListWorkItems(t *testing.T) {
	c, reqs := server(t, func(r *http.Request, body string) (int, any) {
		if strings.HasSuffix(r.URL.Path, "/wiql") {
			return 200, map[string]any{"workItems": []map[string]int{{"id": 2}, {"id": 1}}}
		}
		return 200, map[string]any{"value": []any{
			map[string]any{"id": 1, "fields": map[string]any{"System.Title": "One", "System.WorkItemType": "Bug", "System.State": "New", "System.TeamProject": "P",
				"System.AssignedTo": map[string]string{"displayName": "Alice"}, "System.Parent": 9}},
			map[string]any{"id": 2, "fields": map[string]any{"System.Title": "Two", "System.AssignedTo": "Bob <DOMAIN\\bob>"}},
		}}
	})
	items, err := c.ListWorkItems(context.Background(), domain.WorkItemQuery{Project: "P", Top: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != 2 || items[1].ID != 1 {
		t.Fatalf("order not preserved: %+v", items)
	}
	if items[1].AssignedTo != "Alice" || items[0].AssignedTo != "Bob" || items[1].ParentID != 9 {
		t.Errorf("mapping: %+v", items)
	}
	if items[1].URL != c.Collection()+"/P/_workitems/edit/1" {
		t.Errorf("url: %s", items[1].URL)
	}
	r := (*reqs)[0]
	if r.Method != "POST" || r.Path != "/Col/P/_apis/wit/wiql" || !strings.Contains(r.Query, "$top=5") || r.Auth != "Basic OnBhdA==" {
		t.Errorf("wiql request: %+v", r)
	}
	if !strings.Contains((*reqs)[1].Query, "ids=2,1") {
		t.Errorf("batch request: %+v", (*reqs)[1])
	}
}

func TestListHierarchyBackfillsParents(t *testing.T) {
	calls := 0
	c, _ := server(t, func(r *http.Request, _ string) (int, any) {
		if strings.HasSuffix(r.URL.Path, "/wiql") {
			return 200, map[string]any{"workItems": []map[string]int{{"id": 3}}}
		}
		calls++
		switch r.URL.Query().Get("ids") {
		case "3":
			return 200, map[string]any{"value": []any{map[string]any{"id": 3, "fields": map[string]any{"System.Parent": 2}}}}
		case "2":
			return 200, map[string]any{"value": []any{map[string]any{"id": 2, "fields": map[string]any{"System.Parent": 1}}}}
		default: // 1 is unreachable
			return 200, map[string]any{"value": []any{nil}}
		}
	})
	items, err := c.ListHierarchy(context.Background(), domain.WorkItemQuery{Project: "P", Top: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || calls != 3 {
		t.Errorf("items=%d calls=%d", len(items), calls)
	}
}

func TestErrors(t *testing.T) {
	c, _ := server(t, func(*http.Request, string) (int, any) {
		return 401, map[string]string{"message": "Authorization: Basic abc123 rejected"}
	})
	_, err := c.GetWorkItem(context.Background(), 1)
	if !IsKind(err, ErrUnauthorized) || strings.Contains(err.Error(), "abc123") {
		t.Errorf("err = %v", err)
	}
	c, _ = server(t, func(*http.Request, string) (int, any) { return 404, nil })
	if _, err := c.GetWorkItem(context.Background(), 7); err == nil || !strings.Contains(err.Error(), "#7 not found") {
		t.Errorf("err = %v", err)
	}
}

func TestCreateAndUpdate(t *testing.T) {
	c, reqs := server(t, func(*http.Request, string) (int, any) {
		return 200, map[string]any{"id": 5, "fields": map[string]any{"System.Title": "T"}}
	})
	_, err := c.CreateWorkItem(context.Background(), domain.WorkItemDraft{Project: "P", Type: "User Story", Title: " T ", Tags: []string{"a", "b"}, ParentID: 3})
	if err != nil {
		t.Fatal(err)
	}
	r := (*reqs)[0]
	if r.Path != "/Col/P/_apis/wit/workitems/$User%20Story" && r.Path != "/Col/P/_apis/wit/workitems/$User Story" {
		t.Errorf("path %s", r.Path)
	}
	if r.ContentType != jsonPatch || !strings.Contains(r.Body, `System.Tags","value":"a; b"`) || !strings.Contains(r.Body, "Hierarchy-Reverse") || !strings.Contains(r.Body, "/_apis/wit/workItems/3") {
		t.Errorf("create body: %s", r.Body)
	}
	state := "Active"
	if _, err := c.UpdateWorkItem(context.Background(), 5, domain.WorkItemUpdate{State: &state}); err != nil {
		t.Fatal(err)
	}
	r = (*reqs)[1]
	if r.Method != "PATCH" || strings.TrimSpace(r.Body) != `[{"op":"add","path":"/fields/System.State","value":"Active"}]` {
		t.Errorf("update: %+v", r)
	}
}

func TestAddComment(t *testing.T) {
	c, reqs := server(t, func(r *http.Request, _ string) (int, any) {
		if r.Method == "GET" {
			return 200, map[string]any{"id": 5, "fields": map[string]any{"System.TeamProject": "P"}}
		}
		return 200, map[string]any{"id": 1, "text": "x", "createdBy": map[string]string{"displayName": "Me"}}
	})
	cm, err := c.AddComment(context.Background(), 5, "a<b\nc")
	if err != nil || cm.Author != "Me" {
		t.Fatal(cm, err)
	}
	r := (*reqs)[1]
	if r.Path != "/Col/P/_apis/wit/workItems/5/comments" || !strings.Contains(r.Query, "6.0-preview.3") || strings.TrimSpace(r.Body) != `{"text":"a&lt;b<br/>c"}` {
		t.Errorf("comment: %+v", r)
	}
}

func TestPullRequests(t *testing.T) {
	c, reqs := server(t, func(*http.Request, string) (int, any) {
		return 200, map[string]any{"value": []any{
			map[string]any{"pullRequestId": 1, "title": "A", "status": "active", "sourceRefName": "refs/heads/feature/x", "targetRefName": "refs/heads/main",
				"createdBy": map[string]string{"displayName": "Alice Smith"}, "repository": map[string]any{"name": "Repo"},
				"reviewers": []any{map[string]any{"vote": 10}}},
			map[string]any{"pullRequestId": 2, "title": "B", "createdBy": map[string]string{"displayName": "Bob"}, "repository": map[string]any{"name": "Other"}},
		}}
	})
	prs, err := c.ListPullRequests(context.Background(), domain.PullRequestQuery{Project: "P", Status: "active", Top: 10, Creator: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].SourceBranch != "feature/x" || prs[0].Approval != domain.ApprovalApproved {
		t.Fatalf("prs: %+v", prs)
	}
	if prs[0].URL != c.Collection()+"/P/_git/Repo/pullrequest/1" {
		t.Errorf("fallback url: %s", prs[0].URL)
	}
	if q := (*reqs)[0].Query; !strings.Contains(q, "searchCriteria.status=active") || !strings.Contains(q, "$top=10") {
		t.Errorf("query: %s", q)
	}
}

func TestCurrentUser(t *testing.T) {
	c, reqs := server(t, func(*http.Request, string) (int, any) {
		return 200, map[string]any{"authenticatedUser": map[string]string{"uniqueName": "DOMAIN\\me"}}
	})
	if u, err := c.ResolveIdentity(context.Background(), "@Me"); err != nil || u != "DOMAIN\\me" {
		t.Errorf("got %q %v", u, err)
	}
	c.UserIdentity = "cached"
	if u, _ := c.ResolveIdentity(context.Background(), "@me"); u != "cached" || len(*reqs) != 1 {
		t.Errorf("cache not used: %q", u)
	}
	if u, _ := c.ResolveIdentity(context.Background(), " bob "); u != "bob" {
		t.Errorf("passthrough: %q", u)
	}
}
