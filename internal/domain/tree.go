package domain

import (
	"errors"
	"fmt"
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
	// Ordered reports that siblings are in backlog order, so they can be reordered.
	Ordered bool `json:"-"`
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
	sortSiblings(top)
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
	sortSiblings(kids)
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

// sortSiblings orders items as the backlog does: by backlog order, unranked items last, then id.
func sortSiblings(items []WorkItem) {
	slices.SortStableFunc(items, func(a, b WorkItem) int {
		switch {
		case a.Order == b.Order:
			return a.ID - b.ID
		case a.Order == 0:
			return 1
		case b.Order == 0:
			return -1
		case a.Order < b.Order:
			return -1
		}
		return 1
	})
}

// ReorderOp asks the server to place IDs between PreviousID and NextID (0 meaning the start or
// end of the list) under ParentID (0 for top-level items).
type ReorderOp struct {
	IDs        []int `json:"ids"`
	ParentID   int   `json:"parentId"`
	PreviousID int   `json:"previousId"`
	NextID     int   `json:"nextId"`
}

// peers reports whether b can be reordered relative to a: they share a parent and, in the
// "No parent" group (which mixes backlog levels), also a type.
func peers(a, b WorkItem, sameType bool) bool {
	return a.ParentID == b.ParentID && (!sameType || strings.EqualFold(a.Type, b.Type))
}

// ReorderPlan returns the request that persists id's current position among its peers in nodes.
func ReorderPlan(nodes []*TreeNode, id int, sameType bool) (ReorderOp, error) {
	i := slices.IndexFunc(nodes, func(n *TreeNode) bool { return n.Item.ID == id })
	if i < 0 {
		return ReorderOp{}, fmt.Errorf("work item #%d is not in this list", id)
	}
	it := nodes[i].Item
	op := ReorderOp{IDs: []int{id}, ParentID: it.ParentID}
	for j := i - 1; j >= 0; j-- {
		if peers(it, nodes[j].Item, sameType) {
			op.PreviousID = nodes[j].Item.ID
			break
		}
	}
	for j := i + 1; j < len(nodes); j++ {
		if peers(it, nodes[j].Item, sameType) {
			op.NextID = nodes[j].Item.ID
			break
		}
	}
	return op, nil
}

// MoveSibling moves id one place up (delta < 0) or down past its nearest peer in nodes,
// rearranging nodes in place, and returns the request that persists the new position.
func MoveSibling(nodes []*TreeNode, id, delta int, sameType bool) (ReorderOp, error) {
	i := slices.IndexFunc(nodes, func(n *TreeNode) bool { return n.Item.ID == id })
	if i < 0 {
		return ReorderOp{}, fmt.Errorf("work item #%d is not in this list", id)
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	j := -1
	for k := i + step; k >= 0 && k < len(nodes); k += step {
		if peers(nodes[i].Item, nodes[k].Item, sameType) {
			j = k
			break
		}
	}
	if j < 0 {
		if step < 0 {
			return ReorderOp{}, errors.New("already first")
		}
		return ReorderOp{}, errors.New("already last")
	}
	moved := nodes[i]
	if step < 0 { // shift nodes[j:i] down one and put moved at j
		copy(nodes[j+1:i+1], nodes[j:i])
	} else { // shift nodes[i+1:j+1] up one and put moved at j
		copy(nodes[i:j], nodes[i+1:j+1])
	}
	nodes[j] = moved
	return ReorderPlan(nodes, id, sameType)
}

// Siblings returns the list that holds id (the roots, the orphans, or a node's children) and
// whether it is the mixed "No parent" group.
func (t Tree) Siblings(id int) (nodes []*TreeNode, orphans bool, ok bool) {
	var find func([]*TreeNode) []*TreeNode
	find = func(ns []*TreeNode) []*TreeNode {
		for _, n := range ns {
			if n.Item.ID == id {
				return ns
			}
			if found := find(n.Children); found != nil {
				return found
			}
		}
		return nil
	}
	if ns := find(t.Roots); ns != nil {
		return ns, false, true
	}
	for _, n := range t.Orphans {
		if n.Item.ID == id {
			return t.Orphans, true, true
		}
	}
	if ns := find(t.Orphans); ns != nil {
		return ns, false, true
	}
	return nil, false, false
}

// DefaultTreeExcludedTypes are hidden from trees unless explicitly included via --type.
var DefaultTreeExcludedTypes = []string{"Test Case", "Test Plan", "Test Suite"}
