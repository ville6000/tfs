package domain

import "errors"

// StateScope is a shorthand selection of states driven by --all/--open/--resolved.
type StateScope int

// State scopes. ScopeDefault means no shorthand was given.
const (
	ScopeDefault StateScope = iota
	ScopeAll
	ScopeOpen
	ScopeResolved
)

// State buckets spanning the common process templates (Agile, Scrum, Basic, CMMI).
var (
	ResolvedStates      = []string{"Resolved", "Closed", "Done", "Completed"}
	NotOpenStates       = []string{"Resolved", "Closed", "Done", "Completed", "Removed"}
	DefaultHiddenStates = []string{"Closed", "Removed"}
)

// ScopeFromFlags maps the shorthand flags to a scope, rejecting conflicting combinations.
func ScopeFromFlags(all, open, resolved bool, states, excludedStates []string) (StateScope, error) {
	n := 0
	for _, b := range []bool{all, open, resolved} {
		if b {
			n++
		}
	}
	if n > 1 {
		return ScopeDefault, errors.New("use only one of --all, --open, or --resolved")
	}
	if n == 1 && (len(states) > 0 || len(excludedStates) > 0) {
		return ScopeDefault, errors.New("--all/--open/--resolved cannot be combined with --state or --no-state")
	}
	switch {
	case all:
		return ScopeAll, nil
	case open:
		return ScopeOpen, nil
	case resolved:
		return ScopeResolved, nil
	}
	return ScopeDefault, nil
}

// ResolveStateFilters combines explicit state filters with a scope. When neither is given and
// hideByDefault is true, Closed and Removed items are hidden.
func ResolveStateFilters(scope StateScope, states, excluded []string, hideByDefault bool) (inc, exc []string) {
	inc, exc = ResolveSlugs(states), ResolveSlugs(excluded)
	switch scope {
	case ScopeAll:
		return nil, nil
	case ScopeOpen:
		return nil, append([]string(nil), NotOpenStates...)
	case ScopeResolved:
		return append([]string(nil), ResolvedStates...), nil
	}
	if hideByDefault && len(inc) == 0 && len(exc) == 0 {
		exc = append([]string(nil), DefaultHiddenStates...)
	}
	return inc, exc
}
