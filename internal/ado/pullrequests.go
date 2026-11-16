package ado

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
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
	MergeStatus   string      `json:"mergeStatus"`
	CreationDate  apiTime     `json:"creationDate"`
	SourceRefName string      `json:"sourceRefName"`
	TargetRefName string      `json:"targetRefName"`
	CreatedBy     identityRef `json:"createdBy"`
	Repository    struct {
		ID      string `json:"id"`
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
	AutoCompleteSetBy identityRef       `json:"autoCompleteSetBy"`
	CompletionOptions completionOptions `json:"completionOptions"`
}

// completionOptions are a pull request's completion options as the server has them.
type completionOptions struct {
	MergeStrategy       mergeStrategy `json:"mergeStrategy,omitempty"`
	SquashMerge         bool          `json:"squashMerge,omitempty"` // before mergeStrategy (API 5.1)
	DeleteSourceBranch  bool          `json:"deleteSourceBranch"`
	TransitionWorkItems bool          `json:"transitionWorkItems"`
	MergeCommitMessage  string        `json:"mergeCommitMessage,omitempty"`
	IgnoreConfigIDs     []int         `json:"autoCompleteIgnoreConfigIds,omitempty"`
}

// mergeStrategy is a merge strategy, which the server may send as its name or its number.
type mergeStrategy string

var mergeStrategyNumbers = map[string]domain.MergeStrategy{
	"1": domain.MergeNoFastForward, "2": domain.MergeSquash, "3": domain.MergeRebase, "4": domain.MergeRebaseMerge,
}

func (m *mergeStrategy) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		s = string(b)
	}
	if n, ok := mergeStrategyNumbers[s]; ok {
		s = string(n)
	}
	*m = ""
	if parsed, err := domain.ParseMergeStrategy(s); err == nil {
		*m = mergeStrategy(parsed)
	}
	return nil
}

func (o completionOptions) toDomain() domain.CompletionOptions {
	m := domain.MergeStrategy(o.MergeStrategy)
	if m == "" && o.SquashMerge {
		m = domain.MergeSquash
	}
	return domain.CompletionOptions{MergeStrategy: m, DeleteSourceBranch: o.DeleteSourceBranch,
		CompleteWorkItems: o.TransitionWorkItems, MergeCommitMessage: o.MergeCommitMessage, IgnoreConfigIDs: o.IgnoreConfigIDs}
}

func fromDomainCompletion(o domain.CompletionOptions) completionOptions {
	return completionOptions{
		MergeStrategy: mergeStrategy(o.MergeStrategy),
		// Servers before API 5.1 read only squashMerge. It is sent only when it agrees with
		// mergeStrategy, so newer servers see no conflict.
		SquashMerge:         o.MergeStrategy == domain.MergeSquash,
		DeleteSourceBranch:  o.DeleteSourceBranch,
		TransitionWorkItems: o.CompleteWorkItems,
		MergeCommitMessage:  o.MergeCommitMessage,
		IgnoreConfigIDs:     o.IgnoreConfigIDs,
	}
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
		RepositoryID: r.Repository.ID,
		Project:      project,
		Status:       r.Status,
		IsDraft:      r.IsDraft,
		MergeStatus:  r.MergeStatus,
		Reviewers:    reviewers,
		Approval:     domain.ComputeApproval(reviewers),
		URL:          r.Links.Web.Href,

		CompletionOptions: r.CompletionOptions.toDomain(),
	}
	if by := r.AutoCompleteSetBy; by.ID != noIdentity && (by.ID != "" || by.DisplayName != "") {
		pr.AutoCompleteSetBy = cmp.Or(by.DisplayName, by.UniqueName, "someone")
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

// pullRequestPath is the API path of the pull request in its repository, followed by sub.
func pullRequestPath(pr domain.PullRequest, sub string) (string, error) {
	if pr.RepositoryID == "" {
		return "", fmt.Errorf("the server did not say which repository pull request !%d is in", pr.ID)
	}
	prefix := ""
	if pr.Project != "" {
		prefix = "/" + seg(pr.Project)
	}
	return fmt.Sprintf("%s/_apis/git/repositories/%s/pullRequests/%d%s", prefix, seg(pr.RepositoryID), pr.ID, sub), nil
}

// writeScopeHint explains a refused change to a pull request.
func writeScopeHint(err error, action string, id int) error {
	if IsKind(err, ErrForbidden) {
		return fmt.Errorf(`not allowed to %s !%d (does the token have the Code "Read & write" scope?): %w`, action, id, err)
	}
	return err
}

// Vote records the authenticated user's vote on the pull request (domain.VoteApprove, …). The
// user becomes an optional reviewer when they are not one yet, as in the web UI.
func (c *Client) Vote(ctx context.Context, pr domain.PullRequest, vote int) error {
	if err := pr.CheckOpen(); err != nil {
		return err
	}
	who, err := c.whoAmI(ctx)
	if err != nil {
		return err
	}
	if who.ID == "" {
		return errors.New("server did not return an id for the authenticated user")
	}
	path, err := pullRequestPath(pr, "/reviewers/"+seg(who.ID))
	if err != nil {
		return err
	}
	body := struct {
		Vote       int  `json:"vote"`
		IsRequired bool `json:"isRequired,omitempty"`
	}{Vote: vote}
	// Say again that a required reviewer is required, so the vote never makes them optional.
	for _, r := range pr.Reviewers {
		if strings.EqualFold(r.ID, who.ID) && r.IsRequired {
			body.IsRequired = true
		}
	}
	err = c.do(ctx, http.MethodPut, c.url(path, nil), "application/json", body, nil)
	return writeScopeHint(err, "vote on", pr.ID)
}

// noIdentity clears autoCompleteSetBy, which turns auto-complete off.
const noIdentity = "00000000-0000-0000-0000-000000000000"

// SetAutoComplete makes the pull request complete with opts once its policies pass, on behalf
// of the authenticated user, and returns it as updated.
func (c *Client) SetAutoComplete(ctx context.Context, pr domain.PullRequest, opts domain.CompletionOptions) (domain.PullRequest, error) {
	who, err := c.whoAmI(ctx)
	if err != nil {
		return domain.PullRequest{}, err
	}
	if who.ID == "" {
		return domain.PullRequest{}, errors.New("server did not return an id for the authenticated user")
	}
	o := fromDomainCompletion(opts)
	return c.updatePullRequest(ctx, pr, "set auto-complete of", map[string]any{
		"autoCompleteSetBy": map[string]string{"id": who.ID},
		"completionOptions": o,
	})
}

// CancelAutoComplete turns auto-complete off and returns the pull request as updated.
func (c *Client) CancelAutoComplete(ctx context.Context, pr domain.PullRequest) (domain.PullRequest, error) {
	return c.updatePullRequest(ctx, pr, "cancel auto-complete of", map[string]any{
		"autoCompleteSetBy": map[string]string{"id": noIdentity},
	})
}

func (c *Client) updatePullRequest(ctx context.Context, pr domain.PullRequest, action string, body any) (domain.PullRequest, error) {
	if err := pr.CheckOpen(); err != nil {
		return domain.PullRequest{}, err
	}
	path, err := pullRequestPath(pr, "")
	if err != nil {
		return domain.PullRequest{}, err
	}
	var r pullRequestResponse
	if err := c.do(ctx, http.MethodPatch, c.url(path, nil), "application/json", body, &r); err != nil {
		return domain.PullRequest{}, writeScopeHint(err, action, pr.ID)
	}
	return c.mapPullRequest(r, pr.Project), nil
}

// mergePolicyType is the type of the "Limit merge types" branch policy.
const mergePolicyType = "fa4e907d-c16b-4a4c-9dfa-4916e5d171ab"

// mergePolicySettings are the settings of a "Limit merge types" policy. Servers before the
// allow* settings had a choice between a merge and a squash, in useSquashMerge.
type mergePolicySettings struct {
	AllowNoFastForward *bool `json:"allowNoFastForward"`
	AllowSquash        *bool `json:"allowSquash"`
	AllowRebase        *bool `json:"allowRebase"`
	AllowRebaseMerge   *bool `json:"allowRebaseMerge"`
	UseSquashMerge     bool  `json:"useSquashMerge"`
}

func (s mergePolicySettings) toDomain() domain.MergePolicy {
	flags := []*bool{s.AllowNoFastForward, s.AllowSquash, s.AllowRebase, s.AllowRebaseMerge}
	if !slices.ContainsFunc(flags, func(b *bool) bool { return b != nil }) {
		if s.UseSquashMerge {
			return domain.MergePolicy{Allowed: []domain.MergeStrategy{domain.MergeSquash}}
		}
		return domain.MergePolicy{Allowed: []domain.MergeStrategy{domain.MergeNoFastForward}}
	}
	var p domain.MergePolicy
	for i, b := range flags {
		if b != nil && *b {
			p.Allowed = append(p.Allowed, domain.MergeStrategies[i])
		}
	}
	return p
}

// AllowedMergeStrategies lists the merge strategies the policies of the pull request's target
// branch allow, in the web UI's order; none when no policy limits them.
func (c *Client) AllowedMergeStrategies(ctx context.Context, pr domain.PullRequest) ([]domain.MergeStrategy, error) {
	if pr.Project == "" || pr.RepositoryID == "" {
		return nil, fmt.Errorf("the server did not say which repository pull request !%d is in", pr.ID)
	}
	var payload struct {
		Value []struct {
			IsEnabled bool `json:"isEnabled"`
			IsDeleted bool `json:"isDeleted"`
			Type      struct {
				ID string `json:"id"`
			} `json:"type"`
			Settings mergePolicySettings `json:"settings"`
		} `json:"value"`
	}
	// This lists the policies that apply to the branch, however they are scoped; it is a preview
	// API in every server version.
	u := c.url("/"+seg(pr.Project)+"/_apis/git/policy/configurations", url.Values{
		"repositoryId": {pr.RepositoryID},
		"refName":      {"refs/heads/" + pr.TargetBranch},
		"policyType":   {mergePolicyType},
		"api-version":  {previewVersion(c.apiVersion)},
	})
	if err := c.get(ctx, u, &payload); err != nil {
		return nil, err
	}
	var policies []domain.MergePolicy
	for _, p := range payload.Value {
		if p.IsEnabled && !p.IsDeleted && strings.EqualFold(p.Type.ID, mergePolicyType) {
			policies = append(policies, p.Settings.toDomain())
		}
	}
	return domain.AllowedMergeStrategies(policies), nil
}

// previewVersion is the preview of API version v, e.g. 6.0-preview.1.
func previewVersion(v string) string {
	if strings.Contains(v, "-preview") {
		return v
	}
	return v + "-preview.1"
}
