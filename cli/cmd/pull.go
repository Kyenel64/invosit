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
	"github.com/kyenel64/invosit/cli/internal/syncstate"
	"github.com/spf13/cobra"
)

var (
	pullEnvFlag   string
	pullForceFlag bool
)

var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull file(s) from invosit",
	Long: `Pull all files tracked in the target environment into the project tree.
Uses the nearest .invosit.json file as project root.

--force overwrites local files unconditionally.
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
			default:
				return fmt.Errorf("failed to list files: %w", err)
			}
		}

		state := loadSyncState(stderr, projectRoot, cfg.WorkspaceID)

		failed := 0
		blocked := 0
		remotePaths := make(map[string]struct{}, len(files))
		for _, file := range files {
			remotePaths[file.Path] = struct{}{}

			dest, err := resolveWithinProject(projectRoot, file.Path)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "failed to pull %s: %v\n", file.Path, err)
				failed++
				continue
			}

			localHash, localExists, err := localFileHash(dest)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "failed to pull %s: %v\n", file.Path, err)
				failed++
				continue
			}

			record, hasRecord := state.Record(envName, file.Path)
			base := syncstate.FileRecord{FileID: file.ID, Version: file.Version, ContentHash: file.ContentHash}

			switch planPull(localHash, localExists, record, hasRecord, file, pullForceFlag) {
			case pullDownload:
				if err := pullOne(ctx, dest, file); err != nil {
					_, _ = fmt.Fprintf(stderr, "failed to pull %s: %v\n", file.Path, err)
					failed++
					continue
				}
				state.Put(envName, file.EnvironmentID, file.Path, base)
				_, _ = fmt.Fprintf(stdout, "pulled %s (%d bytes)\n", file.Path, file.Size)
			case pullUpToDate:
				state.Put(envName, file.EnvironmentID, file.Path, base)
				_, _ = fmt.Fprintf(stdout, "up to date %s\n", file.Path)
			case pullLocalOnly:
				_, _ = fmt.Fprintf(stdout, "skipped %s (local changes, remote unchanged)\n", file.Path)
			case pullConflict:
				_, _ = fmt.Fprintf(stderr, "conflict: %s changed locally and remotely; skipped (pull --force overwrites local)\n", file.Path)
				blocked++
			case pullRefuse:
				_, _ = fmt.Fprintf(stderr, "refusing to overwrite %s: local file differs and no sync state exists (pull --force overwrites local)\n", file.Path)
				blocked++
			}
		}

		state.Prune(envName, remotePaths)
		saveSyncState(stderr, projectRoot, state)

		switch {
		case failed > 0 && blocked > 0:
			return fmt.Errorf("%d of %d files failed to pull; %d skipped due to conflicts", failed, len(files), blocked)
		case failed > 0:
			return fmt.Errorf("%d of %d files failed to pull", failed, len(files))
		case blocked > 0:
			return fmt.Errorf("%d of %d files skipped due to conflicts; resolve or pull --force", blocked, len(files))
		}
		return nil
	},
}

func init() {
	pullCmd.Flags().StringVar(&pullEnvFlag, "env", "", "environment name (overrides defaultEnvironment in .invosit.json)")
	pullCmd.Flags().BoolVar(&pullForceFlag, "force", false, "overwrite local files even if they have local changes (unrecoverable)")
	rootCmd.AddCommand(pullCmd)
}

type pullAction int

const (
	pullDownload pullAction = iota
	pullUpToDate
	pullLocalOnly
	pullConflict
	pullRefuse
)

// planPull is the three-way compare between the local file, the last synced
// merge-base, and the remote file.
func planPull(localHash string, localExists bool, record syncstate.FileRecord, hasRecord bool, remote apiclient.ListedFileMeta, force bool) pullAction {
	switch {
	case force:
		return pullDownload
	case !localExists:
		return pullDownload
	case strings.EqualFold(localHash, remote.ContentHash):
		// M3 plaintext shortcut: at M4 the remote hash is ciphertext, so
		// this row stops matching and the base comparisons below decide.
		return pullUpToDate
	case hasRecord && localHash == record.ContentHash:
		return pullDownload
	case hasRecord && remote.ID == record.FileID && remote.Version == record.Version:
		return pullLocalOnly
	case hasRecord:
		return pullConflict
	default:
		return pullRefuse
	}
}

// localFileHash hashes the file at path; exists is false when it is absent.
// Non-regular files (directories, sockets) are an error, never a download
// target.
func localFileHash(path string) (hash string, exists bool, err error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("failed to stat local file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", false, errors.New("local path is not a regular file")
	}

	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", false, fmt.Errorf("failed to read local file: %w", err)
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), true, nil
}

// pullOne downloads a single file, verifies its SHA-256 against the manifest
// entry, and atomically writes it to dest. On any failure the existing local
// file is left untouched.
func pullOne(ctx context.Context, dest string, file apiclient.ListedFileMeta) error {
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

	if err := withinRoot(realProjectRoot, dest); err != nil {
		return "", err
	}

	// Lexical containment isn't enough. A symlinked directory inside the
	// checkout can redirect the real write target outside the project, so
	// resolve the deepest existing ancestor and reject it if it escapes.
	ancestor := filepath.Dir(dest)
	for {
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			if err := withinRoot(realProjectRoot, resolved); err != nil {
				return "", err
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("failed to resolve destination path: %w", err)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", fmt.Errorf("failed to resolve destination path: %w", err)
		}
		ancestor = parent
	}

	return dest, nil
}

// withinRoot reports an error if path lies outside root (lexically). Both
// arguments must already be cleaned, absolute, and symlink-resolved.
func withinRoot(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("failed to resolve project-relative path: %w", err)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return errors.New("file is outside the project tree")
	}
	return nil
}
