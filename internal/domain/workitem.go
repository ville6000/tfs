// Package domain holds the Azure DevOps concepts the CLI works with and the pure logic around
// them (slugs, state scopes, filters, hierarchy trees, branch names, pull request approval).
package domain

import (
	"fmt"
	"strings"
)

// WorkItem is the summary view of a work item used by listings, trees and pickers.
type WorkItem struct {
	ID         int    `json:"id"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	State      string `json:"state"`
	AssignedTo string `json:"assignedTo,omitempty"`
	ParentID   int    `json:"parentId,omitempty"`
	Project    string `json:"project,omitempty"`
	Tags       string `json:"tags,omitempty"`
	URL        string `json:"url,omitempty"`
	// Order is the backlog position (Stack Rank or Backlog Priority); 0 when unknown or unranked.
	Order float64 `json:"order,omitempty"`
}

// HasParent reports whether the item has a known parent.
func (w WorkItem) HasParent() bool { return w.ParentID > 0 }

// WorkItemDetail is a work item together with its long-form fields and comments.
type WorkItemDetail struct {
	WorkItem
	Description        string    `json:"description,omitempty"`
	AcceptanceCriteria string    `json:"acceptanceCriteria,omitempty"`
	Comments           []Comment `json:"comments,omitempty"`
}

// WorkItemDraft is the input for creating a work item.
type WorkItemDraft struct {
	Project     string
	Type        string
	Title       string
	Description string
	AssignedTo  string
	Tags        []string
	ParentID    int
}

// Validate checks the draft. requireParentFor lists work item types that must have a parent
// (configured per user/project; empty by default).
func (d WorkItemDraft) Validate(requireParentFor []string) error {
	if strings.TrimSpace(d.Project) == "" {
		return fmt.Errorf("project is required")
	}
	if strings.TrimSpace(d.Type) == "" {
		return fmt.Errorf("work item type is required")
	}
	if strings.TrimSpace(d.Title) == "" {
		return fmt.Errorf("title must not be empty")
	}
	if d.ParentID <= 0 {
		for _, t := range requireParentFor {
			if strings.EqualFold(ResolveSlug(t), d.Type) {
				return fmt.Errorf("a %s must have a parent work item (--parent)", d.Type)
			}
		}
	}
	return nil
}

// WorkItemUpdate holds the fields to change on an existing work item; nil means unchanged.
type WorkItemUpdate struct {
	State      *string
	Title      *string
	AssignedTo *string
}

// IsEmpty reports whether the update changes nothing.
func (u WorkItemUpdate) IsEmpty() bool {
	return u.State == nil && u.Title == nil && u.AssignedTo == nil
}

// WorkItemQuery describes a flat work item search.
type WorkItemQuery struct {
	Project        string
	Types          []string
	ExcludedTypes  []string
	States         []string
	ExcludedStates []string
	AssignedTo     string
	Tags           []string
	Top            int
	// OrderField is the backlog order field to fetch (e.g. Microsoft.VSTS.Common.StackRank);
	// empty to skip it.
	OrderField string
	// Scope limits the query to a team's items; nil for the whole project.
	Scope *TeamScope
}

// TeamScope is the set of items a team's board shows: those whose team field (usually
// System.AreaPath) matches one of the team's values.
type TeamScope struct {
	Field  string
	Values []TeamFieldValue
}

// TeamFieldValue is one of a team's area paths; IncludeChildren also matches the paths under it.
type TeamFieldValue struct {
	Value           string
	IncludeChildren bool
}

// Query limits.
const (
	DefaultWorkItemTop = 50
	MaxWorkItemTop     = 1000
)

// Validate checks the query bounds.
func (q WorkItemQuery) Validate() error {
	if strings.TrimSpace(q.Project) == "" {
		return fmt.Errorf("project is required")
	}
	if q.Top <= 0 || q.Top > MaxWorkItemTop {
		return fmt.Errorf("--top must be between 1 and %d", MaxWorkItemTop)
	}
	return nil
}
