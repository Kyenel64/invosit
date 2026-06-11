package cmd

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/kyenel64/invosit/cli/internal/config"
	"github.com/kyenel64/invosit/cli/internal/syncstate"
)

// loadProjectConfig returns the closest invosit config and the abs path of project root
func loadProjectConfig() (*config.Config, string, error) {
	configPath, err := config.Find(".")
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, "", errors.New("no .invosit.json found. run `invosit init` first")
		}
		return nil, "", fmt.Errorf("failed to find config: %w", err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load config: %w", err)
	}
	return cfg, filepath.Dir(configPath), nil
}

// resolveEnvName returns the target environment: the --env flag value when
// set, otherwise the config's defaultEnvironment.
func resolveEnvName(flagValue string, cfg *config.Config) (string, error) {
	envName := flagValue
	if envName == "" {
		envName = cfg.DefaultEnvironment
	}
	if envName == "" {
		return "", errors.New("no environment set. pass --env <name> or set defaultEnvironment in .invosit.json")
	}
	return envName, nil
}

func loadSyncState(stderr io.Writer, projectRoot, workspaceID string) *syncstate.State {
	state, err := syncstate.Load(projectRoot)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: %v\n", err)
	}
	if !state.BoundTo(workspaceID) {
		if state.WorkspaceID != "" {
			_, _ = fmt.Fprintf(stderr, "warning: ignoring sync state bound to workspace %s\n", state.WorkspaceID)
		}
		return syncstate.New(workspaceID)
	}
	return state
}

func saveSyncState(stderr io.Writer, projectRoot string, state *syncstate.State) {
	if !state.Dirty() {
		return
	}
	if err := syncstate.Save(projectRoot, state); err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: %v\n", err)
	}
}
