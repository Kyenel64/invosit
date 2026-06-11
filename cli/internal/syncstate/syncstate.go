package syncstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	Version  = 1
	DirName  = ".invosit"
	FileName = "state.json"

	workspaceIDPrefix = "ws_"
)

var ErrRecovered = errors.New("failed to read sync state")

type FileRecord struct {
	FileID      string `json:"fileId"`
	Version     int64  `json:"version"`
	ContentHash string `json:"contentHash"`
}

type EnvState struct {
	EnvironmentID string                `json:"environmentId,omitempty"`
	Files         map[string]FileRecord `json:"files"`
}

type State struct {
	Version      int                 `json:"version"`
	WorkspaceID  string              `json:"workspaceId"`
	Environments map[string]EnvState `json:"environments"`

	dirty bool
}

func (s *State) Dirty() bool { return s.dirty }

func New(workspaceID string) *State {
	return &State{
		Version:      Version,
		WorkspaceID:  workspaceID,
		Environments: map[string]EnvState{},
	}
}

func Dir(projectRoot string) string {
	return filepath.Join(projectRoot, DirName)
}

func Path(projectRoot string) string {
	return filepath.Join(Dir(projectRoot), FileName)
}

func Load(projectRoot string) (*State, error) {
	path := Path(projectRoot)
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return New(""), nil
	}
	if err != nil {
		return New(""), fmt.Errorf("%w at %s: %w", ErrRecovered, path, err)
	}

	var loaded State
	if err := json.Unmarshal(data, &loaded); err != nil {
		return New(""), fmt.Errorf("%w at %s: %w", ErrRecovered, path, err)
	}
	if err := loaded.Validate(); err != nil {
		return New(""), fmt.Errorf("%w at %s: %w", ErrRecovered, path, err)
	}
	if loaded.Environments == nil {
		loaded.Environments = map[string]EnvState{}
	}
	return &loaded, nil
}

func Save(projectRoot string, state *State) error {
	if state == nil {
		return errors.New("failed to save sync state: state is nil")
	}
	if err := state.Validate(); err != nil {
		return fmt.Errorf("failed to save sync state: %w", err)
	}

	dir := Dir(projectRoot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode sync state: %w", err)
	}
	data = append(data, '\n')

	path := Path(projectRoot)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to rename %s: %w", tmp, err)
	}
	return nil
}

func (s *State) Validate() error {
	if s.Version != Version {
		return fmt.Errorf("version must be %d, got %d", Version, s.Version)
	}
	if !strings.HasPrefix(s.WorkspaceID, workspaceIDPrefix) {
		return fmt.Errorf("workspaceId must start with %q", workspaceIDPrefix)
	}
	return nil
}

func (s *State) BoundTo(workspaceID string) bool {
	return s.WorkspaceID == workspaceID
}

func (s *State) Record(envName, path string) (FileRecord, bool) {
	record, ok := s.Environments[envName].Files[path]
	return record, ok
}

// Put records the merge-base for one file, creating the environment block as
// needed. An empty environmentID leaves any previously observed ID in place.
func (s *State) Put(envName, environmentID, path string, record FileRecord) {
	if s.Environments == nil {
		s.Environments = map[string]EnvState{}
	}
	envState := s.Environments[envName]
	if envState.Files == nil {
		envState.Files = map[string]FileRecord{}
	}
	if environmentID != "" && envState.EnvironmentID != environmentID {
		envState.EnvironmentID = environmentID
		s.dirty = true
	}
	if existing, ok := envState.Files[path]; !ok || existing != record {
		envState.Files[path] = record
		s.dirty = true
	}
	s.Environments[envName] = envState
}

// Prune drops records in one environment for paths not in keep.
func (s *State) Prune(envName string, keep map[string]struct{}) {
	for path := range s.Environments[envName].Files {
		if _, ok := keep[path]; !ok {
			delete(s.Environments[envName].Files, path)
			s.dirty = true
		}
	}
}
