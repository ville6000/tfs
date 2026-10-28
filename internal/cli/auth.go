package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/ville6000/tfs/internal/ado"
	"github.com/ville6000/tfs/internal/config"
	"github.com/ville6000/tfs/internal/keychain"
)

func (a *App) authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Store the personal access token in the macOS Keychain",
		Long: `Store the personal access token in the macOS Keychain.

The token is stored per server (scheme://host[:port]) and only sent to that server. The
Keychain item trusts only the tfs binary that stored it, so other programs, and tfs after an
upgrade, need your approval to read it. tfs never prints the token.

The TFS_PAT environment variable, when set, takes precedence over the Keychain.`,
	}
	cmd.AddCommand(a.authLoginCmd(), a.authStatusCmd(), a.authLogoutCmd())
	return cmd
}

// authTarget returns the collection URL and origin that login and logout act on: --url, or the
// global collectionUrl. Project config and TFS_COLLECTION_URL are ignored so that a repository
// can't make you store the token for a different server.
func (a *App) authTarget(flagURL string) (string, string, error) {
	raw := strings.TrimSpace(flagURL)
	if raw == "" {
		cfg, err := a.config()
		if err != nil {
			return "", "", err
		}
		raw = strings.TrimSpace(cfg.Global.CollectionURL)
	}
	if raw == "" {
		return "", "", errors.New("no collection URL: pass --url, or set one with `tfs config set collectionUrl <url> --global`")
	}
	if err := config.ValidateCollectionURL(raw); err != nil {
		return "", "", err
	}
	origin, err := config.Origin(raw)
	if err != nil {
		return "", "", err
	}
	if strings.HasPrefix(origin, "http://") {
		cfg, err := a.config()
		if err != nil {
			return "", "", err
		}
		if !cfg.AllowInsecure() {
			return "", "", fmt.Errorf("refusing to use the personal access token over http; use https, or set %s=1 (development only)", config.EnvAllowInsecure)
		}
	}
	return strings.TrimRight(raw, "/"), origin, nil
}

func (a *App) authLoginCmd() *cobra.Command {
	var flagURL string
	var noVerify bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store a personal access token in the Keychain",
		Long: `Store a personal access token in the Keychain.

The token is read from standard input, or prompted for (hidden) in a terminal. It is checked
against the server before it is stored, unless --no-verify is given.`,
		Example: `  tfs auth login
  bw get notes TFS_PAT | tfs auth login
  tfs auth login --url https://ado.example.com/DefaultCollection`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			collection, origin, err := a.authTarget(flagURL)
			if err != nil {
				return err
			}
			token, err := a.readToken(origin)
			if err != nil {
				return err
			}
			who := ""
			if !noVerify {
				cfg, err := a.config()
				if err != nil {
					return err
				}
				user, err := ado.New(collection, cfg.String("apiVersion"), token).CurrentUser(cmd.Context())
				if err != nil {
					return fmt.Errorf("the token was not stored: %w", err)
				}
				who = " (signed in as " + user + ")"
			}
			if err := keychain.Set(origin, token); err != nil {
				return err
			}
			a.printf("Stored the token for %s in the Keychain%s\n", origin, who)
			return nil
		},
	}
	cmd.Flags().StringVar(&flagURL, "url", "", "collection URL (default: the global collectionUrl)")
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "store the token without checking it against the server")
	return cmd
}

func (a *App) readToken(origin string) (string, error) {
	in := a.in
	if in == nil {
		in = os.Stdin
	}
	var raw []byte
	var err error
	if f, ok := in.(*os.File); ok && term.IsTerminal(f.Fd()) {
		fmt.Fprintf(a.errOut, "Personal access token for %s: ", origin)
		raw, err = term.ReadPassword(f.Fd())
		fmt.Fprintln(a.errOut)
	} else {
		raw, err = io.ReadAll(io.LimitReader(in, 64<<10))
	}
	if err != nil {
		return "", fmt.Errorf("could not read the token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", errors.New("no token given")
	}
	return token, nil
}

func (a *App) authStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show where the token for the current collection comes from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			raw := cfg.String("collectionUrl")
			if raw == "" {
				return errors.New("no collection URL configured; run `tfs config wizard`")
			}
			origin, err := config.Origin(raw)
			if err != nil {
				return err
			}
			a.printf("server: %s\n", origin)
			a.printf("token:  %s\n", tokenSource(origin))
			return nil
		},
	}
}

// tokenSource describes where the token for origin comes from, without reading it.
func tokenSource(origin string) string {
	if os.Getenv(config.EnvPAT) != "" {
		return config.EnvPAT + " environment variable"
	}
	ok, err := keychain.Exists(origin)
	switch {
	case errors.Is(err, keychain.ErrUnsupported):
		return "not set (set " + config.EnvPAT + ")"
	case err != nil:
		return "unknown (" + err.Error() + ")"
	case ok:
		return "Keychain"
	}
	return "not set (run `tfs auth login`)"
}

func (a *App) authLogoutCmd() *cobra.Command {
	var flagURL string
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored token from the Keychain",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, origin, err := a.authTarget(flagURL)
			if err != nil {
				return err
			}
			if err := keychain.Delete(origin); err != nil {
				if errors.Is(err, keychain.ErrNotFound) {
					a.printf("No token stored for %s\n", origin)
					return nil
				}
				return err
			}
			a.printf("Removed the token for %s from the Keychain\n", origin)
			return nil
		},
	}
	cmd.Flags().StringVar(&flagURL, "url", "", "collection URL (default: the global collectionUrl)")
	return cmd
}
