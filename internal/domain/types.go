package domain

import "strings"

// TypeCategory is a work item type category of a project's process, e.g.
// Microsoft.RequirementCategory containing "User Story" (Agile) or "Product Backlog Item" (Scrum).
type TypeCategory struct {
	RefName string   `json:"referenceName"`
	Default string   `json:"defaultType"`
	Types   []string `json:"types"`
}

// Backlog category reference names, from the top of the hierarchy down.
const (
	CategoryEpic        = "Microsoft.EpicCategory"
	CategoryFeature     = "Microsoft.FeatureCategory"
	CategoryRequirement = "Microsoft.RequirementCategory"
	CategoryTask        = "Microsoft.TaskCategory"
	CategoryBug         = "Microsoft.BugCategory"
	categoryHidden      = "Microsoft.HiddenCategory"
)

// childCategory maps a parent's category to the category its children usually belong to.
var childCategory = map[string]string{
	CategoryEpic:        CategoryFeature,
	CategoryFeature:     CategoryRequirement,
	CategoryRequirement: CategoryTask,
	CategoryBug:         CategoryTask,
}

var backlogOrder = []string{CategoryEpic, CategoryFeature, CategoryRequirement, CategoryBug, CategoryTask}

// fallbackChild is used when the server's categories are unavailable.
var fallbackChild = map[string]string{
	"epic":                 "Feature",
	"feature":              "User Story",
	"user story":           "Task",
	"product backlog item": "Task",
	"requirement":          "Task",
	"issue":                "Task",
	"bug":                  "Task",
}

// FallbackTypes are offered when the server's categories are unavailable.
var FallbackTypes = []string{"Epic", "Feature", "User Story", "Product Backlog Item", "Task", "Bug", "Issue"}

// ChildTypeOptions returns the work item types offered when creating a child of parentType,
// with the most likely child type first (Epic → Feature → User Story/PBI → Task).
// Only backlog types are offered: Epic, Feature, requirement, task and bug categories plus
// custom portfolio categories, minus hidden types.
func ChildTypeOptions(cats []TypeCategory, parentType string) []string {
	if len(cats) == 0 {
		return withFirst(FallbackTypes, fallbackChild[strings.ToLower(parentType)])
	}
	hidden := map[string]bool{}
	byRef := map[string]TypeCategory{}
	for _, c := range cats {
		byRef[c.RefName] = c
		if c.RefName == categoryHidden {
			for _, t := range c.Types {
				hidden[strings.ToLower(t)] = true
			}
		}
	}

	var options []string
	seen := map[string]bool{}
	add := func(types ...string) {
		for _, t := range types {
			k := strings.ToLower(t)
			if t != "" && !hidden[k] && !seen[k] {
				seen[k] = true
				options = append(options, t)
			}
		}
	}
	// Custom portfolio levels (above Epic) first, then the standard backlog levels.
	for _, c := range cats {
		if strings.HasPrefix(c.RefName, "Custom.") {
			add(c.Types...)
		}
	}
	for _, ref := range backlogOrder {
		c := byRef[ref]
		add(c.Default)
		add(c.Types...)
	}

	def := ""
	for _, c := range cats {
		if c.RefName == categoryHidden || !containsFold(c.Types, parentType) {
			continue
		}
		child := childCategory[c.RefName]
		if child == "" && strings.HasPrefix(c.RefName, "Custom.") {
			child = CategoryEpic // custom portfolio levels sit above Epic
		}
		if cc, ok := byRef[child]; ok {
			def = cc.Default
			if def == "" && len(cc.Types) > 0 {
				def = cc.Types[0]
			}
		}
		break
	}
	if def == "" {
		def = byRef[CategoryTask].Default
	}
	return withFirst(options, def)
}

func withFirst(list []string, first string) []string {
	out := []string{}
	if first != "" {
		out = append(out, first)
	}
	for _, v := range list {
		if !strings.EqualFold(v, first) {
			out = append(out, v)
		}
	}
	return out
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}
