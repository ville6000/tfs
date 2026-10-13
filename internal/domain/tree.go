package domain

import (
	"slices"
	"strings"
)

// TreeNode is a work item in a hierarchy. Matches is false for ancestors kept only to connect a
// matching descendant to its root.
type TreeNode struct {
	Item     WorkItem    `json:"item"`
	Matches  bool        `json:"matches"`
	Children []*TreeNode `json:"children,omitempty"`
}

// Tree is a work item hierarchy: Roots are top-level Epics, Orphans are parentless non-Epics.
type Tree struct {
	Roots   []*TreeNode `json:"roots"`
	Orphans []*TreeNode `json:"orphans,omitempty"`
}

// IsEmpty reports whether the tree has no nodes.
func (t Tree) IsEmpty() bool { return len(t.Roots) == 0 && len(t.Orphans) == 0 }

const epicType = "Epic"

// BuildForest arranges items into a hierarchy, pruning branches with no matching item.
func BuildForest(items []WorkItem, filter Filter) Tree {
	byID := make(map[int]bool, len(items))
	for _, it := range items {
		byID[it.ID] = true
	}
	childrenOf := map[int][]WorkItem{}
	var top []WorkItem
	for _, it := range items {
		if it.HasParent() && byID[it.ParentID] {
			childrenOf[it.ParentID] = append(childrenOf[it.ParentID], it)
		} else {
			top = append(top, it)
		}
	}
	sortByID(top)
	var tree Tree
	for _, it := range top {
		node := buildNode(it, childrenOf, filter, map[int]bool{})
		if node == nil {
			continue
		}
		if strings.EqualFold(it.Type, epicType) {
			tree.Roots = append(tree.Roots, node)
		} else {
			tree.Orphans = append(tree.Orphans, node)
		}
	}
	return tree
}

// BuildChain builds a single path from the root down to the focus item. chain is ordered from
// the focus item up to its topmost ancestor.
func BuildChain(chain []WorkItem, filter Filter) Tree {
	var current *TreeNode
	for _, it := range chain {
		n := &TreeNode{Item: it, Matches: filter.Matches(it)}
		if current != nil {
			n.Children = []*TreeNode{current}
		}
		current = n
	}
	if current == nil {
		return Tree{}
	}
	return Tree{Roots: []*TreeNode{current}}
}

func buildNode(it WorkItem, childrenOf map[int][]WorkItem, filter Filter, ancestry map[int]bool) *TreeNode {
	if ancestry[it.ID] {
		return nil // defensive: parent cycle
	}
	ancestry[it.ID] = true
	defer delete(ancestry, it.ID)

	kids := childrenOf[it.ID]
	sortByID(kids)
	var built []*TreeNode
	for _, c := range kids {
		if n := buildNode(c, childrenOf, filter, ancestry); n != nil {
			built = append(built, n)
		}
	}
	matches := filter.Matches(it)
	if !matches && len(built) == 0 {
		return nil
	}
	return &TreeNode{Item: it, Matches: matches, Children: built}
}

func sortByID(items []WorkItem) {
	slices.SortFunc(items, func(a, b WorkItem) int { return a.ID - b.ID })
}

// DefaultTreeExcludedTypes are hidden from trees unless explicitly included via --type.
var DefaultTreeExcludedTypes = []string{"Test Case", "Test Plan", "Test Suite"}
