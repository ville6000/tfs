package config

import (
	"os"
	"path/filepath"
	"testing"
)

func envMap(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestGlobalCandidates(t *testing.T) {
	got := GlobalCandidates(envMap(map[string]string{EnvXDGConfigHome: "/x"}), "/h")
	if len(got) != 2 || got[0] != "/x/tfs/config.json" || got[1] != "/h/.config/tfs/config.json" {
		t.Errorf("got %v", got)
	}
	got = GlobalCandidates(envMap(map[string]string{EnvXDGConfigHome: "rel"}), "/h")
	if len(got) != 1 {
		t.Errorf("relative XDG must be ignored: %v", got)
	}
}

func setup(t *testing.T) (home, repo, sub string) {
	t.Helper()
	tmp := t.TempDir()
	home = filepath.Join(tmp, "home")
	repo = filepath.Join(tmp, "repo")
	sub = filepath.Join(repo, "a", "b")
	for _, d := range []string{home, filepath.Join(repo, ".git"), sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func TestLayeringAndWrites(t *testing.T) {
	home, repo, sub := setup(t)
	env := envMap(map[string]string{EnvPAT: "secret"})

	c, err := LoadFrom(sub, home, env)
	if err != nil {
		t.Fatal(err)
	}
	if c.ProjectRoot != repo {
		t.Fatalf("root = %q", c.ProjectRoot)
	}
	if _, err := c.Set("collectionUrl", "https://global.example.com/Col", ScopeGlobal); err != nil {
		t.Fatal(err)
	}
	path, err := c.Set("apiVersion", "7.0", ScopeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(repo, ".config", "tfs.json") {
		t.Errorf("auto write went to %s", path)
	}
	if p, _ := c.Set("userIdentity", "me@example.com", ScopeAuto); p != c.GlobalPath {
		t.Errorf("userIdentity must be global, went to %s", p)
	}

	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v", st.Mode().Perm(), err)
	}

	c, err = LoadFrom(sub, home, env)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Get("apiVersion"); v.Value != "7.0" || v.Source != path {
		t.Errorf("apiVersion = %+v", v)
	}
	if v, _ := c.Get("collectionUrl"); v.Source != c.GlobalPath {
		t.Errorf("collectionUrl = %+v", v)
	}
	if v, _ := c.Get("remote"); v.Value != "origin" || v.Source != "default" {
		t.Errorf("remote = %+v", v)
	}
	conn, err := c.Connection()
	if err != nil || conn.APIVersion != "7.0" || conn.PAT != "secret" {
		t.Errorf("conn = %+v, %v", conn, err)
	}

	// clearing removes the project override
	if _, err := c.Set("apiVersion", "", ScopeAuto); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Get("apiVersion"); v.Value != DefaultAPIVersion {
		t.Errorf("after unset: %+v", v)
	}
}

func TestProjectUserIdentityIgnored(t *testing.T) {
	home, repo, _ := setup(t)
	if err := os.WriteFile(filepath.Join(repo, ".tfs.json"), []byte(`{"UserIdentity":"x","DefaultProject":"P"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(repo, home, envMap(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.String("userIdentity") != "" || len(c.Warnings) != 1 {
		t.Errorf("identity leaked: %q %v", c.String("userIdentity"), c.Warnings)
	}
	if c.String("defaultProject") != "P" {
		t.Error("case-insensitive read failed")
	}
}

func TestConnectionSecurity(t *testing.T) {
	home, _, _ := setup(t)
	outside := t.TempDir()
	c, _ := LoadFrom(outside, home, envMap(map[string]string{EnvCollection: "http://h/Col", EnvPAT: "p"}))
	if _, err := c.Connection(); err == nil {
		t.Error("http must be refused")
	}
	c, _ = LoadFrom(outside, home, envMap(map[string]string{EnvCollection: "http://h/Col", EnvPAT: "p", EnvAllowInsecure: "1"}))
	if _, err := c.Connection(); err != nil {
		t.Errorf("insecure opt-in: %v", err)
	}
	c, _ = LoadFrom(outside, home, envMap(map[string]string{EnvCollection: "https://h/Col"}))
	if _, err := c.Connection(); err == nil {
		t.Error("missing PAT must fail")
	}
	if _, err := c.Set("defaultProject", "x", ScopeProject); err == nil {
		t.Error("project scope outside repo must fail")
	}
}

func TestValidation(t *testing.T) {
	home, _, _ := setup(t)
	c, _ := LoadFrom(t.TempDir(), home, envMap(nil))
	if _, err := c.Set("collectionUrl", "not a url", ScopeGlobal); err == nil {
		t.Error("expected url validation error")
	}
	if _, err := c.Set("branchTemplate", "feature/{slug}", ScopeGlobal); err == nil {
		t.Error("expected template validation error")
	}
	if _, err := c.Set("nope", "x", ScopeGlobal); err == nil {
		t.Error("expected unknown key error")
	}
	if _, err := c.Set("requireParentFor", "user-story, Bug", ScopeGlobal); err != nil {
		t.Fatal(err)
	}
	if got := c.List("requireParentFor"); len(got) != 2 || got[1] != "Bug" {
		t.Errorf("list = %v", got)
	}
}
