package tui

import (
	"errors"
	"os"
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

// WizardValues are the values edited by the config wizard.
type WizardValues struct {
	CollectionURL  string
	DefaultProject string
	APIVersion     string
	UserIdentity   string
	BranchTemplate string
	SaveToProject  bool
}

// RunWizard edits v in place. inRepo enables the "save to project" choice.
func RunWizard(v *WizardValues, inRepo bool, projectPath, globalPath string) error {
	fields := []huh.Field{
		huh.NewInput().Title("Collection URL").
			Description("e.g. https://ado.example.com/DefaultCollection").
			Value(&v.CollectionURL).Validate(config.ValidateCollectionURL),
		huh.NewInput().Title("Default project").Description("Used when --project is omitted (optional)").
			Value(&v.DefaultProject),
		huh.NewInput().Title("API version").Description("6.0 works with Azure DevOps Server 2020+; use 5.0 for 2019").
			Value(&v.APIVersion).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("required")
			}
			return nil
		}),
		huh.NewInput().Title("Branch template for `wi worktree`").Description("Placeholders: {id} {slug} {type}").
			Value(&v.BranchTemplate).Validate(func(s string) error {
			if !strings.Contains(s, "{id}") {
				return errors.New("must contain {id}")
			}
			return nil
		}),
		huh.NewInput().Title("Your identity for @me (optional)").
			Description("Unique name or email; leave empty to ask the server").Value(&v.UserIdentity),
	}
	if inRepo {
		fields = append(fields, huh.NewSelect[bool]().Title("Save settings to").
			Description("userIdentity is always saved globally").
			Options(
				huh.NewOption("Global  "+shortenHome(globalPath), false),
				huh.NewOption("This repository  "+shortenHome(projectPath), true),
			).
			Value(&v.SaveToProject))
	}
	return runForm(huh.NewForm(huh.NewGroup(fields...)))
}

func shortenHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// CreateValues are the values collected by the interactive create form.
type CreateValues struct {
	Type        string
	Title       string
	Description string
	AssignToMe  bool
	Tags        string
	Parent      string
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
