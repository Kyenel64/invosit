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
	fileIDPrefix      = "file_"
)

type Manifest struct {
	Version     int    `yaml:"version"`
	WorkspaceID string `yaml:"workspace_id"`
	Environment string `yaml:"environment"`
	Files       []File `yaml:"files"`
}

type File struct {
	Path        string `yaml:"path"`
	FileID      string `yaml:"file_id"`
	ContentHash string `yaml:"content_hash"`
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

	seen := make(map[string]struct{}, len(m.Files))
	for i, f := range m.Files {
		if f.Path == "" {
			return fmt.Errorf("files[%d]: path must not be empty", i)
		}
		if !strings.HasPrefix(f.FileID, fileIDPrefix) {
			return fmt.Errorf("files[%d]: file_id must start with %q", i, fileIDPrefix)
		}
		if _, dup := seen[f.Path]; dup {
			return fmt.Errorf("files[%d]: duplicate path %q", i, f.Path)
		}
		seen[f.Path] = struct{}{}
	}
	return nil
}
