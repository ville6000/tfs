// Package config loads and writes the tfs configuration: a global file (XDG / ~/.config/tfs)
// merged field-by-field under an optional per-project file found at the git repository root.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Environment variables.
const (
	EnvPAT           = "TFS_PAT"
	EnvAllowInsecure = "TFS_ALLOW_INSECURE"
	EnvCollection    = "TFS_COLLECTION_URL"
	EnvXDGConfigHome = "XDG_CONFIG_HOME"
)

// DefaultAPIVersion is the REST API version used when none is configured. 6.0 is supported by
// Azure DevOps Server 2020 and later.
const DefaultAPIVersion = "6.0"

const (
	toolDir     = "tfs"
	fileName    = "config.json"
	projectName = "tfs.json"
)

// File is the on-disk configuration format. All fields are optional.
type File struct {
	CollectionURL    string   `json:"collectionUrl,omitempty"`
	DefaultProject   string   `json:"defaultProject,omitempty"`
	Team             string   `json:"team,omitempty"`
	APIVersion       string   `json:"apiVersion,omitempty"`
	UserIdentity     string   `json:"userIdentity,omitempty"`
	DefaultType      string   `json:"defaultType,omitempty"`
	BranchTemplate   string   `json:"branchTemplate,omitempty"`
	Remote           string   `json:"remote,omitempty"`
	RequireParentFor []string `json:"requireParentFor,omitempty"`
}

// Config is the loaded global and project configuration.
type Config struct {
	GlobalPath string
	// ProjectRoot is the enclosing git repository root, or "" outside a repository.
	ProjectRoot string
	// ProjectPath is the existing project config file, or "" if there is none.
	ProjectPath string
	Global      File
	Project     File
	Warnings    []string

	env func(string) string
}

// Load reads configuration for the current working directory.
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return LoadFrom(cwd, home, os.Getenv)
}

// LoadFrom reads configuration for the given directory, home and environment (for tests).
func LoadFrom(cwd, home string, env func(string) string) (*Config, error) {
	c := &Config{env: env, GlobalPath: ResolveGlobalPath(env, home, fileExists)}
	if err := readFile(c.GlobalPath, &c.Global); err != nil {
		return nil, err
	}
	if root := FindProjectRoot(cwd); root != "" {
		c.ProjectRoot = root
		for _, p := range ProjectCandidates(root) {
			if fileExists(p) {
				c.ProjectPath = p
				break
			}
		}
		if c.ProjectPath != "" {
			if err := readFile(c.ProjectPath, &c.Project); err != nil {
				return nil, err
			}
			if c.Project.UserIdentity != "" {
				c.Warnings = append(c.Warnings, fmt.Sprintf(
					"ignoring userIdentity in project config %s; it is user-specific and belongs in the global config", c.ProjectPath))
				c.Project.UserIdentity = ""
			}
		}
	}
	return c, nil
}

// GlobalCandidates lists global config locations in priority order.
func GlobalCandidates(env func(string) string, home string) []string {
	var out []string
	if xdg := env(EnvXDGConfigHome); xdg != "" && filepath.IsAbs(xdg) {
		out = append(out, filepath.Join(xdg, toolDir, fileName))
	}
	return append(out, filepath.Join(home, ".config", toolDir, fileName))
}

// ResolveGlobalPath returns the first existing global config, or the preferred location.
func ResolveGlobalPath(env func(string) string, home string, exists func(string) bool) string {
	c := GlobalCandidates(env, home)
	for _, p := range c {
		if exists(p) {
			return p
		}
	}
	return c[0]
}

// FindProjectRoot walks up from dir to the nearest directory containing .git.
func FindProjectRoot(dir string) string {
	dir, _ = filepath.Abs(dir)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ProjectCandidates lists project config locations under a root in priority order. The first
// one is where a new project config is created.
func ProjectCandidates(root string) []string {
	return []string{
		filepath.Join(root, ".config", projectName),
		filepath.Join(root, "."+projectName),
	}
}

// Scope selects which file a write goes to.
type Scope int

// Write scopes.
const (
	// ScopeAuto writes to the project file inside a repository, otherwise to the global file.
	ScopeAuto Scope = iota
	ScopeGlobal
	ScopeProject
)

// Value is an effective configuration value and the file it came from.
type Value struct {
	Value  string
	Source string // file path, "env", "default", or "" when unset
}

// Get returns the effective value of a key.
func (c *Config) Get(name string) (Value, error) {
	k, err := LookupKey(name)
	if err != nil {
		return Value{}, err
	}
	if k.Name == "collectionUrl" {
		if v := c.getenv(EnvCollection); v != "" {
			return Value{v, "env " + EnvCollection}, nil
		}
	}
	if !k.GlobalOnly {
		if v := k.get(&c.Project); v != "" {
			return Value{v, c.ProjectPath}, nil
		}
	}
	if v := k.get(&c.Global); v != "" {
		return Value{v, c.GlobalPath}, nil
	}
	if k.Default != "" {
		return Value{k.Default, "default"}, nil
	}
	return Value{}, nil
}

// String returns the effective value of a key, or "".
func (c *Config) String(name string) string {
	v, _ := c.Get(name)
	return v.Value
}

// List returns the effective value of a list key.
func (c *Config) List(name string) []string { return splitList(c.String(name)) }

// TargetPath returns the file a write with the given scope would modify.
func (c *Config) TargetPath(name string, scope Scope) (string, error) {
	k, err := LookupKey(name)
	if err != nil {
		return "", err
	}
	if k.GlobalOnly || scope == ScopeGlobal {
		return c.GlobalPath, nil
	}
	if c.ProjectRoot == "" {
		if scope == ScopeProject {
			return "", errors.New("not inside a git repository; cannot write a project config")
		}
		return c.GlobalPath, nil
	}
	if c.ProjectPath != "" {
		return c.ProjectPath, nil
	}
	return ProjectCandidates(c.ProjectRoot)[0], nil
}

// Set writes a key and returns the path written. An empty value removes the key.
func (c *Config) Set(name, value string, scope Scope) (string, error) {
	k, err := LookupKey(name)
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value != "" && k.Validate != nil {
		if err := k.Validate(value); err != nil {
			return "", err
		}
	}
	path, err := c.TargetPath(name, scope)
	if err != nil {
		return "", err
	}
	f := &c.Global
	if path != c.GlobalPath {
		f = &c.Project
		c.ProjectPath = path
	}
	k.set(f, value)
	return path, writeFile(path, f)
}

// Connection is everything needed to talk to the server.
type Connection struct {
	CollectionURL string
	APIVersion    string
	PAT           string
}

// Connection validates and returns the server connection settings.
func (c *Config) Connection() (Connection, error) {
	raw := c.String("collectionUrl")
	if raw == "" {
		return Connection{}, fmt.Errorf("no collection URL configured; run `tfs config wizard` or `tfs config set collectionUrl <url>`")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return Connection{}, fmt.Errorf("invalid collection URL %q", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !truthy(c.getenv(EnvAllowInsecure)) {
			return Connection{}, fmt.Errorf("refusing to send the personal access token over http; use https, or set %s=1 (development only)", EnvAllowInsecure)
		}
	default:
		return Connection{}, fmt.Errorf("unsupported collection URL scheme %q; only https is supported", u.Scheme)
	}
	pat := strings.TrimSpace(c.getenv(EnvPAT))
	if pat == "" {
		return Connection{}, fmt.Errorf("no personal access token; set the %s environment variable", EnvPAT)
	}
	return Connection{
		CollectionURL: strings.TrimRight(raw, "/"),
		APIVersion:    c.String("apiVersion"),
		PAT:           pat,
	}, nil
}

func (c *Config) getenv(k string) string {
	if c.env == nil {
		return os.Getenv(k)
	}
	return c.env(k)
}

// ValidateCollectionURL checks that a collection URL is an absolute http(s) URL.
func ValidateCollectionURL(v string) error {
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("collection URL must be an absolute https URL, e.g. https://ado.example.com/DefaultCollection")
	}
	return nil
}

func truthy(v string) bool {
	return slices.Contains([]string{"1", "true", "yes"}, strings.ToLower(strings.TrimSpace(v)))
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func readFile(path string, into *File) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("could not parse config file %s: %w", path, err)
	}
	return nil
}

func writeFile(path string, f *File) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tfs-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
