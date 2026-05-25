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
	"slices"
	"strings"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/blob"
	"github.com/spf13/cobra"
)

var pushEnvFlag string

var pushCmd = &cobra.Command{
	Use:   "push <path> [path...]",
	Short: "Push file(s) to invosit",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return errors.New("pass at least one file to push")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := loadCredentials()
		if err != nil {
			return err
		}

		cfg, configPath, err := loadProjectConfig()
		if err != nil {
			return err
		}
		projectRoot := filepath.Dir(configPath)

		envName := pushEnvFlag
		if envName == "" {
			envName = cfg.DefaultEnvironment
		}
		if envName == "" {
			return errors.New("no environment set. pass --env <name> or set defaultEnvironment in .invosit.json")
		}

		apiClient := apiclient.NewClient(creds.APIURL)
		envID, err := resolveEnvID(cmd.Context(), apiClient, creds.SessionToken, cfg.WorkspaceID, envName)
		if err != nil {
			return err
		}

		failed := 0
		// TODO: batch push files
		for _, arg := range args {
			if err := pushOne(cmd.Context(), apiClient, creds.SessionToken, cfg.WorkspaceID, envID, projectRoot, arg); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "failed to push %s: %v\n", arg, err)
				failed++
				continue
			}
		}

		if failed > 0 {
			return fmt.Errorf("%d of %d files failed to push", failed, len(args))
		}
		return nil
	},
}

func init() {
	pushCmd.Flags().StringVar(&pushEnvFlag, "env", "", "environment name (overrides defaultEnvironment in .invosit.json)")
	rootCmd.AddCommand(pushCmd)
}

func resolveEnvID(ctx context.Context, client *apiclient.Client, token, workspaceID, name string) (string, error) {
	environments, err := client.GetEnvironments(ctx, token, workspaceID)
	if err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return "", errors.New("not logged in or session expired. run `invosit login` to authenticate")
		}
		return "", fmt.Errorf("failed to list environments: %w", err)
	}
	for _, env := range environments {
		if env.Name == name {
			return env.ID, nil
		}
	}
	return "", fmt.Errorf("environment %q not found in workspace", name)
}

func pushOne(ctx context.Context, client *apiclient.Client, token, workspaceID, envID, projectRoot, relPath string) error {
	storedPath, err := projectRelative(projectRoot, relPath)
	if err != nil {
		return err
	}
	if err := validateStoredPath(storedPath); err != nil {
		return err
	}

	// TODO: Hash with TeeReader
	hash, size, err := hashFile(relPath)
	if err != nil {
		return err
	}

	res, err := client.PushFile(ctx, token, workspaceID, envID, apiclient.PushFileRequest{
		Path:        storedPath,
		ContentHash: hash,
		Size:        size,
	})
	if err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return errors.New("not logged in or session expired. run `invosit login` to authenticate")
		}
		return fmt.Errorf("failed to register file with api: %w", err)
	}

	file, err := os.Open(relPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to open file for upload: %w", err)
	}
	defer func() { _ = file.Close() }()

	if err := blob.Upload(ctx, res.UploadURL, file, size); err != nil {
		return fmt.Errorf("failed to upload blob: %w", err)
	}

	fmt.Printf("pushed %s (%d bytes)\n", storedPath, size)
	return nil
}

// projectRelative returns filepath relative to project root.
func projectRelative(projectRoot, relPath string) (string, error) {
	absArg, err := filepath.Abs(relPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}
	rel, err := filepath.Rel(projectRoot, absArg)
	if err != nil {
		return "", fmt.Errorf("failed to resolve project-relative path: %w", err)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", errors.New("file is outside the project tree")
	}
	return rel, nil
}

// validateStoredPath checks that the filepath is a valid path for invosit.
func validateStoredPath(path string) error {
	if path == "" {
		return errors.New("path is empty")
	}
	if strings.ContainsRune(path, 0) {
		return errors.New("path contains a null byte")
	}
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\\") {
		return errors.New("path must be relative, not absolute")
	}
	for _, sep := range []string{"/", "\\"} {
		if slices.Contains(strings.Split(path, sep), "..") {
			return errors.New("path contains traversal (..) segments")
		}
	}
	return nil
}

func hashFile(relPath string) (string, int64, error) {
	file, err := os.Open(relPath) //nolint:gosec
	if err != nil {
		return "", 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = file.Close() }()

	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return "", 0, fmt.Errorf("failed to hash file: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}
