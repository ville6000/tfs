package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/huh/v2"

	"github.com/ville6000/tfs/internal/config"
)

// ErrAborted is returned when the user cancels a form or picker.
var ErrAborted = errors.New("aborted")

func runForm(f *huh.Form) error {
	err := f.WithTheme(huh.ThemeFunc(huh.ThemeCharm)).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrAborted
	}
	return err
}

// WizardValues are the values edited by the config wizard: what the file being written sets each
// key to, or "" for a key it leaves out.
type WizardValues struct {
	CollectionURL  string
	DefaultProject string
	APIVersion     string
	UserIdentity   string
	BranchTemplate string
}

// WizardOptions configure the config wizard.
type WizardOptions struct {
	// Path is the config file the wizard writes.
	Path string
	// Project is set when Path is a repository's project config. userIdentity is global only, so
	// it isn't asked then.
	Project bool
	// Fallback is what each key falls back to when left empty: the global value or the built-in
	// default. Fields show it as their placeholder.
	Fallback WizardValues
}

// AskWizardScope asks whether the config wizard writes the repository's project config or the
// global one. project holds the preselected answer and receives the choice.
func AskWizardScope(project *bool, projectPath, globalPath string) error {
	return runForm(huh.NewForm(huh.NewGroup(
		huh.NewSelect[bool]().Title("Configure").
			Description("A repository config holds only what differs from the global one, and can be committed").
			Options(
				huh.NewOption("This repository  "+shortPath(projectPath), true),
				huh.NewOption("Global  "+shortPath(globalPath), false),
			).
			Value(project),
	)))
}

// RunWizard edits v in place.
func RunWizard(v *WizardValues, opts WizardOptions) error {
	fb := opts.Fallback
	fields := []huh.Field{
		huh.NewInput().Title("Collection URL").
			Description("e.g. https://ado.example.com/DefaultCollection").
			Placeholder(fb.CollectionURL).Value(&v.CollectionURL).
			Validate(orFallback(fb.CollectionURL, config.ValidateCollectionURL)),
		huh.NewInput().Title("Default project").Description("Used when --project is omitted (optional)").
			Placeholder(fb.DefaultProject).Value(&v.DefaultProject),
		huh.NewInput().Title("API version").Description("6.0 works with Azure DevOps Server 2020+; use 5.0 for 2019").
			Placeholder(fb.APIVersion).Value(&v.APIVersion).
			Validate(orFallback(fb.APIVersion, func(string) error { return nil })),
		huh.NewInput().Title("Branch template for `wi worktree`").Description("Placeholders: {id} {slug} {type}").
			Placeholder(fb.BranchTemplate).Value(&v.BranchTemplate).
			Validate(orFallback(fb.BranchTemplate, func(s string) error {
				if !strings.Contains(s, "{id}") {
					return errors.New("must contain {id}")
				}
				return nil
			})),
	}
	intro := "Leave a field empty to use its greyed-out default."
	if opts.Project {
		intro = "Fill in only what this repository overrides. Empty fields use the greyed-out global value or default."
	} else {
		fields = append(fields, huh.NewInput().Title("Your identity for @me (optional)").
			Description("Unique name or email; leave empty to ask the server").Value(&v.UserIdentity))
	}
	return runForm(huh.NewForm(huh.NewGroup(fields...).Title("Saving to " + shortPath(opts.Path)).Description(intro)))
}

// orFallback validates a non-empty value, and accepts an empty one when there is a fallback.
func orFallback(fallback string, validate func(string) error) func(string) error {
	return func(s string) error {
		if s = strings.TrimSpace(s); s != "" {
			return validate(s)
		}
		if fallback == "" {
			return errors.New("required")
		}
		return nil
	}
}

// shortPath shows p relative to the working directory when it is inside it, else with the home
// directory as ~.
func shortPath(p string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, p); err == nil && filepath.IsLocal(rel) {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// CreateValues are the values collected by the interactive create form.
type CreateValues struct {
	Type               string
	Title              string
	Description        string
	AcceptanceCriteria string
	ReproSteps         string
	AssignToMe         bool
	Tags               string
	Parent             string
}

// RunCreateForm edits v in place. types are offered as choices when non-empty.
func RunCreateForm(v *CreateValues, types []string) error {
	var typeField huh.Field
	if len(types) > 0 {
		typeField = huh.NewSelect[string]().Title("Type").Options(huh.NewOptions(types...)...).Value(&v.Type)
	} else {
		typeField = huh.NewInput().Title("Type").Value(&v.Type)
	}
	return runForm(huh.NewForm(huh.NewGroup(
		typeField,
		huh.NewInput().Title("Title").Value(&v.Title).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("title is required")
			}
			return nil
		}),
		huh.NewText().Title("Description").Description("Plain text (optional)").Value(&v.Description),
		huh.NewText().Title("Acceptance criteria").
			Description("Plain text (optional; only types that have it, such as User Story or Product Backlog Item)").
			Lines(3).Value(&v.AcceptanceCriteria),
		huh.NewText().Title("Repro steps").Description("Plain text (optional; Bugs)").Lines(3).Value(&v.ReproSteps),
		huh.NewInput().Title("Parent id").Description("Optional").Value(&v.Parent).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return nil
			}
			if n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(s), "#")); err != nil || n <= 0 {
				return errors.New("must be a positive work item id")
			}
			return nil
		}),
		huh.NewInput().Title("Tags").Description("Comma-separated (optional)").Value(&v.Tags),
		huh.NewConfirm().Title("Assign to me?").Value(&v.AssignToMe),
	)))
}

// Pick runs a picker screen and returns the chosen id, or ErrAborted.
func Pick(screen Screen, picked *int) (int, error) {
	if err := Run(screen); err != nil {
		return 0, err
	}
	if *picked == 0 {
		return 0, ErrAborted
	}
	return *picked, nil
}

// PromptText asks for multi-line text in a form.
func PromptText(title string) (string, error) {
	var text string
	err := runForm(huh.NewForm(huh.NewGroup(
		huh.NewText().Title(title).Value(&text).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("must not be empty")
			}
			return nil
		}),
	)))
	return text, err
}

// ConfirmAction asks a yes/no question that defaults to no.
func ConfirmAction(question, detail, yes string) (bool, error) {
	var ok bool
	err := runForm(huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(question).Description(detail).Affirmative(yes).Negative("Cancel").Value(&ok),
	)))
	return ok, err
}
