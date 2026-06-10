package syncstate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureGitignored makes sure projectRoot/.gitignore ignores .invosit/
func EnsureGitignored(projectRoot string) error {
	path := filepath.Join(projectRoot, ".gitignore")
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}

	for line := range strings.SplitSeq(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "/")
		trimmed = strings.TrimSuffix(trimmed, "/")
		if trimmed == DirName {
			return nil
		}
	}

	entry := DirName + "/\n"
	if len(content) > 0 && !bytes.HasSuffix(content, []byte("\n")) {
		entry = "\n" + entry
	}

	file, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", path, err)
	}
	if _, err := file.WriteString(entry); err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close %s: %w", path, err)
	}
	return nil
}
