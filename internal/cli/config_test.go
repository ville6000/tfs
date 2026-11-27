package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ville6000/tfs/internal/tui"
)

// wizardRun runs `tfs config wizard` with the scope question and the form replaced: ask answers
// the question (nil fails the test if it is asked) and edit changes the form's values.
func wizardRun(t *testing.T, ask func(project *bool) error, edit func(*tui.WizardValues, tui.WizardOptions), args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	app := &App{out: &out, errOut: &errOut,
		askWizardScope: func(project *bool, _, _ string) error {
			if ask == nil {
				t.Fatal("the wizard asked where to save")
			}
			return ask(project)
		},
		runWizard: func(v *tui.WizardValues, opts tui.WizardOptions) error {
			edit(v, opts)
			return nil
		},
	}
	root := app.rootCmd("test")
	root.SetArgs(append([]string{"config", "wizard"}, args...))
	err := root.Execute()
	return out.String(), err
}

func readJSON(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(b)), "")
}

func TestConfigWizardProjectOverride(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TFS_COLLECTION_URL", "")
	t.Setenv("TFS_PAT", "x")
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	globalPath := filepath.Join(home, ".config", "tfs", "config.json")
	projectPath := filepath.Join(repo, ".config", "tfs.json")
	global := `{"collectionUrl":"https://ado.example.com/Col","defaultProject":"Global","apiVersion":"6.0"}`

	// First run inside the repository, before any project config: set up the global config.
	out, err := wizardRun(t, func(project *bool) error {
		if *project {
			t.Error("without a global config the wizard should preselect it")
		}
		return nil
	}, func(v *tui.WizardValues, opts tui.WizardOptions) {
		if opts.Project || opts.Path != globalPath || opts.Fallback.APIVersion != "6.0" || v.APIVersion != "" {
			t.Errorf("global form = %+v, %+v", v, opts)
		}
		v.CollectionURL, v.DefaultProject, v.APIVersion = "https://ado.example.com/Col", "Global", " 6.0 "
	})
	if err != nil || !strings.Contains(out, "Saved "+globalPath) {
		t.Fatalf("global run: %v\n%s", err, out)
	}
	if got := readJSON(t, globalPath); got != global {
		t.Errorf("global config = %s", got)
	}

	// Now the repository is preselected, and only the value changed lands in its config.
	out, err = wizardRun(t, func(project *bool) error {
		if !*project {
			t.Error("with a global config the wizard should preselect the repository")
		}
		return nil
	}, func(v *tui.WizardValues, opts tui.WizardOptions) {
		if !opts.Project || opts.Path != projectPath || *v != (tui.WizardValues{UserIdentity: v.UserIdentity}) {
			t.Errorf("project form = %+v, %+v", v, opts)
		}
		if f := opts.Fallback; f.CollectionURL != "https://ado.example.com/Col" || f.DefaultProject != "Global" || f.BranchTemplate != "feature/{id}-{slug}" {
			t.Errorf("fallbacks = %+v", f)
		}
		v.DefaultProject = "Repo"
	})
	if err != nil || !strings.Contains(out, "Saved "+projectPath) || strings.Contains(out, globalPath) {
		t.Fatalf("project run: %v\n%s", err, out)
	}
	if got := readJSON(t, projectPath); got != `{"defaultProject":"Repo"}` {
		t.Errorf("project config = %s", got)
	}
	if got := readJSON(t, globalPath); got != global {
		t.Errorf("global config changed to %s", got)
	}
	if out, err := runCLI(t, "config", "get", "defaultProject"); err != nil || out != "Repo\n" {
		t.Errorf("defaultProject = %q, %v", out, err)
	}

	// An existing project config is preselected and prefilled; leaving it alone writes nothing.
	out, err = wizardRun(t, func(project *bool) error {
		if !*project {
			t.Error("an existing project config should be preselected")
		}
		return nil
	}, func(v *tui.WizardValues, _ tui.WizardOptions) {
		if v.DefaultProject != "Repo" {
			t.Errorf("prefilled defaultProject = %q", v.DefaultProject)
		}
	})
	if err != nil || !strings.Contains(out, "No changes") {
		t.Errorf("unchanged run: %v\n%s", err, out)
	}

	// --local skips the question; clearing a field removes the override.
	if _, err := wizardRun(t, nil, func(v *tui.WizardValues, _ tui.WizardOptions) {
		v.DefaultProject, v.BranchTemplate = "", "{type}/{id}"
	}, "--local"); err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, projectPath); got != `{"branchTemplate":"{type}/{id}"}` {
		t.Errorf("project config = %s", got)
	}

	// --global edits the global file's own values, whatever the repository overrides.
	if _, err := wizardRun(t, nil, func(v *tui.WizardValues, opts tui.WizardOptions) {
		if opts.Project || v.BranchTemplate != "" || v.DefaultProject != "Global" {
			t.Errorf("global form = %+v, %+v", v, opts)
		}
		v.UserIdentity = "me@example.com"
	}, "--global"); err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, globalPath); !strings.Contains(got, `"userIdentity":"me@example.com"`) {
		t.Errorf("global config = %s", got)
	}

	// --local needs a repository.
	t.Chdir(home)
	if _, err := wizardRun(t, nil, func(*tui.WizardValues, tui.WizardOptions) {
		t.Error("the form must not open outside a repository with --local")
	}, "--local"); err == nil {
		t.Error("--local outside a repository should fail")
	}
}
