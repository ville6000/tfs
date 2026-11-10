package ado

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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

func TestBuildWIQLTeamScope(t *testing.T) {
	q := domain.WorkItemQuery{Project: "P", Scope: &domain.TeamScope{Field: "System.AreaPath", Values: []domain.TeamFieldValue{
		{Value: `P\Team A`, IncludeChildren: true}, {Value: `P\Shared`},
	}}}
	want := "SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = 'P' AND " +
		`([System.AreaPath] UNDER 'P\Team A' OR [System.AreaPath] = 'P\Shared') ` +
		"ORDER BY [System.ChangedDate] DESC"
	if got := BuildWIQL(q); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

func TestTeamScope(t *testing.T) {
	c, reqs := server(t, func(*http.Request, string) (int, any) {
		return 200, map[string]any{
			"field":  map[string]string{"referenceName": "System.AreaPath"},
			"values": []map[string]any{{"value": `P\Team A`, "includeChildren": true}},
		}
	})
	s, err := c.TeamScope(context.Background(), "P", "Team A")
	if err != nil || s.Field != "System.AreaPath" || len(s.Values) != 1 || !s.Values[0].IncludeChildren {
		t.Fatalf("scope %+v %v", s, err)
	}
	if p := (*reqs)[0].Path; p != "/Col/P/Team A/_apis/work/teamsettings/teamfieldvalues" {
		t.Errorf("path %s", p)
	}
	c, _ = server(t, func(*http.Request, string) (int, any) { return 404, nil })
	if _, err := c.TeamScope(context.Background(), "P", "Nope"); err == nil || !strings.Contains(err.Error(), `team "Nope" not found`) {
		t.Errorf("missing team: %v", err)
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

func TestPullRequestThreads(t *testing.T) {
	c, reqs := server(t, func(*http.Request, string) (int, any) {
		return 200, map[string]any{"count": 2, "value": []any{
			map[string]any{
				"id": 11, "status": "active", "isDeleted": false,
				"publishedDate": "2024-05-01T10:00:00Z", "lastUpdatedDate": "2024-05-02T10:00:00Z",
				"threadContext": map[string]any{"filePath": "/src/main.go",
					"rightFileStart": map[string]int{"line": 42, "offset": 1}, "rightFileEnd": map[string]int{"line": 42, "offset": 9}},
				"properties": map[string]any{},
				"comments": []any{
					map[string]any{"id": 1, "parentCommentId": 0, "author": map[string]string{"displayName": "Alice", "uniqueName": "DOMAIN\\alice"},
						"content": "Why?", "publishedDate": "2024-05-01T10:00:00Z", "lastUpdatedDate": "2024-05-01T11:00:00Z",
						"lastContentUpdatedDate": "2024-05-01T10:30:00Z", "commentType": "text", "isDeleted": false,
						"usersLiked": []any{map[string]string{"displayName": "Bob"}},
						"_links":     map[string]any{"self": map[string]string{"href": "https://x/comments/1"}}},
					map[string]any{"id": 2, "parentCommentId": 1, "content": "Because.", "commentType": "text"},
				},
			},
			map[string]any{"id": 12, "status": "closed", "threadContext": map[string]any{"filePath": "/old.go",
				"leftFileStart": map[string]int{"line": 7, "offset": 1}},
				"comments": []any{map[string]any{"id": 1, "content": "removed", "commentType": "system", "isDeleted": true}}},
		}}
	})
	threads, err := c.PullRequestThreads(context.Background(), "P", "R", 7)
	if err != nil {
		t.Fatal(err)
	}
	if r := (*reqs)[0]; r.Path != "/Col/P/_apis/git/repositories/R/pullRequests/7/threads" || r.Query != "api-version=6.0" {
		t.Errorf("request: %s ? %s", r.Path, r.Query)
	}
	if len(threads) != 2 {
		t.Fatalf("threads: %+v", threads)
	}
	th := threads[0]
	if th.ID != 11 || th.Status != "active" || th.FilePath != "/src/main.go" || th.Line != 42 || th.IsDeleted ||
		!th.Published.Equal(time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)) || !th.Updated.Equal(time.Date(2024, 5, 2, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("thread: %+v", th)
	}
	if len(th.Comments) != 2 {
		t.Fatalf("comments: %+v", th.Comments)
	}
	full := th.Comments[0]
	if full.ID != 1 || full.Author != "Alice" || full.Text != "Why?" || full.Type != "text" || full.ParentID != 0 ||
		!full.Created.Equal(time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)) ||
		!full.Updated.Equal(time.Date(2024, 5, 1, 11, 0, 0, 0, time.UTC)) ||
		!full.ContentUpdated.Equal(time.Date(2024, 5, 1, 10, 30, 0, 0, time.UTC)) ||
		len(full.LikedBy) != 1 || full.LikedBy[0] != "Bob" || full.URL != "https://x/comments/1" {
		t.Errorf("full comment: %+v", full)
	}
	if reply := th.Comments[1]; reply.ParentID != 1 || reply.Author != "unknown" || !reply.Created.IsZero() || reply.LikedBy != nil || reply.URL != "" {
		t.Errorf("minimal comment: %+v", reply)
	}
	if left := threads[1]; left.Line != 7 || left.Status != "closed" || !left.Comments[0].IsDeleted || left.Comments[0].Type != "system" {
		t.Errorf("left-side thread: %+v", left)
	}
	if _, err := c.PullRequestThreads(context.Background(), "P", "R", 0); err == nil {
		t.Error("expected error for id 0")
	}
}

func TestPullRequestThreadsEmpty(t *testing.T) {
	c, _ := server(t, func(*http.Request, string) (int, any) {
		return 200, map[string]any{"count": 0, "value": []any{}}
	})
	threads, err := c.PullRequestThreads(context.Background(), "P", "R", 7)
	if err != nil || threads == nil || len(threads) != 0 {
		t.Errorf("got %#v, %v; want non-nil empty slice", threads, err)
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

func TestCurrentUserServerShape(t *testing.T) {
	c, _ := server(t, func(*http.Request, string) (int, any) {
		return 200, map[string]any{"authenticatedUser": map[string]any{
			"id": "u1", "providerDisplayName": "Me Myself",
			"properties": map[string]any{"Account": map[string]string{"$type": "System.String", "$value": "DOMAIN\\me"}},
		}}
	})
	if u, err := c.CurrentUser(context.Background()); err != nil || u != "DOMAIN\\me" {
		t.Errorf("got %q %v", u, err)
	}
}

func TestTypeCategories(t *testing.T) {
	c, reqs := server(t, func(*http.Request, string) (int, any) {
		return 200, map[string]any{"value": []any{map[string]any{
			"referenceName": "Microsoft.RequirementCategory", "defaultWorkItemType": map[string]string{"name": "User Story"},
			"workItemTypes": []map[string]string{{"name": "User Story"}},
		}}}
	})
	cats, err := c.TypeCategories(context.Background(), "My Proj")
	if err != nil || len(cats) != 1 || cats[0].Default != "User Story" || cats[0].Types[0] != "User Story" {
		t.Fatalf("%+v %v", cats, err)
	}
	if p := (*reqs)[0].Path; p != "/Col/My Proj/_apis/wit/workitemtypecategories" {
		t.Errorf("path %s", p)
	}
}

func TestBacklogOrdering(t *testing.T) {
	c, reqs := server(t, func(r *http.Request, _ string) (int, any) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/backlogconfiguration"):
			return 200, map[string]any{"backlogFields": map[string]any{"typeFields": map[string]string{"Order": "Microsoft.VSTS.Common.BacklogPriority"}}}
		case strings.Contains(r.URL.Path, "/_apis/projects/"):
			return 200, map[string]any{"defaultTeam": map[string]string{"name": "P Team"}}
		case strings.HasSuffix(r.URL.Path, "/workitemsorder"):
			return 200, map[string]any{"count": 1, "value": []map[string]any{{"id": 3, "order": 1500.5}}}
		case strings.HasSuffix(r.URL.Path, "/wiql"):
			return 200, map[string]any{"workItems": []map[string]int{{"id": 3}}}
		}
		return 200, map[string]any{"value": []any{map[string]any{"id": 3, "fields": map[string]any{"Microsoft.VSTS.Common.BacklogPriority": 2.5e9}}}}
	})
	ctx := context.Background()
	field, err := c.BacklogOrderField(ctx, "P", "")
	if err != nil || field != "Microsoft.VSTS.Common.BacklogPriority" {
		t.Fatalf("field %q %v", field, err)
	}
	if p := (*reqs)[0].Path; p != "/Col/P/_apis/work/backlogconfiguration" {
		t.Errorf("config path %s", p)
	}
	team, err := c.DefaultTeam(ctx, "P")
	if err != nil || team != "P Team" {
		t.Fatalf("team %q %v", team, err)
	}
	items, err := c.ListWorkItems(ctx, domain.WorkItemQuery{Project: "P", Top: 1, OrderField: field})
	if err != nil || items[0].Order != 2.5e9 {
		t.Fatalf("order %+v %v", items, err)
	}
	if q := (*reqs)[3].Query; !strings.Contains(q, "Microsoft.VSTS.Common.BacklogPriority") {
		t.Errorf("batch should request the order field: %s", q)
	}
	orders, err := c.ReorderWorkItems(ctx, "P", "P Team", domain.ReorderOp{IDs: []int{3}, ParentID: 1, PreviousID: 2, NextID: 0})
	if err != nil || orders[3] != 1500.5 {
		t.Fatalf("reorder %v %v", orders, err)
	}
	r := (*reqs)[4]
	if r.Method != "PATCH" || r.Path != "/Col/P/P Team/_apis/work/workitemsorder" || !strings.Contains(r.Query, "6.0-preview.1") ||
		strings.TrimSpace(r.Body) != `{"ids":[3],"parentId":1,"previousId":2,"nextId":0}` {
		t.Errorf("reorder request: %+v", r)
	}
}

func TestRedirectOffOriginKeepsToken(t *testing.T) {
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization")
	}))
	t.Cleanup(other.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL+"/Col", "6.0", "secret")
	if _, err := c.CurrentUser(context.Background()); err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Errorf("expected the redirect to be refused, got %v", err)
	}
	if leaked != "" {
		t.Errorf("token sent to another server: %q", leaked)
	}
}

func TestSameOriginDefaultPort(t *testing.T) {
	check := sameOrigin("https://ADO.example.com/Col")
	for target, ok := range map[string]bool{
		"https://ado.example.com:443/Col/x": true,
		"https://ado.example.com/Col/x":     true,
		"https://ado.example.com:8443/x":    false,
		"http://ado.example.com/x":          false,
		"https://evil.example.com/x":        false,
	} {
		u, _ := url.Parse(target)
		if err := check(&http.Request{URL: u}, nil); (err == nil) != ok {
			t.Errorf("%s: got %v, want allowed=%v", target, err, ok)
		}
	}
}
