package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

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
	Short: "Create a new environment in a workspace",
	Args:  cobra.ExactArgs(1),
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
			switch {
			case errors.Is(err, apiclient.ErrUnauthorized):
				return errors.New("not logged in or session expired. run `invosit login` to authenticate")
			case errors.Is(err, apiclient.ErrForbidden):
				return errors.New("you need the admin role on this workspace to create environments")
			case errors.Is(err, apiclient.ErrConflict):
				return fmt.Errorf("environment %q already exists in workspace %q", name, workspace.Name)
			case errors.Is(err, apiclient.ErrInvalidRequest):
				return errors.New("invalid environment name (names cannot start with 'env_')")
			default:
				return err
			}
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
			return err
		}

		out := cmd.OutOrStdout()
		if len(envs) == 0 {
			_, _ = fmt.Fprintf(out, "No environments in workspace %q\n", workspace.Name)
			return nil
		}

		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, env := range envs {
			_, _ = fmt.Fprintf(tw, "%s\t%s\n", env.ID, env.Name)
		}
		return tw.Flush()
	},
}

// resolveWorkspace picks the target workspace from, in order: the --workspace
// flag (matched by ID or name), the workspaceId bound in .invosit.json, or an
// interactive picker. It fetches the user's workspaces once to resolve the name
// for output and to confirm access.
func resolveWorkspace(ctx context.Context, client *apiclient.Client, token, flag string) (*apiclient.Workspace, error) {
	workspaces, err := client.GetWorkspaces(ctx, token)
	if err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return nil, errors.New("not logged in or session expired. run `invosit login` to authenticate")
		}
		return nil, err
	}

	if flag != "" {
		for i := range workspaces {
			if workspaces[i].ID == flag || workspaces[i].Name == flag {
				return &workspaces[i], nil
			}
		}
		return nil, fmt.Errorf("workspace %q not found or you don't have access", flag)
	}

	if cfg, _, err := loadProjectConfig(); err == nil && cfg.WorkspaceID != "" {
		for i := range workspaces {
			if workspaces[i].ID == cfg.WorkspaceID {
				return &workspaces[i], nil
			}
		}
		return nil, fmt.Errorf("workspace %q from %s not found or you don't have access", cfg.WorkspaceID, config.FileName)
	}

	return pickWorkspace(workspaces)
}

func init() {
	environmentCreateCmd.Flags().StringVarP(&environmentCreateWorkspace, "workspace", "w", "", "Workspace ID or name (defaults to .invosit.json, else interactive picker)")
	environmentListCmd.Flags().StringVarP(&environmentListWorkspace, "workspace", "w", "", "Workspace ID or name (defaults to .invosit.json, else interactive picker)")
	environmentCmd.AddCommand(environmentCreateCmd)
	environmentCmd.AddCommand(environmentListCmd)
	rootCmd.AddCommand(environmentCmd)
}
