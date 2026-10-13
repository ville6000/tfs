package cli

import "github.com/spf13/cobra"

func (a *App) boardCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "board", Short: "Boards"}
	cmd.AddCommand(&cobra.Command{
		Use:   "open",
		Short: "Open the project's boards in the browser",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := a.api()
			if err != nil {
				return err
			}
			project, err := a.resolveProject(cmd.Context())
			if err != nil {
				return err
			}
			return a.openURL(client.BoardURL(project))
		},
	})
	return cmd
}
