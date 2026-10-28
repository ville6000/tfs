package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ville6000/tfs/internal/config"
	"github.com/ville6000/tfs/internal/output"
	"github.com/ville6000/tfs/internal/tui"
	"github.com/ville6000/tfs/internal/ui"
)

func keysHelp() string {
	var b strings.Builder
	b.WriteString("Keys:\n")
	for _, k := range config.Keys {
		def := ""
		if k.Default != "" {
			def = " (default " + k.Default + ")"
		}
		scope := ""
		if k.GlobalOnly {
			scope = " [global only]"
		}
		fmt.Fprintf(&b, "  %-17s %s%s%s\n", k.Name, k.Description, def, scope)
	}
	return b.String()
}

func (a *App) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show and change configuration",
		Long: `Show and change configuration.

The global config lives at $XDG_CONFIG_HOME/tfs/config.json or ~/.config/tfs/config.json.
Inside a git repository, a project config (.config/tfs.json or .tfs.json at the repository
root) is merged over it field by field; writes go there by default (use --global to override).
The personal access token is never written to these files: store it in the macOS Keychain
with 'tfs auth login', or set the TFS_PAT environment variable.

` + keysHelp(),
	}
	cmd.AddCommand(a.configListCmd(), a.configGetCmd(), a.configSetCmd(), a.configUnsetCmd(), a.configWizardCmd())
	return cmd
}

func scopeFlags(cmd *cobra.Command, global, project *bool) {
	cmd.Flags().BoolVarP(global, "global", "g", false, "write to the global config")
	cmd.Flags().BoolVar(project, "local", false, "write to the project config of the current repository")
	cmd.MarkFlagsMutuallyExclusive("global", "local")
}

func toScope(global, project bool) config.Scope {
	switch {
	case global:
		return config.ScopeGlobal
	case project:
		return config.ScopeProject
	}
	return config.ScopeAuto
}

func (a *App) configListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "show"},
		Short:   "Show every key with its effective value and source",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			if a.jsonOut {
				m := map[string]config.Value{}
				for _, k := range config.Keys {
					m[k.Name], _ = cfg.Get(k.Name)
				}
				return output.JSON(a.out, m)
			}
			rows := [][]output.Cell{}
			for _, k := range config.Keys {
				v, _ := cfg.Get(k.Name)
				rows = append(rows, []output.Cell{{Text: k.Name, Style: ui.Bold}, {Text: ui.OrDash(v.Value)}, {Text: v.Source, Style: ui.Muted}})
			}
			token := "not set"
			if origin, err := config.Origin(cfg.String("collectionUrl")); err == nil {
				token = tokenSource(origin)
			} else if os.Getenv(config.EnvPAT) != "" {
				token = tokenSource("")
			}
			rows = append(rows, []output.Cell{{Text: "token", Style: ui.Bold}, {Text: token}, {}})
			output.Table(a.out, []string{"KEY", "VALUE", "SOURCE"}, rows, 0)
			a.printf("\nglobal:  %s\n", cfg.GlobalPath)
			if cfg.ProjectRoot != "" {
				p := cfg.ProjectPath
				if p == "" {
					p, _ = cfg.TargetPath("defaultProject", config.ScopeProject)
					p += " (not created)"
				}
				a.printf("project: %s\n", p)
			}
			return nil
		},
	}
}

func (a *App) configGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print the effective value of a key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			v, err := cfg.Get(args[0])
			if err != nil {
				return err
			}
			if v.Value == "" {
				fmt.Fprintln(a.errOut, "(not set)")
				return nil
			}
			a.printf("%s\n", v.Value)
			if v.Source != "" {
				fmt.Fprintf(a.errOut, "source: %s\n", v.Source)
			}
			return nil
		},
	}
}

func (a *App) configSetCmd() *cobra.Command {
	var global, project bool
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a key",
		Example: `  tfs config set collectionUrl https://ado.example.com/DefaultCollection --global
  tfs config set defaultProject MyProject
  tfs config set requireParentFor "User Story,Task"`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			if strings.TrimSpace(args[1]) == "" {
				return fmt.Errorf("value must not be empty; use `tfs config unset %s`", args[0])
			}
			path, err := cfg.Set(args[0], args[1], toScope(global, project))
			if err != nil {
				return err
			}
			a.printf("Set %s in %s\n", args[0], path)
			return nil
		},
	}
	scopeFlags(cmd, &global, &project)
	return cmd
}

func (a *App) configUnsetCmd() *cobra.Command {
	var global, project bool
	cmd := &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove a key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			path, err := cfg.Set(args[0], "", toScope(global, project))
			if err != nil {
				return err
			}
			a.printf("Removed %s from %s\n", args[0], path)
			return nil
		},
	}
	scopeFlags(cmd, &global, &project)
	return cmd
}

func (a *App) configWizardCmd() *cobra.Command {
	var global bool
	cmd := &cobra.Command{
		Use:   "wizard",
		Short: "Set up the configuration interactively",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			v := tui.WizardValues{
				CollectionURL:  cfg.String("collectionUrl"),
				DefaultProject: cfg.String("defaultProject"),
				APIVersion:     cfg.String("apiVersion"),
				UserIdentity:   cfg.String("userIdentity"),
				BranchTemplate: cfg.String("branchTemplate"),
				SaveToProject:  cfg.ProjectPath != "",
			}
			inRepo := cfg.ProjectRoot != "" && !global
			projectPath, _ := cfg.TargetPath("defaultProject", config.ScopeAuto)
			if err := tui.RunWizard(&v, inRepo, projectPath, cfg.GlobalPath); err != nil {
				return err
			}
			scope := config.ScopeGlobal
			if inRepo && v.SaveToProject {
				scope = config.ScopeProject
			}
			written := map[string]bool{}
			for _, kv := range [][2]string{
				{"collectionUrl", v.CollectionURL}, {"defaultProject", v.DefaultProject},
				{"apiVersion", v.APIVersion}, {"branchTemplate", v.BranchTemplate}, {"userIdentity", v.UserIdentity},
			} {
				// Don't persist values that merely echo a built-in default.
				if k, _ := config.LookupKey(kv[0]); k.Default != "" && kv[1] == k.Default {
					if cur, _ := cfg.Get(kv[0]); cur.Source == "default" {
						continue
					}
				}
				path, err := cfg.Set(kv[0], kv[1], scope)
				if err != nil {
					return fmt.Errorf("%s: %w", kv[0], err)
				}
				written[path] = true
			}
			for p := range written {
				a.printf("Saved %s\n", p)
			}
			if os.Getenv(config.EnvPAT) == "" {
				a.printf("\nNext: create a personal access token (scopes: Work Items read & write, Code read)\nand store it in the Keychain:  tfs auth login  (or export %s=<token>)\n", config.EnvPAT)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&global, "global", "g", false, "save everything to the global config")
	return cmd
}
