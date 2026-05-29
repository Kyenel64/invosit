package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/spf13/cobra"
)

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage workspaces",
}

var workspaceCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		if name == "" {
			return errors.New("workspace name cannot be empty")
		}

		creds, err := loadCredentials()
		if err != nil {
			return err
		}

		apiClient := apiclient.NewClient(creds.APIURL)
		workspace, err := apiClient.CreateWorkspace(cmd.Context(), creds.SessionToken, name)
		if err != nil {
			if errors.Is(err, apiclient.ErrUnauthorized) {
				return errors.New("not logged in or session expired. run `invosit login` to authenticate")
			}
			return fmt.Errorf("failed to create workspace: %w", err)
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created workspace %q (%s)\n", workspace.Name, workspace.ID)
		return nil
	},
}

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspaces you have access to",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := loadCredentials()
		if err != nil {
			return err
		}

		apiClient := apiclient.NewClient(creds.APIURL)
		workspaces, err := apiClient.GetWorkspaces(cmd.Context(), creds.SessionToken)
		if err != nil {
			if errors.Is(err, apiclient.ErrUnauthorized) {
				return errors.New("not logged in or session expired. run `invosit login` to authenticate")
			}
			return fmt.Errorf("failed to list workspaces: %w", err)
		}

		out := cmd.OutOrStdout()
		if len(workspaces) == 0 {
			_, _ = fmt.Fprintln(out, "No workspaces yet. Create one with `invosit workspace create <name>`")
			return nil
		}

		rows := make([][]string, 0, len(workspaces))
		for _, workspace := range workspaces {
			rows = append(rows, []string{
				workspace.ID,
				workspace.Name,
				workspace.Role,
				workspace.CreatedAt.Local().Format("2006-01-02 15:04"),
			})
		}

		headerStyle := lipgloss.NewStyle().Bold(true).Padding(0, 1)
		cellStyle := lipgloss.NewStyle().Padding(0, 1)
		workspaceTable := table.New().
			Border(lipgloss.NormalBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
			Headers("ID", "NAME", "ROLE", "CREATED").
			Rows(rows...).
			StyleFunc(func(row, _ int) lipgloss.Style {
				if row == table.HeaderRow {
					return headerStyle
				}
				return cellStyle
			})

		_, _ = fmt.Fprintln(out, workspaceTable)
		return nil
	},
}

func init() {
	workspaceCmd.AddCommand(workspaceCreateCmd, workspaceListCmd)
	rootCmd.AddCommand(workspaceCmd)
}
