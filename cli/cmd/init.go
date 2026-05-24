package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/config"
	"github.com/kyenel64/invosit/cli/internal/tui"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize an invosit workspace",
	Long:  ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := loadCredentials()
		if err != nil {
			return err
		}

		if err := confirmOverwriteIfExists(config.FileName); err != nil {
			return err
		}

		apiClient := apiclient.NewClient(baseURL())
		workspace, err := chooseWorkspace(cmd.Context(), apiClient, creds.SessionToken)
		if err != nil {
			return err
		}

		defaultEnv, err := chooseDefaultEnvironment(cmd.Context(), apiClient, creds.SessionToken, workspace.ID)
		if err != nil {
			return err
		}

		if err := config.Save(config.FileName, &config.Config{
			Version:            config.Version,
			WorkspaceID:        workspace.ID,
			DefaultEnvironment: defaultEnv,
		}); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		if defaultEnv == "" {
			fmt.Printf("Initialized %s (workspace: %s, no default environment — pass --env to push/pull)\n", config.FileName, workspace.Name)
		} else {
			fmt.Printf("Initialized %s (workspace: %s, default environment: %s)\n", config.FileName, workspace.Name, defaultEnv)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func confirmOverwriteIfExists(path string) error {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	choice, err := tui.Select(
		fmt.Sprintf("%s already exists. Overwrite?", path),
		[]string{"No, cancel", "Yes, overwrite"},
	)
	if err != nil {
		if errors.Is(err, tui.ErrCancelled) {
			return errors.New("init cancelled")
		}
		return err
	}
	if choice == 0 {
		return errors.New("init cancelled")
	}
	return nil
}

func chooseWorkspace(ctx context.Context, client *apiclient.Client, token string) (*apiclient.Workspace, error) {
	workspaces, err := client.GetWorkspaces(ctx, token)
	if err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return nil, errors.New("not logged in or session expired. run `invosit login` to authenticate")
		}
		return nil, err
	}
	if len(workspaces) == 0 {
		return nil, errors.New("you don't have access to any workspaces")
	}

	labels := make([]string, len(workspaces))
	for i, workspace := range workspaces {
		labels[i] = workspace.Name
	}
	selectedIdx, err := tui.Select("Select a workspace:", labels)
	if err != nil {
		return nil, err
	}
	return &workspaces[selectedIdx], nil
}

func chooseDefaultEnvironment(ctx context.Context, client *apiclient.Client, token string, workspaceID string) (string, error) {
	environments, err := client.GetEnvironments(ctx, token, workspaceID)
	if err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return "", errors.New("not logged in or session expired. run `invosit login` to authenticate")
		}
		return "", err
	}
	if len(environments) == 0 {
		return "", fmt.Errorf("you don't have access to any environments in workspace: %s", workspaceID)
	}

	const skipLabel = "Skip (no default)"
	labels := make([]string, 0, len(environments)+1)
	labels = append(labels, skipLabel)
	for _, environment := range environments {
		labels = append(labels, environment.Name)
	}
	selectedIdx, err := tui.Select("Select a default environment (optional):", labels)
	if err != nil {
		return "", err
	}
	if selectedIdx == 0 {
		return "", nil
	}
	return environments[selectedIdx-1].Name, nil
}
