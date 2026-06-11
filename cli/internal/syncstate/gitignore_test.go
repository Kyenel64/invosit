package syncstate_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/syncstate"
)

func gitignorePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".gitignore")
}

func readGitignore(t *testing.T, projectRoot string) string {
	t.Helper()
	content, err := os.ReadFile(gitignorePath(projectRoot))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(content)
}

func writeGitignore(t *testing.T, projectRoot, content string) {
	t.Helper()
	if err := os.WriteFile(gitignorePath(projectRoot), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestEnsureGitignoredCreatesFile(t *testing.T) {
	projectRoot := t.TempDir()
	if err := syncstate.EnsureGitignored(projectRoot); err != nil {
		t.Fatalf("EnsureGitignored: %v", err)
	}
	if got := readGitignore(t, projectRoot); got != ".invosit/\n" {
		t.Errorf("content = %q, want %q", got, ".invosit/\n")
	}
}

func TestEnsureGitignoredAppendsPreservingContent(t *testing.T) {
	projectRoot := t.TempDir()
	writeGitignore(t, projectRoot, "node_modules/\n.env\n")

	if err := syncstate.EnsureGitignored(projectRoot); err != nil {
		t.Fatalf("EnsureGitignored: %v", err)
	}
	if got := readGitignore(t, projectRoot); got != "node_modules/\n.env\n.invosit/\n" {
		t.Errorf("content = %q", got)
	}
}

func TestEnsureGitignoredAppendsAfterMissingTrailingNewline(t *testing.T) {
	projectRoot := t.TempDir()
	writeGitignore(t, projectRoot, "node_modules/")

	if err := syncstate.EnsureGitignored(projectRoot); err != nil {
		t.Fatalf("EnsureGitignored: %v", err)
	}
	if got := readGitignore(t, projectRoot); got != "node_modules/\n.invosit/\n" {
		t.Errorf("content = %q", got)
	}
}

func TestEnsureGitignoredIdempotent(t *testing.T) {
	projectRoot := t.TempDir()
	for range 2 {
		if err := syncstate.EnsureGitignored(projectRoot); err != nil {
			t.Fatalf("EnsureGitignored: %v", err)
		}
	}
	if got := readGitignore(t, projectRoot); got != ".invosit/\n" {
		t.Errorf("content = %q, want single entry", got)
	}
}

func TestEnsureGitignoredMatchesExistingVariants(t *testing.T) {
	for _, existing := range []string{".invosit\n", ".invosit/\n", "/.invosit/\n", "  .invosit/  \n"} {
		projectRoot := t.TempDir()
		writeGitignore(t, projectRoot, existing)

		if err := syncstate.EnsureGitignored(projectRoot); err != nil {
			t.Fatalf("EnsureGitignored(%q): %v", existing, err)
		}
		if got := readGitignore(t, projectRoot); got != existing {
			t.Errorf("existing %q was modified to %q", existing, got)
		}
	}
}

func TestEnsureGitignoredDoesNotMatchConfigFileLine(t *testing.T) {
	projectRoot := t.TempDir()
	writeGitignore(t, projectRoot, ".invosit.json\n")

	if err := syncstate.EnsureGitignored(projectRoot); err != nil {
		t.Fatalf("EnsureGitignored: %v", err)
	}
	if got := readGitignore(t, projectRoot); got != ".invosit.json\n.invosit/\n" {
		t.Errorf("content = %q, want .invosit/ appended", got)
	}
}
