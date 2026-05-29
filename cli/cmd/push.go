package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/blob"
	"github.com/spf13/cobra"
)

const pushBatchLimit = 100

var pushEnvFlag string

var pushCmd = &cobra.Command{
	Use:   "push <path> [path...]",
	Short: "Push file(s) to invosit",
	Long: `Push local files to invosit.
Uses the nearest .invosit.json file as project root.
	`,
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

		cfg, projectRoot, err := loadProjectConfig()
		if err != nil {
			return err
		}

		envName := pushEnvFlag
		if envName == "" {
			envName = cfg.DefaultEnvironment
		}
		if envName == "" {
			return errors.New("no environment set. pass --env <name> or set defaultEnvironment in .invosit.json")
		}

		apiClient := apiclient.NewClient(creds.APIURL)

		prepared, failed := prepareFiles(cmd, projectRoot, args)

		for chunkStart := 0; chunkStart < len(prepared); chunkStart += pushBatchLimit {
			chunkEnd := min(chunkStart+pushBatchLimit, len(prepared))
			batchFailed, err := pushBatch(cmd, apiClient, creds.SessionToken, cfg.WorkspaceID, envName, prepared[chunkStart:chunkEnd])
			if err != nil {
				return err
			}
			failed += batchFailed
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

type preparedFile struct {
	projectRelPath string
	content        []byte
	hash           string
	size           int64
}

// prepareFiles resolves and hashes each arg. Failures are reported to stderr
// and dropped from the returned slice so the rest of the batch can proceed.
func prepareFiles(cmd *cobra.Command, projectRoot string, args []string) ([]preparedFile, int) {
	stderr := cmd.ErrOrStderr()
	prepared := make([]preparedFile, 0, len(args))
	failed := 0
	for _, arg := range args {
		entry, err := prepareFile(projectRoot, arg)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "failed to push %s: %v\n", arg, err)
			failed++
			continue
		}
		prepared = append(prepared, entry)
	}
	return prepared, failed
}

func prepareFile(projectRoot, arg string) (preparedFile, error) {
	projectRelPath, err := projectRelative(projectRoot, arg)
	if err != nil {
		return preparedFile{}, err
	}

	content, err := os.ReadFile(arg) //nolint:gosec
	if err != nil {
		return preparedFile{}, fmt.Errorf("failed to read file: %w", err)
	}

	sum := sha256.Sum256(content)
	return preparedFile{
		projectRelPath: projectRelPath,
		content:        content,
		hash:           hex.EncodeToString(sum[:]),
		size:           int64(len(content)),
	}, nil
}

// pushBatch runs the 2-step file creation process.
// create pending file metadata -> retrieve and post to s3 with signed url -> call :complete
func pushBatch(cmd *cobra.Command, client *apiclient.Client, token, workspaceID, environment string, batch []preparedFile) (int, error) {
	ctx := cmd.Context()
	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	entries := make([]apiclient.CreateFileEntry, len(batch))
	for index, file := range batch {
		entries[index] = apiclient.CreateFileEntry{
			Path:        file.projectRelPath,
			ContentHash: file.hash,
			Size:        file.size,
		}
	}

	created, err := client.CreateFiles(ctx, token, workspaceID, environment, entries)
	if err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return 0, errors.New("not logged in or session expired. run `invosit login` to authenticate")
		}
		return 0, fmt.Errorf("failed to register files with api: %w", err)
	}

	resultByPath := make(map[string]apiclient.CreateFilesResult, len(created))
	for _, result := range created {
		resultByPath[result.Path] = result
	}

	uploadedIDs := make([]string, 0, len(batch))
	uploadedByID := make(map[string]preparedFile, len(batch))
	failed := 0

	for _, file := range batch {
		result, ok := resultByPath[file.projectRelPath]
		if !ok {
			_, _ = fmt.Fprintf(stderr, "failed to push %s: no result returned from api\n", file.projectRelPath)
			failed++
			continue
		}
		if result.Status != "ok" || result.File == nil {
			_, _ = fmt.Fprintf(stderr, "failed to push %s: %s\n", file.projectRelPath, formatResultError(result.Code, result.Message))
			failed++
			continue
		}

		if err := uploadOne(ctx, file, result.UploadURL); err != nil {
			_, _ = fmt.Fprintf(stderr, "failed to push %s: %v\n", file.projectRelPath, err)
			failed++
			continue
		}
		uploadedIDs = append(uploadedIDs, result.File.ID)
		uploadedByID[result.File.ID] = file
	}

	if len(uploadedIDs) == 0 {
		return failed, nil
	}

	completed, err := client.CompleteFiles(ctx, token, workspaceID, environment, uploadedIDs)
	if err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return 0, errors.New("not logged in or session expired. run `invosit login` to authenticate")
		}
		return 0, fmt.Errorf("failed to complete files with api: %w", err)
	}

	for _, result := range completed {
		file, ok := uploadedByID[result.ID]
		if !ok {
			continue
		}
		if result.Status != "ok" {
			_, _ = fmt.Fprintf(stderr, "failed to push %s: %s\n", file.projectRelPath, formatResultError(result.Code, result.Message))
			failed++
			continue
		}
		_, _ = fmt.Fprintf(stdout, "pushed %s (%d bytes)\n", file.projectRelPath, file.size)
	}

	return failed, nil
}

func uploadOne(ctx context.Context, file preparedFile, signedURL string) error {
	if err := blob.Upload(ctx, signedURL, bytes.NewReader(file.content), file.size); err != nil {
		return fmt.Errorf("failed to upload blob: %w", err)
	}
	return nil
}

func formatResultError(code, message string) string {
	switch {
	case code != "" && message != "":
		return fmt.Sprintf("%s: %s", code, message)
	case message != "":
		return message
	case code != "":
		return code
	default:
		return "unknown error"
	}
}

// projectRelative constructs an invosit-valid path relative to project root
func projectRelative(projectRoot, relPath string) (string, error) {
	absPath, err := filepath.Abs(relPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}
	realProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return "", fmt.Errorf("failed to resolve project root: %w", err)
	}
	realAbsPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}
	rel, err := filepath.Rel(realProjectRoot, realAbsPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve project-relative path: %w", err)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", errors.New("file is outside the project tree")
	}
	return rel, nil
}
