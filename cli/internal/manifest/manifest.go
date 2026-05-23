package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

const (
	Version     = 1
	DefaultName = ".invosit.yaml"

	workspaceIDPrefix = "ws_"
)

type Manifest struct {
	Version     int    `yaml:"version"`
	WorkspaceID string `yaml:"workspace_id"`
	Environment string `yaml:"environment"`
}

var ErrNotFound = errors.New("invosit manifest not found")

func Load(path string) (*Manifest, error) {
	f, err := os.Open(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrNotFound, path)
	}
	if err != nil {
		return nil, fmt.Errorf("open manifest %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var manifest Manifest
	decoder := yaml.NewDecoder(f, yaml.DisallowUnknownField())
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode manifest %s: %w", path, err)
	}

	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest %s: %w", path, err)
	}
	return &manifest, nil
}

func Save(path string, manifest *Manifest) error {
	if manifest == nil {
		return errors.New("save: manifest is nil")
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("save: %w", err)
	}

	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil { //nolint:gosec // manifest is committed to git, not secret
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

func (m *Manifest) Validate() error {
	if m.Version != Version {
		return fmt.Errorf("version must be %d, got %d", Version, m.Version)
	}
	if !strings.HasPrefix(m.WorkspaceID, workspaceIDPrefix) {
		return fmt.Errorf("workspace_id must start with %q", workspaceIDPrefix)
	}
	if m.Environment == "" {
		return errors.New("environment must not be empty")
	}
	return nil
}
