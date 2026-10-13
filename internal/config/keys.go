package config

import (
	"fmt"
	"strings"
)

// Key describes a configuration key.
type Key struct {
	Name        string
	Description string
	Default     string
	// GlobalOnly keys are user-specific and never read from or written to a project file.
	GlobalOnly bool
	Validate   func(string) error

	get func(*File) string
	set func(*File, string)
}

// Keys lists every supported configuration key.
var Keys = []Key{
	{
		Name: "collectionUrl", Description: "Collection URL, e.g. https://ado.example.com/DefaultCollection",
		Validate: ValidateCollectionURL,
		get:      func(f *File) string { return f.CollectionURL },
		set:      func(f *File, v string) { f.CollectionURL = v },
	},
	{
		Name: "defaultProject", Description: "Project used when --project is omitted",
		get: func(f *File) string { return f.DefaultProject },
		set: func(f *File, v string) { f.DefaultProject = v },
	},
	{
		Name: "apiVersion", Description: "REST API version sent with every request", Default: DefaultAPIVersion,
		get: func(f *File) string { return f.APIVersion },
		set: func(f *File, v string) { f.APIVersion = v },
	},
	{
		Name: "userIdentity", Description: "Identity '@me' resolves to (skips the server lookup)", GlobalOnly: true,
		get: func(f *File) string { return f.UserIdentity },
		set: func(f *File, v string) { f.UserIdentity = v },
	},
	{
		Name: "defaultType", Description: "Work item type for `wi create` when --type is omitted", Default: "Task",
		get: func(f *File) string { return f.DefaultType },
		set: func(f *File, v string) { f.DefaultType = v },
	},
	{
		Name: "branchTemplate", Description: "Branch name for `wi worktree`; placeholders {id} {slug} {type}", Default: "feature/{id}-{slug}",
		Validate: func(v string) error {
			if !strings.Contains(v, "{id}") {
				return fmt.Errorf("branchTemplate must contain {id}")
			}
			return nil
		},
		get: func(f *File) string { return f.BranchTemplate },
		set: func(f *File, v string) { f.BranchTemplate = v },
	},
	{
		Name: "remote", Description: "Git remote used for fetch and `pr create`", Default: "origin",
		get: func(f *File) string { return f.Remote },
		set: func(f *File, v string) { f.Remote = v },
	},
	{
		Name: "requireParentFor", Description: "Comma-separated work item types that `wi create` requires a --parent for",
		get: func(f *File) string { return strings.Join(f.RequireParentFor, ", ") },
		set: func(f *File, v string) { f.RequireParentFor = splitList(v) },
	},
}

// LookupKey finds a key by name (case-insensitive).
func LookupKey(name string) (Key, error) {
	for _, k := range Keys {
		if strings.EqualFold(k.Name, name) {
			return k, nil
		}
	}
	names := make([]string, len(Keys))
	for i, k := range Keys {
		names[i] = k.Name
	}
	return Key{}, fmt.Errorf("unknown config key %q (valid: %s)", name, strings.Join(names, ", "))
}
