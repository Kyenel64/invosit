package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/config"
	"github.com/spf13/cobra"
)

var environmentCmd = &cobra.Command{
	Use:     "environment",
	Aliases: []string{"env"},
	Short:   "Manage environments",
}

var environmentCreateWorkspace string

var environmentCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Creates a new environment in a workspace",
	Long: `Creates a new environment in a workspace.
Uses the currently active workspace.
Optionally pass workspace ID with --workspace flag
	`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		if name == "" {
			return errors.New("environment name cannot be empty")
		}

		creds, err := loadCredentials()
		if err != nil {
			return err
		}

		client := apiclient.NewClient(creds.APIURL)
		workspace, err := resolveWorkspace(cmd.Context(), client, creds.SessionToken, environmentCreateWorkspace)
		if err != nil {
			return err
		}

		env, err := client.CreateEnvironment(cmd.Context(), creds.SessionToken, workspace.ID, name)
		if err != nil {
			if errors.Is(err, apiclient.ErrUnauthorized) {
				return errors.New("not logged in or session expired. run `invosit login` to authenticate")
			}
			return fmt.Errorf("failed to create environment: %w", err)
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created environment %q (%s) in workspace %q\n", name, env.ID, workspace.Name)
		return nil
	},
}

var environmentListWorkspace string

var environmentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List environments in a workspace",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := loadCredentials()
		if err != nil {
			return err
		}

		client := apiclient.NewClient(creds.APIURL)
		workspace, err := resolveWorkspace(cmd.Context(), client, creds.SessionToken, environmentListWorkspace)
		if err != nil {
			return err
		}

		envs, err := client.GetEnvironments(cmd.Context(), creds.SessionToken, workspace.ID)
		if err != nil {
			if errors.Is(err, apiclient.ErrUnauthorized) {
				return errors.New("not logged in or session expired. run `invosit login` to authenticate")
			}
			return fmt.Errorf("failed to list environments: %w", err)
		}

		out := cmd.OutOrStdout()
		if len(envs) == 0 {
			_, _ = fmt.Fprintf(out, "No environments in workspace %q\n", workspace.Name)
			return nil
		}

		rows := make([][]string, 0, len(envs))
		for _, env := range envs {
			rows = append(rows, []string{
				env.ID,
				env.Name,
				env.CreatedAt.Local().Format("2006-01-02 15:04"),
			})
		}

		headerStyle := lipgloss.NewStyle().Bold(true).Padding(0, 1)
		cellStyle := lipgloss.NewStyle().Padding(0, 1)
		environmentTable := table.New().
			Border(lipgloss.NormalBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
			Headers("ID", "NAME", "CREATED").
			Rows(rows...).
			StyleFunc(func(row, _ int) lipgloss.Style {
				if row == table.HeaderRow {
					return headerStyle
				}
				return cellStyle
			})

		_, _ = fmt.Fprintln(out, environmentTable)
		return nil
	},
}

// resolveWorkspace picks the target workspace from either the --workspace, -w flag
// or the workspaceId bound in .invosit.json
func resolveWorkspace(ctx context.Context, client *apiclient.Client, token, flag string) (*apiclient.Workspace, error) {
	workspaces, err := client.GetWorkspaces(ctx, token)
	if err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return nil, errors.New("not logged in or session expired. run `invosit login` to authenticate")
		}
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	if flag != "" {
		for i := range workspaces {
			if workspaces[i].ID == flag {
				return &workspaces[i], nil
			}
		}
		return nil, fmt.Errorf("workspace %q not found or you don't have access", flag)
	}

	cfg, _, err := loadProjectConfig()
	if err != nil {
		return nil, errors.New("no workspace specified. pass --workspace or run `invosit init` to bind one")
	}
	if cfg.WorkspaceID == "" {
		return nil, fmt.Errorf("no workspaceId in %s. pass --workspace or re-run `invosit init`", config.FileName)
	}
	for i := range workspaces {
		if workspaces[i].ID == cfg.WorkspaceID {
			return &workspaces[i], nil
		}
	}
	return nil, fmt.Errorf("workspace %q from %s not found or you don't have access", cfg.WorkspaceID, config.FileName)
}

func init() {
	environmentCreateCmd.Flags().StringVarP(&environmentCreateWorkspace, "workspace", "w", "", "Workspace ID (defaults to .invosit.json)")
	environmentListCmd.Flags().StringVarP(&environmentListWorkspace, "workspace", "w", "", "Workspace ID (defaults to .invosit.json)")
	environmentCmd.AddCommand(environmentCreateCmd)
	environmentCmd.AddCommand(environmentListCmd)
	rootCmd.AddCommand(environmentCmd)
}
