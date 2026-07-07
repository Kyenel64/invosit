package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/syncstate"
)

var statusEnvFlag string

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the state of tracked files",
	Long: `Show the sync state of every file tracked in the target environment.
Uses the nearest .invosit.json file as project root.`,
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

		envName, err := resolveEnvName(statusEnvFlag, cfg)
		if err != nil {
			return err
		}

		ctx := cmd.Context()
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

		readFailed := 0
		entries := make([]statusEntry, 0, len(files))
		for _, file := range files {
			dest, err := resolveWithinProject(projectRoot, file.Path)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "failed to read %s: %v\n", file.Path, err)
				readFailed++
				continue
			}

			localHash, localExists, err := localFileHash(dest)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "failed to read %s: %v\n", file.Path, err)
				readFailed++
				continue
			}

			record, hasRecord := state.Record(envName, file.Path)
			entries = append(entries, statusEntry{
				path:   file.Path,
				status: classifyStatus(localHash, localExists, record, hasRecord, file.FileMeta),
			})
		}

		out := cmd.OutOrStdout()
		styles := newStatusStyles(lipgloss.NewRenderer(out))
		name := workspaceName(ctx, apiClient, creds.SessionToken, cfg.WorkspaceID)
		renderStatus(out, styles, name, cfg.WorkspaceID, envName, entries)

		if readFailed > 0 {
			return fmt.Errorf("failed to read %d of %d files", readFailed, len(files))
		}
		return nil
	},
}

func init() {
	statusCmd.Flags().StringVar(&statusEnvFlag, "env", "", "environment name (overrides defaultEnvironment in .invosit.json)")
	rootCmd.AddCommand(statusCmd)
}

type fileStatus int

const (
	statusConflict fileStatus = iota
	statusDiverged
	statusBehind
	statusModified
	statusMissing
	statusOK
)

func (s fileStatus) rank() int {
	if s == statusDiverged {
		return int(statusConflict)
	}
	return int(s)
}

func (s fileStatus) word() string {
	switch s {
	case statusConflict, statusDiverged:
		return "conflict"
	case statusBehind:
		return "behind"
	case statusModified:
		return "modified"
	case statusMissing:
		return "missing"
	default:
		return "ok"
	}
}

func (s fileStatus) hint() string {
	switch s {
	case statusConflict:
		return "changed locally and remotely. resolve or pull --force"
	case statusDiverged:
		return "differs from remote, no sync history. pull --force or push --force"
	case statusBehind:
		return "remote is newer. pull"
	case statusModified:
		return "local edits. push"
	case statusMissing:
		return "not on disk. pull"
	default:
		return ""
	}
}

// classifyStatus is the read-only counterpart of planPull: the same three-way
// compare in the same predicate order, minus the force arm. The remote
// content_hash is the ciphertext hash, so only the record's plaintext
// merge-base and the server version can decide.
func classifyStatus(localHash string, localExists bool, record syncstate.FileRecord, hasRecord bool, remote apiclient.FileMeta) fileStatus {
	switch {
	case !localExists:
		return statusMissing
	case hasRecord && remote.ID == record.FileID && remote.Version == record.Version && localHash == record.ContentHash:
		return statusOK
	case hasRecord && localHash == record.ContentHash:
		return statusBehind
	case hasRecord && remote.ID == record.FileID && remote.Version == record.Version:
		return statusModified
	case hasRecord:
		return statusConflict
	default:
		return statusDiverged
	}
}

type statusEntry struct {
	path   string
	status fileStatus
}

type statusStyles struct {
	workspace   lipgloss.Style
	environment lipgloss.Style
	dim         lipgloss.Style
	conflict    lipgloss.Style
	behind      lipgloss.Style
	modified    lipgloss.Style
	missing     lipgloss.Style
}

func newStatusStyles(renderer *lipgloss.Renderer) statusStyles {
	return statusStyles{
		workspace:   renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		environment: renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
		dim:         renderer.NewStyle().Foreground(lipgloss.Color("240")),
		conflict:    renderer.NewStyle().Foreground(lipgloss.Color("9")),
		behind:      renderer.NewStyle().Foreground(lipgloss.Color("14")),
		modified:    renderer.NewStyle().Foreground(lipgloss.Color("11")),
		missing:     renderer.NewStyle().Foreground(lipgloss.Color("13")),
	}
}

func (s statusStyles) forStatus(status fileStatus) lipgloss.Style {
	switch status {
	case statusConflict, statusDiverged:
		return s.conflict
	case statusBehind:
		return s.behind
	case statusModified:
		return s.modified
	case statusMissing:
		return s.missing
	default:
		return s.dim
	}
}

const statusWordWidth = len("conflict")

// renderStatus prints the status report: header, then files grouped by
// status (most-urgent-first) with the action hint once per group. Pads raw
// text before styling so columns align in color mode.
func renderStatus(out io.Writer, styles statusStyles, workspaceName, workspaceID, envName string, entries []statusEntry) {
	workspaceLabel := workspaceName
	if workspaceLabel == "" {
		workspaceLabel = workspaceID
	}
	_, _ = fmt.Fprintf(out, "Workspace: %s\n", styles.workspace.Render(workspaceLabel))
	_, _ = fmt.Fprintf(out, "Environment: %s\n", styles.environment.Render(envName))

	if len(entries) == 0 {
		_, _ = fmt.Fprintf(out, "\nno files tracked in %s\n", envName)
		return
	}

	slices.SortFunc(entries, func(a, b statusEntry) int {
		if byRank := cmp.Compare(a.status.rank(), b.status.rank()); byRank != 0 {
			return byRank
		}
		if byStatus := cmp.Compare(a.status, b.status); byStatus != 0 {
			return byStatus
		}
		return cmp.Compare(a.path, b.path)
	})

	_, _ = fmt.Fprintf(out, "\nFiles:\n")
	for index, entry := range entries {
		if index == 0 || entry.status != entries[index-1].status {
			if index > 0 {
				_, _ = fmt.Fprintln(out)
			}
			if hint := entry.status.hint(); hint != "" {
				_, _ = fmt.Fprintf(out, "  %s\n", styles.dim.Render("("+hint+")"))
			}
		}
		style := styles.forStatus(entry.status)
		word := entry.status.word()
		wordCell := style.Render(word + strings.Repeat(" ", statusWordWidth-len(word)))
		if entry.status == statusOK {
			_, _ = fmt.Fprintf(out, "  %s  %s\n", wordCell, styles.dim.Render(entry.path))
		} else {
			_, _ = fmt.Fprintf(out, "  %s  %s\n", wordCell, entry.path)
		}
	}
}

// workspaceName is a best-effort lookup for the header; a failure or miss
// falls back to the bare workspace ID rather than failing the report.
func workspaceName(ctx context.Context, client *apiclient.Client, token, workspaceID string) string {
	workspaces, err := client.GetWorkspaces(ctx, token)
	if err != nil {
		return ""
	}
	for _, workspace := range workspaces {
		if workspace.ID == workspaceID {
			return workspace.Name
		}
	}
	return ""
}
