package ado

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestDescriptorForms(t *testing.T) {
	var got []descriptor
	in := `["Microsoft.TeamFoundation.Identity;S-1-9-1", {"identityType": "Microsoft.TeamFoundation.Identity", "identifier": "S-1-9-2"}, null]`
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	want := []descriptor{"microsoft.teamfoundation.identity;s-1-9-1", "microsoft.teamfoundation.identity;s-1-9-2", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("descriptor %d: %q, want %q", i, got[i], want[i])
		}
	}
}

// pullRequestServer fakes the calls behind MyPullRequests: the user is "me", a member of the
// "Web Team" group (through another group, which expanded membership includes) and not of "Ops".
// identities answers the identities calls; nil serves the memberships above.
func pullRequestServer(t *testing.T, prs []map[string]any, identities func(q url.Values) (int, any)) (*Client, *[]recorded) {
	t.Helper()
	return server(t, func(r *http.Request, _ string) (int, any) {
		q := r.URL.Query()
		switch {
		case strings.HasSuffix(r.URL.Path, "/_apis/connectionData"):
			return 200, map[string]any{"authenticatedUser": map[string]string{"id": "ME-ID", "providerDisplayName": "Me"}}
		case strings.HasSuffix(r.URL.Path, "/P/_apis/git/pullrequests"):
			skip, _ := strconv.Atoi(q.Get("$skip"))
			top, _ := strconv.Atoi(q.Get("$top"))
			page := prs[min(skip, len(prs)):min(skip+top, len(prs))]
			return 200, map[string]any{"value": page}
		case strings.HasSuffix(r.URL.Path, "/_apis/identities"):
			if identities != nil {
				return identities(q)
			}
			if q.Get("queryMembership") == "expanded" {
				return 200, map[string]any{"value": []any{map[string]any{"id": "me-id", "memberOf": []string{
					"Microsoft.TeamFoundation.Identity;S-WEB", "Microsoft.TeamFoundation.Identity;S-ENG"}}}}
			}
			var out []any
			for _, id := range strings.Split(q.Get("identityIds"), ",") {
				switch id {
				case "g-web":
					out = append(out, map[string]any{"id": "G-WEB", "descriptor": "Microsoft.TeamFoundation.Identity;S-WEB"})
				case "g-ops":
					out = append(out, map[string]any{"id": "g-ops", "descriptor": map[string]string{
						"identityType": "Microsoft.TeamFoundation.Identity", "identifier": "S-OPS"}})
				}
			}
			return 200, map[string]any{"value": out}
		}
		return 404, map[string]string{"message": "unexpected " + r.URL.Path}
	})
}

func reviewer(id, name string, vote int) map[string]any {
	return map[string]any{"id": id, "displayName": name, "vote": vote}
}

func samplePullRequests() []map[string]any {
	pr := func(id int, author string, reviewers ...map[string]any) map[string]any {
		return map[string]any{"pullRequestId": id, "status": "active", "title": "PR " + strconv.Itoa(id),
			"createdBy": map[string]string{"id": author, "displayName": author}, "repository": map[string]string{"name": "web"},
			"reviewers": reviewers}
	}
	return []map[string]any{
		pr(5, "ME-ID", reviewer("g-web", "Web Team", 0)),
		pr(4, "bob", reviewer("g-web", "Web Team", 0), reviewer("carol", "Carol", 10)),
		pr(3, "bob", reviewer("g-ops", "Ops", 0)),
		pr(2, "bob", reviewer("me-id", "Me", 0)),
		pr(1, "bob", reviewer("g-web", "Web Team", 10)),
	}
}

func TestMyPullRequests(t *testing.T) {
	c, reqs := pullRequestServer(t, samplePullRequests(), nil)
	got, err := c.MyPullRequests(context.Background(), "P")
	if err != nil {
		t.Fatal(err)
	}
	if got.Note != "" {
		t.Errorf("note %q", got.Note)
	}
	if len(got.Created) != 1 || got.Created[0].ID != 5 {
		t.Errorf("created %+v", got.Created)
	}
	if len(got.ToReview) != 2 || got.ToReview[0].ID != 4 || got.ToReview[0].Via != "Web Team" ||
		got.ToReview[1].ID != 2 || got.ToReview[1].Via != "" {
		t.Errorf("to review %+v", got.ToReview)
	}

	// Memberships are read once; identities already looked up are not asked for again.
	var lookups []string
	for _, r := range *reqs {
		if strings.HasSuffix(r.Path, "/_apis/identities") {
			lookups = append(lookups, r.Query)
		}
	}
	if len(lookups) != 2 || !strings.Contains(lookups[0], "identityIds=ME-ID") || !strings.Contains(lookups[0], "queryMembership=expanded") {
		t.Fatalf("identity lookups %v", lookups)
	}
	if q, _ := url.ParseQuery(lookups[1]); q.Get("identityIds") != "g-web,carol,g-ops" {
		t.Errorf("reviewers looked up: %q", q.Get("identityIds"))
	}
	if _, err := c.MyPullRequests(context.Background(), "P"); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range *reqs {
		if strings.HasSuffix(r.Path, "/_apis/identities") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("a second load looked up identities again: %d lookups", n)
	}
}

func TestMyPullRequestsPages(t *testing.T) {
	var prs []map[string]any
	for i := range activePage + 3 {
		prs = append(prs, map[string]any{"pullRequestId": i + 1, "status": "active",
			"createdBy": map[string]string{"id": "bob"}, "reviewers": []any{reviewer("me-id", "Me", 0)}})
	}
	c, reqs := pullRequestServer(t, prs, nil)
	got, err := c.MyPullRequests(context.Background(), "P")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ToReview) != activePage+3 {
		t.Errorf("%d to review, want %d", len(got.ToReview), activePage+3)
	}
	var pages []string
	for _, r := range *reqs {
		if strings.HasSuffix(r.Path, "/pullrequests") {
			q, _ := url.ParseQuery(r.Query)
			pages = append(pages, q.Get("searchCriteria.status")+" "+q.Get("$skip"))
		}
	}
	if len(pages) != 2 || pages[0] != "active 0" || pages[1] != "active 500" {
		t.Errorf("pages %v", pages)
	}
}

func TestMyPullRequestsWithoutIdentityScope(t *testing.T) {
	c, _ := pullRequestServer(t, samplePullRequests(), func(url.Values) (int, any) {
		return 401, map[string]string{"message": "TF400813: not authorized"}
	})
	got, err := c.MyPullRequests(context.Background(), "P")
	if err != nil {
		t.Fatal(err)
	}
	// Direct requests still show; the note says what is missing and why.
	if len(got.ToReview) != 1 || got.ToReview[0].ID != 2 {
		t.Errorf("to review %+v", got.ToReview)
	}
	if !strings.Contains(got.Note, `Identity "Read" scope`) {
		t.Errorf("note %q", got.Note)
	}
}
