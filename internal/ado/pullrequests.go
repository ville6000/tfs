package ado

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/ville6000/tfs/internal/domain"
)

type pullRequestResponse struct {
	PullRequestID int         `json:"pullRequestId"`
	Title         string      `json:"title"`
	Description   string      `json:"description"`
	Status        string      `json:"status"`
	IsDraft       bool        `json:"isDraft"`
	CreationDate  apiTime     `json:"creationDate"`
	SourceRefName string      `json:"sourceRefName"`
	TargetRefName string      `json:"targetRefName"`
	CreatedBy     identityRef `json:"createdBy"`
	Repository    struct {
		Name    string `json:"name"`
		Project struct {
			Name string `json:"name"`
		} `json:"project"`
	} `json:"repository"`
	Reviewers []struct {
		identityRef
		Vote        int  `json:"vote"`
		IsRequired  bool `json:"isRequired"`
		HasDeclined bool `json:"hasDeclined"`
	} `json:"reviewers"`
	Links struct {
		Web struct {
			Href string `json:"href"`
		} `json:"web"`
	} `json:"_links"`
}

func (c *Client) mapPullRequest(r pullRequestResponse, project string) domain.PullRequest {
	if r.Repository.Project.Name != "" {
		project = r.Repository.Project.Name
	}
	reviewers := make([]domain.Reviewer, len(r.Reviewers))
	for i, rv := range r.Reviewers {
		reviewers[i] = domain.Reviewer{Name: rv.DisplayName, Vote: rv.Vote, IsRequired: rv.IsRequired,
			ID: rv.ID, IsGroup: rv.IsContainer, HasDeclined: rv.HasDeclined}
	}
	pr := domain.PullRequest{
		ID:           r.PullRequestID,
		Title:        r.Title,
		Description:  r.Description,
		CreatedBy:    r.CreatedBy.DisplayName,
		CreatedByID:  r.CreatedBy.ID,
		Created:      r.CreationDate.Time,
		SourceBranch: domain.TrimRef(r.SourceRefName),
		TargetBranch: domain.TrimRef(r.TargetRefName),
		Repository:   r.Repository.Name,
		Project:      project,
		Status:       r.Status,
		IsDraft:      r.IsDraft,
		Reviewers:    reviewers,
		Approval:     domain.ComputeApproval(reviewers),
		URL:          r.Links.Web.Href,
	}
	if pr.CreatedBy == "" {
		pr.CreatedBy = "unknown"
	}
	if pr.URL == "" {
		pr.URL = fmt.Sprintf("%s/%s/_git/%s/pullrequest/%d", c.collection, seg(project), seg(pr.Repository), pr.ID)
	}
	return pr
}

// ListPullRequests lists pull requests in a project, applying creator/repository filters.
func (c *Client) ListPullRequests(ctx context.Context, q domain.PullRequestQuery) ([]domain.PullRequest, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	var payload struct {
		Value []pullRequestResponse `json:"value"`
	}
	u := c.url("/"+seg(q.Project)+"/_apis/git/pullrequests", url.Values{
		"searchCriteria.status": {strings.ToLower(q.Status)},
		"$top":                  {strconv.Itoa(q.Top)},
	})
	if err := c.get(ctx, u, &payload); err != nil {
		return nil, err
	}
	prs := make([]domain.PullRequest, len(payload.Value))
	for i, r := range payload.Value {
		prs[i] = c.mapPullRequest(r, q.Project)
	}
	return domain.FilterPullRequests(prs, q), nil
}

// GetPullRequest fetches a pull request by id (collection-wide).
func (c *Client) GetPullRequest(ctx context.Context, id int) (domain.PullRequest, error) {
	if id <= 0 {
		return domain.PullRequest{}, fmt.Errorf("pull request id must be positive")
	}
	var r pullRequestResponse
	if err := c.get(ctx, c.url(fmt.Sprintf("/_apis/git/pullrequests/%d", id), nil), &r); err != nil {
		if IsKind(err, ErrNotFound) {
			return domain.PullRequest{}, fmt.Errorf("pull request !%d not found", id)
		}
		return domain.PullRequest{}, err
	}
	return c.mapPullRequest(r, ""), nil
}

// Repository is a git repository on the server.
type Repository struct {
	ID            string
	Name          string
	DefaultBranch string
}

// GetRepository looks up a repository by name within a project.
func (c *Client) GetRepository(ctx context.Context, project, name string) (Repository, error) {
	var r struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		DefaultBranch string `json:"defaultBranch"`
	}
	if err := c.get(ctx, c.url("/"+seg(project)+"/_apis/git/repositories/"+seg(name), nil), &r); err != nil {
		return Repository{}, err
	}
	if r.ID == "" {
		return Repository{}, fmt.Errorf("server did not return a repository named %q", name)
	}
	if r.Name == "" {
		r.Name = name
	}
	return Repository{ID: r.ID, Name: r.Name, DefaultBranch: domain.TrimRef(r.DefaultBranch)}, nil
}

// CreatePullRequest creates a pull request, linking the draft's work items.
func (c *Client) CreatePullRequest(ctx context.Context, d domain.PullRequestDraft) (domain.PullRequest, error) {
	if err := d.Validate(); err != nil {
		return domain.PullRequest{}, err
	}
	type workItemRef struct {
		ID string `json:"id"`
	}
	body := struct {
		SourceRefName string        `json:"sourceRefName"`
		TargetRefName string        `json:"targetRefName"`
		Title         string        `json:"title"`
		Description   string        `json:"description,omitempty"`
		IsDraft       bool          `json:"isDraft,omitempty"`
		WorkItemRefs  []workItemRef `json:"workItemRefs,omitempty"`
	}{
		SourceRefName: "refs/heads/" + domain.TrimRef(strings.TrimSpace(d.SourceBranch)),
		TargetRefName: "refs/heads/" + domain.TrimRef(strings.TrimSpace(d.TargetBranch)),
		Title:         strings.TrimSpace(d.Title),
		Description:   d.Description,
		IsDraft:       d.IsDraft,
	}
	for _, id := range d.WorkItemIDs {
		body.WorkItemRefs = append(body.WorkItemRefs, workItemRef{ID: strconv.Itoa(id)})
	}
	var r pullRequestResponse
	u := c.url("/"+seg(d.Project)+"/_apis/git/repositories/"+seg(d.RepositoryID)+"/pullrequests", nil)
	if err := c.postJSON(ctx, u, body, &r); err != nil {
		return domain.PullRequest{}, err
	}
	return c.mapPullRequest(r, d.Project), nil
}

// Active pull requests are read in pages of activePage, up to maxActive in all.
const (
	activePage = 500
	maxActive  = 2000
)

// activePullRequests lists every active pull request of a project, up to maxActive.
func (c *Client) activePullRequests(ctx context.Context, project string) ([]domain.PullRequest, error) {
	var prs []domain.PullRequest
	for skip := 0; skip < maxActive; skip += activePage {
		var payload struct {
			Value []pullRequestResponse `json:"value"`
		}
		u := c.url("/"+seg(project)+"/_apis/git/pullrequests", url.Values{
			"searchCriteria.status": {"active"},
			"$top":                  {strconv.Itoa(activePage)},
			"$skip":                 {strconv.Itoa(skip)},
		})
		if err := c.get(ctx, u, &payload); err != nil {
			return nil, err
		}
		for _, r := range payload.Value {
			prs = append(prs, c.mapPullRequest(r, project))
		}
		if len(payload.Value) < activePage {
			break
		}
	}
	return prs, nil
}

// MyPullRequests lists the project's active pull requests that wait for the authenticated
// user's review, as a reviewer or through a group they belong to, and those they created. When
// group memberships cannot be read, reviews requested from groups are left out and the result's
// Note says so.
func (c *Client) MyPullRequests(ctx context.Context, project string) (domain.MyPullRequests, error) {
	who, err := c.whoAmI(ctx)
	if err != nil {
		return domain.MyPullRequests{}, err
	}
	if who.ID == "" {
		return domain.MyPullRequests{}, errors.New("server did not return an id for the authenticated user")
	}
	prs, err := c.activePullRequests(ctx, project)
	if err != nil {
		return domain.MyPullRequests{}, err
	}
	var reviewers []string
	for _, pr := range prs {
		if strings.EqualFold(pr.CreatedByID, who.ID) {
			continue
		}
		for _, r := range pr.Reviewers {
			// Groups are not always marked as such, so every other reviewer is looked up.
			if r.ID != "" && !strings.EqualFold(r.ID, who.ID) {
				reviewers = append(reviewers, r.ID)
			}
		}
	}
	var groups map[string]bool
	var note string
	if len(reviewers) > 0 {
		if groups, err = c.MemberGroups(ctx, reviewers); err != nil {
			note = "reviews requested from your groups are missing: could not read your group memberships: " + err.Error()
			if IsKind(err, ErrUnauthorized) || IsKind(err, ErrForbidden) {
				note = `reviews requested from your groups are missing: reading your group memberships needs a token with the Identity "Read" scope`
			}
		}
	}
	out := domain.SplitMyPullRequests(prs, who.ID, groups)
	out.Note = note
	return out, nil
}
