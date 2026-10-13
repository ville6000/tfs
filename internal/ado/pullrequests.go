package ado

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ville6000/tfs/internal/domain"
)

type pullRequestResponse struct {
	PullRequestID int       `json:"pullRequestId"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	Status        string    `json:"status"`
	IsDraft       bool      `json:"isDraft"`
	CreationDate  time.Time `json:"creationDate"`
	SourceRefName string    `json:"sourceRefName"`
	TargetRefName string    `json:"targetRefName"`
	CreatedBy     struct {
		DisplayName string `json:"displayName"`
	} `json:"createdBy"`
	Repository struct {
		Name    string `json:"name"`
		Project struct {
			Name string `json:"name"`
		} `json:"project"`
	} `json:"repository"`
	Reviewers []struct {
		DisplayName string `json:"displayName"`
		Vote        int    `json:"vote"`
		IsRequired  bool   `json:"isRequired"`
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
		reviewers[i] = domain.Reviewer{Name: rv.DisplayName, Vote: rv.Vote, IsRequired: rv.IsRequired}
	}
	pr := domain.PullRequest{
		ID:           r.PullRequestID,
		Title:        r.Title,
		Description:  r.Description,
		CreatedBy:    r.CreatedBy.DisplayName,
		Created:      r.CreationDate,
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
