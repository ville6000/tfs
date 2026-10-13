package ado

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ville6000/tfs/internal/domain"
)

// maxLogBytes caps how much of a task log is read; the end of a log is what matters, so longer
// logs keep their last maxLogBytes.
const maxLogBytes = 8 << 20

type buildResponse struct {
	ID          int    `json:"id"`
	BuildNumber string `json:"buildNumber"`
	Status      string `json:"status"`
	Result      string `json:"result"`
	Reason      string `json:"reason"`
	Definition  struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"definition"`
	SourceBranch  string      `json:"sourceBranch"`
	SourceVersion string      `json:"sourceVersion"`
	RequestedFor  identityRef `json:"requestedFor"`
	Repository    struct {
		Name string `json:"name"`
	} `json:"repository"`
	QueueTime  apiTime `json:"queueTime"`
	StartTime  apiTime `json:"startTime"`
	FinishTime apiTime `json:"finishTime"`
	Links      link    `json:"_links"`
}

func (c *Client) mapBuild(project string, b buildResponse) domain.Build {
	out := domain.Build{
		ID: b.ID, Number: b.BuildNumber, PipelineID: b.Definition.ID, Pipeline: b.Definition.Name,
		Status: b.Status, Result: b.Result, Reason: b.Reason, Branch: domain.TrimRef(b.SourceBranch),
		Commit: b.SourceVersion, Repository: b.Repository.Name, RequestedFor: b.RequestedFor.DisplayName,
		Queued: b.QueueTime.Time, Started: b.StartTime.Time, Finished: b.FinishTime.Time, URL: b.Links.Web.Href,
	}
	if out.URL == "" {
		out.URL = c.BuildWebURL(project, b.ID)
	}
	return out
}

// BuildWebURL is the web URL of a build's results page.
func (c *Client) BuildWebURL(project string, id int) string {
	return fmt.Sprintf("%s/%s/_build/results?buildId=%d", c.collection, seg(project), id)
}

// ListBuildPipelines lists the project's build pipelines.
func (c *Client) ListBuildPipelines(ctx context.Context, project string) ([]domain.BuildPipeline, error) {
	var payload struct {
		Value []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"value"`
	}
	if err := c.get(ctx, c.url("/"+seg(project)+"/_apis/build/definitions", url.Values{"$top": {"1000"}}), &payload); err != nil {
		return nil, err
	}
	out := make([]domain.BuildPipeline, len(payload.Value))
	for i, d := range payload.Value {
		out[i] = domain.BuildPipeline{ID: d.ID, Name: d.Name, Path: d.Path}
	}
	return out, nil
}

// ListBuilds lists builds, most recently queued first.
func (c *Client) ListBuilds(ctx context.Context, q domain.BuildQuery) ([]domain.Build, error) {
	v := url.Values{"queryOrder": {"queueTimeDescending"}, "$top": {strconv.Itoa(max(1, q.Top))}}
	if len(q.PipelineIDs) > 0 {
		ids := make([]string, len(q.PipelineIDs))
		for i, id := range q.PipelineIDs {
			ids[i] = strconv.Itoa(id)
		}
		v.Set("definitions", strings.Join(ids, ","))
	}
	if b := strings.TrimSpace(q.Branch); b != "" {
		if !strings.HasPrefix(b, "refs/") {
			b = "refs/heads/" + b
		}
		v.Set("branchName", b)
	}
	if q.RequestedFor != "" {
		v.Set("requestedFor", q.RequestedFor)
	}
	if q.Result != "" {
		v.Set("resultFilter", q.Result)
		v.Set("statusFilter", "completed")
	}
	var payload struct {
		Value []buildResponse `json:"value"`
	}
	if err := c.get(ctx, c.url("/"+seg(q.Project)+"/_apis/build/builds", v), &payload); err != nil {
		return nil, err
	}
	out := make([]domain.Build, len(payload.Value))
	for i, b := range payload.Value {
		out[i] = c.mapBuild(q.Project, b)
	}
	return out, nil
}

// GetBuild fetches one build.
func (c *Client) GetBuild(ctx context.Context, project string, id int) (domain.Build, error) {
	var b buildResponse
	if err := c.get(ctx, c.url(fmt.Sprintf("/%s/_apis/build/builds/%d", seg(project), id), nil), &b); err != nil {
		if IsKind(err, ErrNotFound) {
			return domain.Build{}, fmt.Errorf("build %d not found in %s", id, project)
		}
		return domain.Build{}, err
	}
	return c.mapBuild(project, b), nil
}

// BuildTimeline returns a build's stages, jobs and tasks.
func (c *Client) BuildTimeline(ctx context.Context, project string, id int) ([]domain.TimelineRecord, error) {
	var payload struct {
		Records []struct {
			ID           string  `json:"id"`
			ParentID     string  `json:"parentId"`
			Type         string  `json:"type"`
			Name         string  `json:"name"`
			Order        int     `json:"order"`
			State        string  `json:"state"`
			Result       string  `json:"result"`
			StartTime    apiTime `json:"startTime"`
			FinishTime   apiTime `json:"finishTime"`
			ErrorCount   int     `json:"errorCount"`
			WarningCount int     `json:"warningCount"`
			Log          *struct {
				ID int `json:"id"`
			} `json:"log"`
			Issues []domain.BuildIssue `json:"issues"`
		} `json:"records"`
	}
	if err := c.get(ctx, c.url(fmt.Sprintf("/%s/_apis/build/builds/%d/timeline", seg(project), id), nil), &payload); err != nil {
		return nil, err
	}
	out := make([]domain.TimelineRecord, len(payload.Records))
	for i, r := range payload.Records {
		out[i] = domain.TimelineRecord{
			ID: r.ID, ParentID: r.ParentID, Type: r.Type, Name: r.Name, Order: r.Order, State: r.State, Result: r.Result,
			Started: r.StartTime.Time, Finished: r.FinishTime.Time, Errors: r.ErrorCount, Warnings: r.WarningCount, Issues: r.Issues,
		}
		if r.Log != nil {
			out[i].LogID = r.Log.ID
		}
	}
	return out, nil
}

// BuildLog returns the text of one log of a build (the end of it, for very long logs).
func (c *Client) BuildLog(ctx context.Context, project string, buildID, logID int) (string, error) {
	u := c.url(fmt.Sprintf("/%s/_apis/build/builds/%d/logs/%d", seg(project), buildID, logID), nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "text/plain")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request to %s failed: %w", req.URL.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", newAPIError(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return "", err
	}
	if len(data) > maxLogBytes {
		data = data[len(data)-maxLogBytes:]
		if i := strings.IndexByte(string(data), '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	return string(data), nil
}

// MyDisplayName returns the authenticated user's display name, as build and release filters
// expect it.
func (c *Client) MyDisplayName(ctx context.Context) (string, error) {
	who, err := c.whoAmI(ctx)
	return who.DisplayName, err
}
