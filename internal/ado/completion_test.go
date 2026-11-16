package ado

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/ville6000/tfs/internal/domain"
)

// samplePR is an open pull request as read from the server, in a project whose name needs
// escaping.
func samplePR() domain.PullRequest {
	return domain.PullRequest{ID: 7, Title: "Fix login", Project: "My P", RepositoryID: "repo-1", Status: "active",
		SourceBranch: "feature/7-fix", TargetBranch: "main"}
}

// canonical re-encodes a JSON body with sorted keys.
func canonical(t *testing.T, body string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func meHandler(next func(r *http.Request, body string) (int, any)) func(r *http.Request, body string) (int, any) {
	return func(r *http.Request, body string) (int, any) {
		if strings.HasSuffix(r.URL.Path, "/_apis/connectionData") {
			return 200, map[string]any{"authenticatedUser": map[string]string{"id": "me-id", "providerDisplayName": "Me"}}
		}
		return next(r, body)
	}
}

func TestVote(t *testing.T) {
	status := 200
	c, reqs := server(t, meHandler(func(*http.Request, string) (int, any) { return status, map[string]any{} }))
	pr := samplePR()
	pr.Reviewers = []domain.Reviewer{{ID: "ME-ID", IsRequired: true}, {ID: "other"}}
	if err := c.Vote(context.Background(), pr, domain.VoteApprove); err != nil {
		t.Fatal(err)
	}
	r := (*reqs)[len(*reqs)-1]
	if r.Method != http.MethodPut || r.Path != "/Col/My P/_apis/git/repositories/repo-1/pullRequests/7/reviewers/me-id" {
		t.Errorf("request: %+v", r)
	}
	// A required reviewer stays required.
	if got := canonical(t, r.Body); got != `{"isRequired":true,"vote":10}` {
		t.Errorf("body %s", got)
	}

	pr.Reviewers = nil
	if err := c.Vote(context.Background(), pr, domain.VoteApprove); err != nil {
		t.Fatal(err)
	}
	if got := canonical(t, (*reqs)[len(*reqs)-1].Body); got != `{"vote":10}` {
		t.Errorf("not yet a reviewer: body %s", got)
	}

	status = http.StatusForbidden
	if err := c.Vote(context.Background(), pr, domain.VoteApprove); err == nil || !strings.Contains(err.Error(), `Code "Read & write"`) {
		t.Errorf("forbidden: %v", err)
	}

	n := len(*reqs)
	pr.Status = "completed"
	if err := c.Vote(context.Background(), pr, domain.VoteApprove); err == nil || len(*reqs) != n {
		t.Errorf("a completed pull request is not voted on: %v, %d requests", err, len(*reqs)-n)
	}
}

func TestSetAndCancelAutoComplete(t *testing.T) {
	var reply map[string]any
	c, reqs := server(t, meHandler(func(*http.Request, string) (int, any) { return 200, reply }))
	reply = map[string]any{"pullRequestId": 7, "status": "active",
		"repository":        map[string]any{"id": "repo-1", "name": "web", "project": map[string]string{"name": "My P"}},
		"autoCompleteSetBy": map[string]string{"id": "me-id", "displayName": "Me"},
		"completionOptions": map[string]any{"mergeStrategy": "squash", "deleteSourceBranch": true, "mergeCommitMessage": "msg"}}
	pr, err := c.SetAutoComplete(context.Background(), samplePR(), domain.CompletionOptions{
		MergeStrategy: domain.MergeSquash, DeleteSourceBranch: true, MergeCommitMessage: "msg", IgnoreConfigIDs: []int{3},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := (*reqs)[len(*reqs)-1]
	if r.Method != http.MethodPatch || r.Path != "/Col/My P/_apis/git/repositories/repo-1/pullRequests/7" || r.ContentType != "application/json" {
		t.Errorf("request: %+v", r)
	}
	want := `{"autoCompleteSetBy":{"id":"me-id"},"completionOptions":{"autoCompleteIgnoreConfigIds":[3],"deleteSourceBranch":true,` +
		`"mergeCommitMessage":"msg","mergeStrategy":"squash","squashMerge":true,"transitionWorkItems":false}}`
	if got := canonical(t, r.Body); got != want {
		t.Errorf("body:\n got %s\nwant %s", got, want)
	}
	if !pr.HasAutoComplete() || pr.AutoCompleteSetBy != "Me" || pr.RepositoryID != "repo-1" ||
		pr.CompletionOptions.MergeStrategy != domain.MergeSquash || !pr.CompletionOptions.DeleteSourceBranch {
		t.Errorf("updated pr: %+v", pr)
	}

	// squashMerge is left out unless the strategy is a squash.
	if _, err := c.SetAutoComplete(context.Background(), samplePR(), domain.CompletionOptions{MergeStrategy: domain.MergeRebase}); err != nil {
		t.Fatal(err)
	}
	if got := canonical(t, (*reqs)[len(*reqs)-1].Body); !strings.Contains(got, `"completionOptions":{"deleteSourceBranch":false,"mergeStrategy":"rebase","transitionWorkItems":false}`) {
		t.Errorf("rebase body %s", got)
	}

	// The server may answer a cancel with the empty identity rather than none.
	reply = map[string]any{"pullRequestId": 7, "status": "active",
		"autoCompleteSetBy": map[string]string{"id": noIdentity}}
	pr, err = c.CancelAutoComplete(context.Background(), samplePR())
	if err != nil {
		t.Fatal(err)
	}
	if got := canonical(t, (*reqs)[len(*reqs)-1].Body); got != `{"autoCompleteSetBy":{"id":"`+noIdentity+`"}}` {
		t.Errorf("cancel body %s", got)
	}
	if pr.HasAutoComplete() {
		t.Errorf("auto-complete still on: %+v", pr)
	}

	noRepo := samplePR()
	noRepo.RepositoryID = ""
	if _, err := c.CancelAutoComplete(context.Background(), noRepo); err == nil {
		t.Error("a pull request without a repository id cannot be changed")
	}
}

func TestCompletionOptionsFromServer(t *testing.T) {
	for raw, want := range map[string]domain.CompletionOptions{
		`{"mergeStrategy":4,"transitionWorkItems":true}`: {MergeStrategy: domain.MergeRebaseMerge, CompleteWorkItems: true},
		`{"squashMerge":true}`:                           {MergeStrategy: domain.MergeSquash},
		`{"mergeStrategy":"noFastForward"}`:              {MergeStrategy: domain.MergeNoFastForward},
		`{"mergeStrategy":"somethingNew"}`:               {},
	} {
		var o completionOptions
		if err := json.Unmarshal([]byte(raw), &o); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if got := o.toDomain(); got.MergeStrategy != want.MergeStrategy || got.CompleteWorkItems != want.CompleteWorkItems {
			t.Errorf("%s: %+v, want %+v", raw, got, want)
		}
	}
}

func TestAllowedMergeStrategies(t *testing.T) {
	policy := func(enabled, deleted bool, typ string, settings map[string]any) map[string]any {
		return map[string]any{"isEnabled": enabled, "isDeleted": deleted, "type": map[string]string{"id": typ}, "settings": settings}
	}
	var policies []any
	c, reqs := server(t, func(*http.Request, string) (int, any) { return 200, map[string]any{"value": policies} })

	policies = []any{
		policy(true, false, mergePolicyType, map[string]any{"allowSquash": true, "allowRebase": true, "allowNoFastForward": false}),
		policy(true, false, mergePolicyType, map[string]any{"useSquashMerge": true}), // before the allow* settings
		policy(false, false, mergePolicyType, map[string]any{"allowNoFastForward": true}),
		policy(true, true, mergePolicyType, map[string]any{"allowNoFastForward": true}),
		policy(true, false, "some-other-policy", map[string]any{"allowNoFastForward": true}),
	}
	got, err := c.AllowedMergeStrategies(context.Background(), samplePR())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []domain.MergeStrategy{domain.MergeSquash}) {
		t.Errorf("allowed %v", got)
	}
	r := (*reqs)[0]
	q, _ := url.ParseQuery(r.Query)
	if r.Path != "/Col/My P/_apis/git/policy/configurations" || q.Get("repositoryId") != "repo-1" ||
		q.Get("refName") != "refs/heads/main" || q.Get("policyType") != mergePolicyType || q.Get("api-version") != "6.0-preview.1" {
		t.Errorf("request: %+v", r)
	}

	policies = []any{policy(true, false, mergePolicyType, map[string]any{"useSquashMerge": false})}
	if got, _ := c.AllowedMergeStrategies(context.Background(), samplePR()); !slices.Equal(got, []domain.MergeStrategy{domain.MergeNoFastForward}) {
		t.Errorf("an old policy without squash allows only a merge: %v", got)
	}
	policies = nil
	if got, _ := c.AllowedMergeStrategies(context.Background(), samplePR()); got != nil {
		t.Errorf("no policy: %v", got)
	}
}
