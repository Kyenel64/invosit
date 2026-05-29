package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/blob"
	"github.com/spf13/cobra"
)

var pullEnvFlag string

var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull file(s) from invosit",
	Long: `Pull all files tracked in the target environment into the project tree.
Uses the nearest .invosit.json file as project root.
	`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		creds, err := loadCredentials()
		if err != nil {
			return err
		}

		cfg, projectRoot, err := loadProjectConfig()
		if err != nil {
			return err
		}

		envName := pullEnvFlag
		if envName == "" {
			envName = cfg.DefaultEnvironment
		}
		if envName == "" {
			return errors.New("no environment set. pass --env <name> or set defaultEnvironment in .invosit.json")
		}

		ctx := cmd.Context()
		stdout := cmd.OutOrStdout()
		stderr := cmd.ErrOrStderr()

		apiClient := apiclient.NewClient(creds.APIURL)

		files, err := apiClient.ListFiles(ctx, creds.SessionToken, cfg.WorkspaceID, envName)
		if err != nil {
			switch {
			case errors.Is(err, apiclient.ErrUnauthorized):
				return errors.New("not logged in or session expired. run `invosit login` to authenticate")
			case errors.Is(err, apiclient.ErrForbidden):
				return errors.New("access denied")
			default:
				return fmt.Errorf("failed to list files: %w", err)
			}
		}

		failed := 0
		for _, file := range files {
			if err := pullOne(ctx, projectRoot, file); err != nil {
				_, _ = fmt.Fprintf(stderr, "failed to pull %s: %v\n", file.Path, err)
				failed++
				continue
			}
			_, _ = fmt.Fprintf(stdout, "pulled %s (%d bytes)\n", file.Path, file.Size)
		}

		if failed > 0 {
			return fmt.Errorf("%d of %d files failed to pull", failed, len(files))
		}
		return nil
	},
}

func init() {
	pullCmd.Flags().StringVar(&pullEnvFlag, "env", "", "environment name (overrides defaultEnvironment in .invosit.json)")
	rootCmd.AddCommand(pullCmd)
}

// pullOne downloads a single file, verifies its SHA-256 against the manifest
// entry, and atomically writes it to its project-relative path. On any failure
// the existing local file is left untouched.
func pullOne(ctx context.Context, projectRoot string, file apiclient.ListedFileMeta) error {
	dest, err := resolveWithinProject(projectRoot, file.Path)
	if err != nil {
		return err
	}

	destDir := filepath.Dir(dest)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	tmp, err := os.CreateTemp(destDir, ".invosit-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	hasher := sha256.New()
	if err := blob.Download(ctx, file.DownloadURL, io.MultiWriter(tmp, hasher)); err != nil {
		cleanup()
		return fmt.Errorf("failed to download blob: %w", err)
	}

	if got := hex.EncodeToString(hasher.Sum(nil)); !strings.EqualFold(got, file.ContentHash) {
		cleanup()
		return errors.New("content hash mismatch: downloaded file is corrupt")
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, dest); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

// resolveWithinProject turns an API-reported project-relative path into an
// absolute destination, rejecting absolute paths, null bytes, and any path
// that would escape the project tree.
func resolveWithinProject(projectRoot, relPath string) (string, error) {
	if strings.ContainsRune(relPath, 0) {
		return "", errors.New("invalid path: contains null byte")
	}
	if relPath == "" {
		return "", errors.New("invalid path: empty")
	}
	if filepath.IsAbs(relPath) {
		return "", errors.New("invalid path: must be relative")
	}

	realProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return "", fmt.Errorf("failed to resolve project root: %w", err)
	}

	cleaned := filepath.Clean(filepath.FromSlash(relPath))
	dest := filepath.Join(realProjectRoot, cleaned)

	rel, err := filepath.Rel(realProjectRoot, dest)
	if err != nil {
		return "", fmt.Errorf("failed to resolve project-relative path: %w", err)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", errors.New("file is outside the project tree")
	}
	return dest, nil
}
