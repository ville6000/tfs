package domain

import "strings"

// Filter decides whether a work item matches type and state allow/deny lists. Values may be slugs.
type Filter struct {
	includedTypes, excludedTypes, includedStates, excludedStates map[string]bool
}

// NewFilter builds a filter; empty include lists allow everything not excluded.
func NewFilter(includedTypes, excludedTypes, includedStates, excludedStates []string) Filter {
	return Filter{
		includedTypes:  set(includedTypes),
		excludedTypes:  set(excludedTypes),
		includedStates: set(includedStates),
		excludedStates: set(excludedStates),
	}
}

// Matches reports whether the item passes the filter.
func (f Filter) Matches(w WorkItem) bool {
	return allows(w.Type, f.includedTypes, f.excludedTypes) &&
		allows(w.State, f.includedStates, f.excludedStates)
}

func allows(value string, inc, exc map[string]bool) bool {
	k := strings.ToLower(ResolveSlug(value))
	if exc[k] {
		return false
	}
	return len(inc) == 0 || inc[k]
}

func set(values []string) map[string]bool {
	m := map[string]bool{}
	for _, v := range values {
		if r := ResolveSlug(v); r != "" {
			m[strings.ToLower(r)] = true
		}
	}
	return m
}
