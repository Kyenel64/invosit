package cmd

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/kyenel64/invosit/cli/internal/config"
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
		if errors.Is(err, config.ErrUnsupportedVersion) {
			return nil, "", errors.New(".invosit.json is from an older invosit version. run `invosit init` to recreate it")
		}
		return nil, "", fmt.Errorf("failed to load config: %w", err)
	}
	return cfg, filepath.Dir(configPath), nil
}
