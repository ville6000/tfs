package ado

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ville6000/tfs/internal/domain"
)

// MaxBatchSize is the most ids the work item batch endpoint accepts per call.
const MaxBatchSize = 200

const (
	fieldID        = "System.Id"
	fieldTitle     = "System.Title"
	fieldState     = "System.State"
	fieldType      = "System.WorkItemType"
	fieldAssigned  = "System.AssignedTo"
	fieldParent    = "System.Parent"
	fieldProject   = "System.TeamProject"
	fieldTags      = "System.Tags"
	fieldDesc      = "System.Description"
	fieldAccept    = "Microsoft.VSTS.Common.AcceptanceCriteria"
	relParent      = "System.LinkTypes.Hierarchy-Reverse"
	fieldStackRank = "Microsoft.VSTS.Common.StackRank"
	fieldPriority  = "Microsoft.VSTS.Common.BacklogPriority"
	listFieldsBase = "System.Id,System.Title,System.State,System.WorkItemType,System.AssignedTo,System.TeamProject,System.Tags"
)

type workItemResponse struct {
	ID        int                        `json:"id"`
	URL       string                     `json:"url"`
	Fields    map[string]json.RawMessage `json:"fields"`
	Relations []struct {
		Rel string `json:"rel"`
		URL string `json:"url"`
	} `json:"relations"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"_links"`
}

func (r *workItemResponse) str(field string) string {
	raw, ok := r.Fields[field]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// identity reads an identity field, which is an object on API 5+ and "Name <domain\user>" on
// older servers.
func (r *workItemResponse) identity(field string) string {
	raw, ok := r.Fields[field]
	if !ok {
		return ""
	}
	var obj struct {
		DisplayName string `json:"displayName"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.DisplayName != "" {
		return obj.DisplayName
	}
	s := r.str(field)
	if i := strings.Index(s, "<"); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

var relationID = regexp.MustCompile(`(?i)/workItems/(\d+)$`)

func (r *workItemResponse) parentFromRelations() int {
	for _, rel := range r.Relations {
		if rel.Rel != relParent {
			continue
		}
		if m := relationID.FindStringSubmatch(rel.URL); m != nil {
			id, _ := strconv.Atoi(m[1])
			return id
		}
	}
	return 0
}

func (c *Client) mapWorkItem(r *workItemResponse) domain.WorkItem {
	w := domain.WorkItem{
		ID:         r.ID,
		Type:       r.str(fieldType),
		Title:      r.str(fieldTitle),
		State:      r.str(fieldState),
		AssignedTo: r.identity(fieldAssigned),
		Project:    r.str(fieldProject),
		Tags:       r.str(fieldTags),
		URL:        r.Links.HTML.Href,
	}
	if p, err := strconv.Atoi(r.str(fieldParent)); err == nil && p > 0 {
		w.ParentID = p
	} else {
		w.ParentID = r.parentFromRelations()
	}
	if w.URL == "" {
		w.URL = c.WorkItemWebURL(w.Project, w.ID)
	}
	// The order field depends on the process: Stack Rank (Agile, CMMI, Basic) or Backlog
	// Priority (Scrum). Only the one that was requested is present.
	for _, f := range []string{fieldStackRank, fieldPriority} {
		if o, err := strconv.ParseFloat(r.str(f), 64); err == nil && o > 0 {
			w.Order = o
			break
		}
	}
	return w
}

// BuildWIQL renders a flat WIQL query. "@me" as assignee uses the server-side @Me macro.
func BuildWIQL(q domain.WorkItemQuery) string {
	clauses := []string{fmt.Sprintf("[System.TeamProject] = '%s'", esc(q.Project))}
	if len(q.Types) > 0 {
		clauses = append(clauses, fmt.Sprintf("[System.WorkItemType] IN (%s)", quoteList(q.Types)))
	}
	if len(q.ExcludedTypes) > 0 {
		clauses = append(clauses, fmt.Sprintf("[System.WorkItemType] NOT IN (%s)", quoteList(q.ExcludedTypes)))
	}
	if len(q.States) > 0 {
		clauses = append(clauses, fmt.Sprintf("[System.State] IN (%s)", quoteList(q.States)))
	}
	if len(q.ExcludedStates) > 0 {
		clauses = append(clauses, fmt.Sprintf("[System.State] NOT IN (%s)", quoteList(q.ExcludedStates)))
	}
	if a := strings.TrimSpace(q.AssignedTo); a != "" {
		if IsMe(a) {
			clauses = append(clauses, "[System.AssignedTo] = @Me")
		} else {
			clauses = append(clauses, fmt.Sprintf("[System.AssignedTo] = '%s'", esc(a)))
		}
	}
	for _, t := range q.Tags {
		clauses = append(clauses, fmt.Sprintf("[System.Tags] CONTAINS '%s'", esc(t)))
	}
	return "SELECT [System.Id] FROM WorkItems WHERE " + strings.Join(clauses, " AND ") +
		" ORDER BY [System.ChangedDate] DESC"
}

func esc(s string) string { return strings.ReplaceAll(s, "'", "''") }

func quoteList(vs []string) string {
	q := make([]string, len(vs))
	for i, v := range vs {
		q[i] = "'" + esc(v) + "'"
	}
	return strings.Join(q, ", ")
}

func (c *Client) queryIDs(ctx context.Context, q domain.WorkItemQuery) ([]int, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	var payload struct {
		WorkItems []struct {
			ID int `json:"id"`
		} `json:"workItems"`
	}
	u := c.url("/"+seg(q.Project)+"/_apis/wit/wiql", url.Values{"$top": {strconv.Itoa(q.Top)}})
	if err := c.postJSON(ctx, u, map[string]string{"query": BuildWIQL(q)}, &payload); err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(payload.WorkItems))
	for _, w := range payload.WorkItems {
		if w.ID > 0 {
			ids = append(ids, w.ID)
		}
	}
	return ids, nil
}

func (c *Client) batch(ctx context.Context, ids []int, extraFields ...string) (map[int]*workItemResponse, error) {
	fields := listFieldsBase + "," + fieldParent
	for _, f := range extraFields {
		if f != "" {
			fields += "," + f
		}
	}
	out := make(map[int]*workItemResponse, len(ids))
	for chunk := range slices.Chunk(ids, MaxBatchSize) {
		strs := make([]string, len(chunk))
		for i, id := range chunk {
			strs[i] = strconv.Itoa(id)
		}
		var payload struct {
			Value []*workItemResponse `json:"value"`
		}
		u := c.url("/_apis/wit/workitems", url.Values{
			"ids":         {strings.Join(strs, ",")},
			"fields":      {fields},
			"errorPolicy": {"omit"},
		})
		if err := c.get(ctx, u, &payload); err != nil {
			return nil, err
		}
		for _, r := range payload.Value {
			if r != nil && r.ID > 0 {
				out[r.ID] = r
			}
		}
	}
	return out, nil
}

// ListWorkItems runs a flat query and returns matching items, most recently changed first.
func (c *Client) ListWorkItems(ctx context.Context, q domain.WorkItemQuery) ([]domain.WorkItem, error) {
	ids, err := c.queryIDs(ctx, q)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	byID, err := c.batch(ctx, ids, q.OrderField)
	if err != nil {
		return nil, err
	}
	items := make([]domain.WorkItem, 0, len(ids))
	for _, id := range ids {
		if r, ok := byID[id]; ok {
			items = append(items, c.mapWorkItem(r))
		}
	}
	return items, nil
}

// ListHierarchy returns matching items plus every ancestor needed to connect them to a root.
func (c *Client) ListHierarchy(ctx context.Context, q domain.WorkItemQuery) ([]domain.WorkItem, error) {
	items, err := c.ListWorkItems(ctx, q)
	if err != nil {
		return nil, err
	}
	byID := make(map[int]domain.WorkItem, len(items))
	for _, w := range items {
		byID[w.ID] = w
	}
	for {
		var missing []int
		seen := map[int]bool{}
		for _, w := range byID {
			if w.HasParent() && !seen[w.ParentID] {
				if _, ok := byID[w.ParentID]; !ok {
					missing = append(missing, w.ParentID)
					seen[w.ParentID] = true
				}
			}
		}
		if len(missing) == 0 {
			break
		}
		fetched, err := c.batch(ctx, missing, q.OrderField)
		if err != nil {
			return nil, err
		}
		added := false
		for _, id := range missing {
			if r, ok := fetched[id]; ok {
				byID[id] = c.mapWorkItem(r)
				added = true
			}
		}
		if !added {
			break // parents unreachable (deleted, cross-project, or no access)
		}
	}
	out := make([]domain.WorkItem, 0, len(byID))
	for _, w := range byID {
		out = append(out, w)
	}
	return out, nil
}

func (c *Client) getWorkItem(ctx context.Context, id int) (*workItemResponse, error) {
	if id <= 0 {
		return nil, fmt.Errorf("work item id must be positive")
	}
	var r workItemResponse
	u := c.url(fmt.Sprintf("/_apis/wit/workitems/%d", id), url.Values{"$expand": {"all"}})
	if err := c.get(ctx, u, &r); err != nil {
		if IsKind(err, ErrNotFound) {
			return nil, fmt.Errorf("work item #%d not found", id)
		}
		return nil, err
	}
	return &r, nil
}

// GetWorkItem returns a work item summary (no comments).
func (c *Client) GetWorkItem(ctx context.Context, id int) (domain.WorkItem, error) {
	r, err := c.getWorkItem(ctx, id)
	if err != nil {
		return domain.WorkItem{}, err
	}
	return c.mapWorkItem(r), nil
}

// GetWorkItemDetail returns a work item with description, acceptance criteria and comments.
func (c *Client) GetWorkItemDetail(ctx context.Context, id int) (domain.WorkItemDetail, error) {
	r, err := c.getWorkItem(ctx, id)
	if err != nil {
		return domain.WorkItemDetail{}, err
	}
	d := domain.WorkItemDetail{
		WorkItem:           c.mapWorkItem(r),
		Description:        r.str(fieldDesc),
		AcceptanceCriteria: r.str(fieldAccept),
	}
	if d.Project != "" {
		comments, err := c.Comments(ctx, d.Project, id)
		if err != nil {
			return d, err
		}
		d.Comments = comments
	}
	return d, nil
}

// AncestorChain returns the item followed by its parents up to the root.
func (c *Client) AncestorChain(ctx context.Context, id int) ([]domain.WorkItem, error) {
	var chain []domain.WorkItem
	visited := map[int]bool{}
	for cur := id; cur > 0 && !visited[cur]; {
		visited[cur] = true
		w, err := c.GetWorkItem(ctx, cur)
		if err != nil {
			return nil, err
		}
		chain = append(chain, w)
		cur = w.ParentID
	}
	return chain, nil
}

type patchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// CreateWorkItem creates a work item from a draft.
func (c *Client) CreateWorkItem(ctx context.Context, d domain.WorkItemDraft) (domain.WorkItem, error) {
	ops := []patchOp{{"add", "/fields/" + fieldTitle, strings.TrimSpace(d.Title)}}
	if d.Description != "" {
		ops = append(ops, patchOp{"add", "/fields/" + fieldDesc, d.Description})
	}
	if d.AssignedTo != "" {
		ops = append(ops, patchOp{"add", "/fields/" + fieldAssigned, d.AssignedTo})
	}
	if len(d.Tags) > 0 {
		ops = append(ops, patchOp{"add", "/fields/" + fieldTags, strings.Join(d.Tags, "; ")})
	}
	if d.ParentID > 0 {
		ops = append(ops, patchOp{"add", "/relations/-", map[string]string{
			"rel": relParent,
			"url": fmt.Sprintf("%s/_apis/wit/workItems/%d", c.collection, d.ParentID),
		}})
	}
	u := c.url("/"+seg(d.Project)+"/_apis/wit/workitems/$"+seg(d.Type), nil)
	var r workItemResponse
	if err := c.do(ctx, http.MethodPost, u, jsonPatch, ops, &r); err != nil {
		return domain.WorkItem{}, err
	}
	return c.mapWorkItem(&r), nil
}

// UpdateWorkItem changes state, title and/or assignee.
func (c *Client) UpdateWorkItem(ctx context.Context, id int, upd domain.WorkItemUpdate) (domain.WorkItem, error) {
	if upd.IsEmpty() {
		return domain.WorkItem{}, errors.New("nothing to update")
	}
	var ops []patchOp
	if upd.State != nil {
		ops = append(ops, patchOp{"add", "/fields/" + fieldState, *upd.State})
	}
	if upd.Title != nil {
		ops = append(ops, patchOp{"add", "/fields/" + fieldTitle, *upd.Title})
	}
	if upd.AssignedTo != nil {
		ops = append(ops, patchOp{"add", "/fields/" + fieldAssigned, *upd.AssignedTo})
	}
	var r workItemResponse
	if err := c.do(ctx, http.MethodPatch, c.url(fmt.Sprintf("/_apis/wit/workitems/%d", id), nil), jsonPatch, ops, &r); err != nil {
		return domain.WorkItem{}, err
	}
	return c.mapWorkItem(&r), nil
}

type commentResponse struct {
	ID          int       `json:"id"`
	Text        string    `json:"text"`
	CreatedDate time.Time `json:"createdDate"`
	CreatedBy   struct {
		DisplayName string `json:"displayName"`
	} `json:"createdBy"`
}

func (r commentResponse) toDomain() domain.Comment {
	author := r.CreatedBy.DisplayName
	if author == "" {
		author = "unknown"
	}
	return domain.Comment{ID: r.ID, Author: author, Created: r.CreatedDate, Text: r.Text}
}

func (c *Client) commentsURL(project string, id int) string {
	return c.url(fmt.Sprintf("/%s/_apis/wit/workItems/%d/comments", seg(project), id),
		url.Values{"api-version": {c.apiVersion + "-preview.3"}})
}

// Comments lists a work item's comments.
func (c *Client) Comments(ctx context.Context, project string, id int) ([]domain.Comment, error) {
	var payload struct {
		Comments []commentResponse `json:"comments"`
	}
	if err := c.get(ctx, c.commentsURL(project, id), &payload); err != nil {
		return nil, err
	}
	out := make([]domain.Comment, len(payload.Comments))
	for i, cm := range payload.Comments {
		out[i] = cm.toDomain()
	}
	return out, nil
}

// EncodeComment converts plain text into the HTML the comments API stores.
func EncodeComment(text string) string {
	s := html.EscapeString(strings.ReplaceAll(text, "\r\n", "\n"))
	return strings.ReplaceAll(s, "\n", "<br/>")
}

// AddComment posts a plain-text comment.
func (c *Client) AddComment(ctx context.Context, id int, text string) (domain.Comment, error) {
	if strings.TrimSpace(text) == "" {
		return domain.Comment{}, errors.New("comment text cannot be empty")
	}
	w, err := c.GetWorkItem(ctx, id)
	if err != nil {
		return domain.Comment{}, err
	}
	if w.Project == "" {
		return domain.Comment{}, fmt.Errorf("could not resolve the project of work item #%d", id)
	}
	var r commentResponse
	if err := c.postJSON(ctx, c.commentsURL(w.Project, id), map[string]string{"text": EncodeComment(text)}, &r); err != nil {
		return domain.Comment{}, err
	}
	return r.toDomain(), nil
}

// TypeStates lists the states defined for a work item type, in workflow order.
func (c *Client) TypeStates(ctx context.Context, project, workItemType string) ([]string, error) {
	var payload struct {
		Value []struct {
			Name string `json:"name"`
		} `json:"value"`
	}
	u := c.url(fmt.Sprintf("/%s/_apis/wit/workitemtypes/%s/states", seg(project), seg(workItemType)),
		url.Values{"api-version": {c.apiVersion + "-preview.1"}})
	if err := c.get(ctx, u, &payload); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(payload.Value))
	for _, s := range payload.Value {
		out = append(out, s.Name)
	}
	return out, nil
}

// TypeCategories lists the project's work item type categories (Epic, Feature, Requirement,
// Task, Bug, Hidden, …), which map process-specific type names onto backlog levels.
func (c *Client) TypeCategories(ctx context.Context, project string) ([]domain.TypeCategory, error) {
	var payload struct {
		Value []struct {
			ReferenceName       string `json:"referenceName"`
			DefaultWorkItemType struct {
				Name string `json:"name"`
			} `json:"defaultWorkItemType"`
			WorkItemTypes []struct {
				Name string `json:"name"`
			} `json:"workItemTypes"`
		} `json:"value"`
	}
	if err := c.get(ctx, c.url("/"+seg(project)+"/_apis/wit/workitemtypecategories", nil), &payload); err != nil {
		return nil, err
	}
	out := make([]domain.TypeCategory, len(payload.Value))
	for i, v := range payload.Value {
		types := make([]string, len(v.WorkItemTypes))
		for j, t := range v.WorkItemTypes {
			types[j] = t.Name
		}
		out[i] = domain.TypeCategory{RefName: v.ReferenceName, Default: v.DefaultWorkItemType.Name, Types: types}
	}
	return out, nil
}

// BacklogOrderField returns the field that holds backlog order for the project's process
// (Microsoft.VSTS.Common.StackRank or Microsoft.VSTS.Common.BacklogPriority). An empty team means
// the project's default team.
func (c *Client) BacklogOrderField(ctx context.Context, project, team string) (string, error) {
	path := "/" + seg(project)
	if team != "" {
		path += "/" + seg(team)
	}
	var payload struct {
		BacklogFields struct {
			TypeFields map[string]string `json:"typeFields"`
		} `json:"backlogFields"`
	}
	if err := c.get(ctx, c.url(path+"/_apis/work/backlogconfiguration", nil), &payload); err != nil {
		return "", err
	}
	f := payload.BacklogFields.TypeFields["Order"]
	if f == "" {
		return "", errors.New("server did not report a backlog order field")
	}
	return f, nil
}

// DefaultTeam returns the name of the project's default team.
func (c *Client) DefaultTeam(ctx context.Context, project string) (string, error) {
	var payload struct {
		DefaultTeam struct {
			Name string `json:"name"`
		} `json:"defaultTeam"`
	}
	if err := c.get(ctx, c.url("/_apis/projects/"+seg(project), nil), &payload); err != nil {
		return "", err
	}
	if payload.DefaultTeam.Name == "" {
		return "", fmt.Errorf("project %q has no default team; set one with `tfs config set team <name>`", project)
	}
	return payload.DefaultTeam.Name, nil
}

// ReorderWorkItems moves work items on a team's backlog and returns their new order values.
// It needs Azure DevOps Server 2020 or later.
func (c *Client) ReorderWorkItems(ctx context.Context, project, team string, op domain.ReorderOp) (map[int]float64, error) {
	var payload struct {
		Value []struct {
			ID    int     `json:"id"`
			Order float64 `json:"order"`
		} `json:"value"`
	}
	u := c.url(fmt.Sprintf("/%s/%s/_apis/work/workitemsorder", seg(project), seg(team)),
		url.Values{"api-version": {c.apiVersion + "-preview.1"}})
	if err := c.do(ctx, http.MethodPatch, u, "application/json", op, &payload); err != nil {
		if IsKind(err, ErrNotFound) {
			return nil, fmt.Errorf("reordering is not available (needs Azure DevOps Server 2020+, and team %q must exist): %w", team, err)
		}
		return nil, err
	}
	out := make(map[int]float64, len(payload.Value))
	for _, v := range payload.Value {
		out[v.ID] = v.Order
	}
	return out, nil
}
