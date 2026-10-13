package ado

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ville6000/tfs/internal/domain"
)

type identityRef struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	UniqueName  string `json:"uniqueName"`
	IsContainer bool   `json:"isContainer"`
}

type link struct {
	Web struct {
		Href string `json:"href"`
	} `json:"web"`
}

type approvalResponse struct {
	ID           int         `json:"id"`
	ApprovalType string      `json:"approvalType"`
	Status       string      `json:"status"`
	Approver     identityRef `json:"approver"`
	CreatedOn    time.Time   `json:"createdOn"`
	Release      struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"release"`
	ReleaseDefinition struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"releaseDefinition"`
	ReleaseEnvironment struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"releaseEnvironment"`
}

func (a approvalResponse) toDomain() domain.ReleaseApproval {
	return domain.ReleaseApproval{
		ID: a.ID, Type: a.ApprovalType, Status: a.Status, Approver: a.Approver.DisplayName,
		ReleaseID: a.Release.ID, Release: a.Release.Name,
		PipelineID: a.ReleaseDefinition.ID, Pipeline: a.ReleaseDefinition.Name,
		StageID: a.ReleaseEnvironment.ID, Stage: a.ReleaseEnvironment.Name, Created: a.CreatedOn,
	}
}

type releaseResponse struct {
	ID          int         `json:"id"`
	Name        string      `json:"name"`
	Status      string      `json:"status"`
	Description string      `json:"description"`
	CreatedOn   time.Time   `json:"createdOn"`
	CreatedBy   identityRef `json:"createdBy"`
	Links       link        `json:"_links"`
	Definition  struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"releaseDefinition"`
	Environments []struct {
		ID                      int                `json:"id"`
		DefinitionEnvironmentID int                `json:"definitionEnvironmentId"`
		Name                    string             `json:"name"`
		Rank                    int                `json:"rank"`
		Status                  string             `json:"status"`
		ModifiedOn              time.Time          `json:"modifiedOn"`
		PreDeployApprovals      []approvalResponse `json:"preDeployApprovals"`
		PostDeployApprovals     []approvalResponse `json:"postDeployApprovals"`
	} `json:"environments"`
}

func (c *Client) mapRelease(project string, r releaseResponse) domain.Release {
	rel := domain.Release{
		ID: r.ID, Name: r.Name, PipelineID: r.Definition.ID, Pipeline: r.Definition.Name,
		Status: r.Status, Description: r.Description, CreatedBy: r.CreatedBy.DisplayName, Created: r.CreatedOn,
		URL: r.Links.Web.Href,
	}
	if rel.URL == "" {
		rel.URL = c.ReleaseWebURL(project, r.ID)
	}
	for _, e := range r.Environments {
		s := domain.ReleaseStage{
			ID: e.ID, PipelineStageID: e.DefinitionEnvironmentID, Name: e.Name, Rank: e.Rank,
			Status: e.Status, Modified: e.ModifiedOn,
		}
		for _, a := range slices.Concat(e.PreDeployApprovals, e.PostDeployApprovals) {
			if strings.EqualFold(a.Status, "pending") {
				d := a.toDomain()
				d.ReleaseID, d.Release, d.StageID, d.Stage = r.ID, r.Name, e.ID, e.Name
				d.PipelineID, d.Pipeline = r.Definition.ID, r.Definition.Name
				s.PendingApprovals = append(s.PendingApprovals, d)
			}
		}
		rel.Stages = append(rel.Stages, s)
	}
	slices.SortStableFunc(rel.Stages, func(a, b domain.ReleaseStage) int { return a.Rank - b.Rank })
	return rel
}

// ReleaseWebURL is the web URL of a release.
func (c *Client) ReleaseWebURL(project string, id int) string {
	return fmt.Sprintf("%s/%s/_releaseProgress?releaseId=%d&_a=release-pipeline-progress", c.collection, seg(project), id)
}

// ListReleasePipelines lists the project's classic release pipelines with their stages.
func (c *Client) ListReleasePipelines(ctx context.Context, project string) ([]domain.ReleasePipeline, error) {
	var payload struct {
		Value []struct {
			ID           int    `json:"id"`
			Name         string `json:"name"`
			Path         string `json:"path"`
			Links        link   `json:"_links"`
			Environments []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
				Rank int    `json:"rank"`
			} `json:"environments"`
		} `json:"value"`
	}
	u := c.url("/"+seg(project)+"/_apis/release/definitions", url.Values{"$expand": {"environments"}, "$top": {"500"}})
	if err := c.get(ctx, u, &payload); err != nil {
		return nil, err
	}
	out := make([]domain.ReleasePipeline, 0, len(payload.Value))
	for _, d := range payload.Value {
		p := domain.ReleasePipeline{ID: d.ID, Name: d.Name, Path: d.Path, URL: d.Links.Web.Href}
		if p.URL == "" {
			p.URL = fmt.Sprintf("%s/%s/_release?definitionId=%d", c.collection, seg(project), d.ID)
		}
		for _, e := range d.Environments {
			p.Stages = append(p.Stages, domain.PipelineStage{ID: e.ID, Name: e.Name, Rank: e.Rank})
		}
		slices.SortStableFunc(p.Stages, func(a, b domain.PipelineStage) int { return a.Rank - b.Rank })
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b domain.ReleasePipeline) int {
		return strings.Compare(strings.ToLower(a.Path+`\`+a.Name), strings.ToLower(b.Path+`\`+b.Name))
	})
	return out, nil
}

// ListReleases lists a pipeline's releases, newest first.
func (c *Client) ListReleases(ctx context.Context, project string, pipelineID, top int) ([]domain.Release, error) {
	var payload struct {
		Value []releaseResponse `json:"value"`
	}
	u := c.url("/"+seg(project)+"/_apis/release/releases", url.Values{
		"definitionId": {strconv.Itoa(pipelineID)},
		"$top":         {strconv.Itoa(top)},
		"$expand":      {"environments"},
	})
	if err := c.get(ctx, u, &payload); err != nil {
		return nil, err
	}
	out := make([]domain.Release, len(payload.Value))
	for i, r := range payload.Value {
		out[i] = c.mapRelease(project, r)
	}
	return out, nil
}

// GetRelease fetches one release with its stages and approvals.
func (c *Client) GetRelease(ctx context.Context, project string, id int) (domain.Release, error) {
	var r releaseResponse
	if err := c.get(ctx, c.url(fmt.Sprintf("/%s/_apis/release/releases/%d", seg(project), id), nil), &r); err != nil {
		if IsKind(err, ErrNotFound) {
			return domain.Release{}, fmt.Errorf("release %d not found in %s", id, project)
		}
		return domain.Release{}, err
	}
	return c.mapRelease(project, r), nil
}

// me is the authenticated user, as the release service identifies them.
type me struct {
	ID, DisplayName, UniqueName string
}

func (c *Client) whoAmI(ctx context.Context) (me, error) {
	c.meOnce.Do(func() {
		var payload struct {
			AuthenticatedUser struct {
				ID                  string `json:"id"`
				ProviderDisplayName string `json:"providerDisplayName"`
				CustomDisplayName   string `json:"customDisplayName"`
				DisplayName         string `json:"displayName"`
				UniqueName          string `json:"uniqueName"`
			} `json:"authenticatedUser"`
		}
		if c.meErr = c.get(ctx, c.collection+"/_apis/connectionData", &payload); c.meErr != nil {
			return
		}
		u := payload.AuthenticatedUser
		name := u.CustomDisplayName
		for _, v := range []string{u.ProviderDisplayName, u.DisplayName} {
			if name == "" {
				name = v
			}
		}
		c.me = me{ID: u.ID, DisplayName: name, UniqueName: u.UniqueName}
	})
	return c.me, c.meErr
}

// MyPendingApprovals lists release approvals waiting for the authenticated user, including
// approvals assigned to groups they belong to.
func (c *Client) MyPendingApprovals(ctx context.Context, project string) ([]domain.ReleaseApproval, error) {
	who, err := c.whoAmI(ctx)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Value []approvalResponse `json:"value"`
	}
	u := c.url("/"+seg(project)+"/_apis/release/approvals", url.Values{
		"assignedToFilter":        {who.DisplayName},
		"statusFilter":            {"pending"},
		"includeMyGroupApprovals": {"true"},
		"top":                     {"100"},
	})
	if err := c.get(ctx, u, &payload); err != nil {
		return nil, err
	}
	out := make([]domain.ReleaseApproval, len(payload.Value))
	for i, a := range payload.Value {
		out[i] = a.toDomain()
	}
	return out, nil
}

// PickApproval chooses which pending approval of a stage the user acts on: their own when it
// can be identified, otherwise the first one (often a group approval); the server checks that
// the user may approve it.
func (c *Client) PickApproval(ctx context.Context, project string, rel domain.Release, stage domain.ReleaseStage) (domain.ReleaseApproval, error) {
	if len(stage.PendingApprovals) == 0 {
		return domain.ReleaseApproval{}, fmt.Errorf("%s has no pending approval on %s", rel.Name, stage.Name)
	}
	if mine, err := c.MyPendingApprovals(ctx, project); err == nil {
		for _, a := range mine {
			for _, p := range stage.PendingApprovals {
				if a.ID == p.ID {
					return p, nil
				}
			}
		}
	}
	return stage.PendingApprovals[0], nil
}

// SetApproval approves or rejects a release approval.
func (c *Client) SetApproval(ctx context.Context, project string, approvalID int, approve bool, comment string) error {
	status := "rejected"
	if approve {
		status = "approved"
	}
	u := c.url(fmt.Sprintf("/%s/_apis/release/approvals/%d", seg(project), approvalID), nil)
	err := c.do(ctx, http.MethodPatch, u, "application/json", map[string]string{"status": status, "comments": comment}, nil)
	if IsKind(err, ErrForbidden) {
		return fmt.Errorf("not allowed to %s this stage (are you one of its approvers, and does TFS_PAT have the Release manage scope?): %w", map[bool]string{true: "approve", false: "reject"}[approve], err)
	}
	return err
}

// ReleaseOverview builds the dashboard for the pipelines matching patterns: each pipeline's newest
// release and, per stage, the newest release that reached it (from the last perPipeline releases).
func (c *Client) ReleaseOverview(ctx context.Context, project string, patterns []string, perPipeline int) ([]domain.PipelineOverview, error) {
	all, err := c.ListReleasePipelines(ctx, project)
	if err != nil {
		return nil, err
	}
	var pipes []domain.ReleasePipeline
	for _, p := range all {
		if domain.MatchPipeline(patterns, p) {
			pipes = append(pipes, p)
		}
	}
	out := make([]domain.PipelineOverview, len(pipes))
	errs := make([]error, len(pipes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, p := range pipes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rels, err := c.ListReleases(ctx, project, p.ID, perPipeline)
			if err != nil {
				errs[i] = fmt.Errorf("%s: %w", p.Name, err)
				return
			}
			out[i] = domain.PipelineOverview{Pipeline: p, Deployments: domain.LatestPerStage(p, rels)}
			if len(rels) > 0 {
				out[i].Latest = &rels[0]
			}
		}()
	}
	wg.Wait()
	return out, errors.Join(errs...)
}
