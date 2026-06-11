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
	"github.com/kyenel64/invosit/cli/internal/syncstate"
	"github.com/spf13/cobra"
)

const pushBatchLimit = 100

var (
	pushEnvFlag   string
	pushForceFlag bool
)

var pushCmd = &cobra.Command{
	Use:   "push <path> [path...]",
	Short: "Push file(s) to invosit",
	Long: `Push local files to invosit.
Uses the nearest .invosit.json file as project root.

Each push sends the version it was based on, so a file a teammate updated
since your last pull is rejected instead of silently overwritten. Files
unchanged since the last sync are skipped.

--force overwrites the remote regardless of who changed it last. The
replaced content is permanently unrecoverable: invosit keeps no history.
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

		envName, err := resolveEnvName(pushEnvFlag, cfg)
		if err != nil {
			return err
		}

		apiClient := apiclient.NewClient(creds.APIURL)

		remote, err := remoteFiles(cmd.Context(), apiClient, creds.SessionToken, cfg.WorkspaceID, envName)
		if err != nil {
			return err
		}

		state := loadSyncState(cmd.ErrOrStderr(), projectRoot, cfg.WorkspaceID)

		prepared, failed := prepareFiles(cmd, projectRoot, args)
		planned := planFiles(cmd, dedupePrepared(prepared), envName, state, remote)

		for chunkStart := 0; chunkStart < len(planned); chunkStart += pushBatchLimit {
			chunkEnd := min(chunkStart+pushBatchLimit, len(planned))
			batchFailed, err := pushBatch(cmd, apiClient, creds.SessionToken, cfg.WorkspaceID, envName, planned[chunkStart:chunkEnd], state)
			if err != nil {
				saveSyncState(cmd.ErrOrStderr(), projectRoot, state)
				return err
			}
			failed += batchFailed
		}

		saveSyncState(cmd.ErrOrStderr(), projectRoot, state)

		if failed > 0 {
			return fmt.Errorf("%d of %d files failed to push", failed, len(args))
		}
		return nil
	},
}

func init() {
	pushCmd.Flags().StringVar(&pushEnvFlag, "env", "", "environment name (overrides defaultEnvironment in .invosit.json)")
	pushCmd.Flags().BoolVar(&pushForceFlag, "force", false, "overwrite the remote even if it changed since your last sync (unrecoverable)")
	rootCmd.AddCommand(pushCmd)
}

type preparedFile struct {
	projectRelPath string
	content        []byte
	hash           string
	size           int64
}

type plannedFile struct {
	preparedFile
	baseVersion int64
}

type pushPlan struct {
	baseVersion int64
	skip        bool
	adopt       bool
}

// planPush decides the base version for the server CAS, or to skip the file.
// adopt marks a skip whose merge-base should still be recorded (the local
// file already matches the remote, e.g. after an interrupted sync).
func planPush(localHash string, record syncstate.FileRecord, hasRecord bool, remote apiclient.FileMeta, hasRemote bool, force bool) pushPlan {
	switch {
	case force:
		return pushPlan{baseVersion: remote.Version}
	case hasRecord && localHash == record.ContentHash:
		return pushPlan{skip: true}
	case !hasRecord && hasRemote && localHash == remote.ContentHash:
		return pushPlan{skip: true, adopt: true}
	case hasRecord && !hasRemote:
		return pushPlan{baseVersion: 0}
	case hasRecord && remote.ID != record.FileID:
		// Stale generation (deleted + recreated remotely); the create
		// collision surfaces the server CONFLICT and its pull-first hint.
		return pushPlan{baseVersion: 0}
	case hasRecord:
		return pushPlan{baseVersion: record.Version}
	default:
		return pushPlan{baseVersion: 0}
	}
}

// planFiles classifies each prepared file against the sync state and the
// remote inventory, reporting and recording skips.
func planFiles(cmd *cobra.Command, prepared []preparedFile, envName string, state *syncstate.State, remote map[string]apiclient.FileMeta) []plannedFile {
	stdout := cmd.OutOrStdout()
	planned := make([]plannedFile, 0, len(prepared))
	for _, file := range prepared {
		record, hasRecord := state.Record(envName, file.projectRelPath)
		remoteMeta, hasRemote := remote[file.projectRelPath]
		plan := planPush(file.hash, record, hasRecord, remoteMeta, hasRemote, pushForceFlag)
		if plan.skip {
			if plan.adopt {
				state.Put(envName, remoteMeta.EnvironmentID, file.projectRelPath, syncstate.FileRecord{
					FileID:      remoteMeta.ID,
					Version:     remoteMeta.Version,
					ContentHash: file.hash,
				})
			}
			_, _ = fmt.Fprintf(stdout, "up to date %s\n", file.projectRelPath)
			continue
		}
		planned = append(planned, plannedFile{preparedFile: file, baseVersion: plan.baseVersion})
	}
	return planned
}

func dedupePrepared(prepared []preparedFile) []preparedFile {
	lastByPath := make(map[string]int, len(prepared))
	for index, file := range prepared {
		lastByPath[file.projectRelPath] = index
	}
	deduped := make([]preparedFile, 0, len(lastByPath))
	for index, file := range prepared {
		if lastByPath[file.projectRelPath] == index {
			deduped = append(deduped, file)
		}
	}
	return deduped
}

func remoteFiles(ctx context.Context, client *apiclient.Client, token, workspaceID, environment string) (map[string]apiclient.FileMeta, error) {
	files, err := client.ListFiles(ctx, token, workspaceID, environment)
	if err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return nil, errors.New("not logged in or session expired. run `invosit login` to authenticate")
		}
		return nil, fmt.Errorf("failed to list files: %w", err)
	}
	remote := make(map[string]apiclient.FileMeta, len(files))
	for _, file := range files {
		remote[file.Path] = file.FileMeta
	}
	return remote, nil
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
func pushBatch(cmd *cobra.Command, client *apiclient.Client, token, workspaceID, environment string, batch []plannedFile, state *syncstate.State) (int, error) {
	ctx := cmd.Context()
	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	entries := make([]apiclient.CreateFileEntry, len(batch))
	for index, file := range batch {
		entries[index] = apiclient.CreateFileEntry{
			Path:        file.projectRelPath,
			ContentHash: file.hash,
			Size:        file.size,
			BaseVersion: file.baseVersion,
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
	uploadedFiles := make([]plannedFile, 0, len(batch))
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

		if err := uploadOne(ctx, file.preparedFile, result.UploadURL); err != nil {
			_, _ = fmt.Fprintf(stderr, "failed to push %s: %v\n", file.projectRelPath, err)
			failed++
			continue
		}
		uploadedIDs = append(uploadedIDs, result.File.ID)
		uploadedFiles = append(uploadedFiles, file)
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

	// completeOne returns one result per requested id in request order, so
	// pair positionally: an overwrite's committed id differs from the pending
	// id we sent, so matching by result.ID would miss it.
	for index, result := range completed {
		if index >= len(uploadedFiles) {
			break
		}
		file := uploadedFiles[index]
		if result.Status != "ok" {
			_, _ = fmt.Fprintf(stderr, "failed to push %s: %s\n", file.projectRelPath, formatResultError(result.Code, result.Message))
			failed++
			continue
		}
		if result.File != nil {
			state.Put(environment, result.File.EnvironmentID, file.projectRelPath, syncstate.FileRecord{
				FileID:      result.File.ID,
				Version:     result.File.Version,
				ContentHash: file.hash,
			})
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
	if code == "CONFLICT" {
		return "remote changed since you last pulled; run `invosit pull` then push again"
	}
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
