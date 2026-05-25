package cmd

import (
	"errors"
	"fmt"

	"github.com/kyenel64/invosit/cli/internal/config"
)

// loadProjectConfig returns the closest .invosit.json file from cwd, and its path
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
	return cfg, configPath, nil
}
