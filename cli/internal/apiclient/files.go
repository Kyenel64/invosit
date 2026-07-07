package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type FileMeta struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environment_id"`
	Path          string    `json:"path"`
	ContentHash   string    `json:"content_hash"`
	Size          int64     `json:"size"`
	Version       int64     `json:"version"`
	PushedBy      string    `json:"pushed_by"`
	PushedAt      time.Time `json:"pushed_at"`
}

type ListedFileMeta struct {
	FileMeta
	DownloadURL       string    `json:"download_url"`
	DownloadExpiresAt time.Time `json:"download_expires_at"`
	WrappedDEK        []byte    `json:"wrapped_dek"`
}

func (meta *FileMeta) normalize() {
	if meta == nil {
		return
	}
	meta.ContentHash = strings.ToLower(meta.ContentHash)
}

type WrappedDEKEntry struct {
	UserID       string `json:"user_id"`
	EncryptedDEK []byte `json:"encrypted_dek"`
}

type CreateFileEntry struct {
	Path        string            `json:"path"`
	ContentHash string            `json:"content_hash"`
	Size        int64             `json:"size"`
	BaseVersion int64             `json:"base_version"`
	WrappedDEKs []WrappedDEKEntry `json:"wrapped_deks"`
}

type CreateFilesResult struct {
	Path            string     `json:"path"`
	Status          string     `json:"status"`
	File            *FileMeta  `json:"file,omitempty"`
	UploadURL       string     `json:"upload_url,omitempty"`
	UploadExpiresAt *time.Time `json:"upload_expires_at,omitempty"`
	Code            string     `json:"code,omitempty"`
	Message         string     `json:"message,omitempty"`
}

type CompleteFilesResult struct {
	ID      string    `json:"id"`
	Status  string    `json:"status"`
	File    *FileMeta `json:"file,omitempty"`
	Code    string    `json:"code,omitempty"`
	Message string    `json:"message,omitempty"`
}

func (c *Client) ListFiles(ctx context.Context, token, workspaceID, environment string) ([]ListedFileMeta, error) {
	endpoint := fmt.Sprintf("%s/api/v1/workspaces/%s/environments/%s/files", c.baseURL, workspaceID, url.PathEscape(environment))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build list files request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("list files request failed: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusOK:
		var envelope struct {
			Files []ListedFileMeta `json:"files"`
		}
		if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
			return nil, fmt.Errorf("decode list files response: %w", err)
		}
		for index := range envelope.Files {
			envelope.Files[index].normalize()
		}
		return envelope.Files, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	default:
		return nil, fmt.Errorf("unexpected status: %d", res.StatusCode)
	}
}

func (c *Client) CreateFiles(ctx context.Context, token, workspaceID, environment string, files []CreateFileEntry) ([]CreateFilesResult, error) {
	body, err := json.Marshal(struct {
		Files []CreateFileEntry `json:"files"`
	}{Files: files})
	if err != nil {
		return nil, fmt.Errorf("encode create files request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/v1/workspaces/%s/environments/%s/files", c.baseURL, workspaceID, url.PathEscape(environment))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build create files request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("create files request failed: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusOK:
		var envelope struct {
			Results []CreateFilesResult `json:"results"`
		}
		if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
			return nil, fmt.Errorf("decode create files response: %w", err)
		}
		for _, result := range envelope.Results {
			result.File.normalize()
		}
		return envelope.Results, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	default:
		return nil, fmt.Errorf("unexpected status: %d", res.StatusCode)
	}
}

func (c *Client) CompleteFiles(ctx context.Context, token, workspaceID, environment string, fileIDs []string) ([]CompleteFilesResult, error) {
	body, err := json.Marshal(struct {
		FileIDs []string `json:"file_ids"`
	}{FileIDs: fileIDs})
	if err != nil {
		return nil, fmt.Errorf("encode complete files request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/v1/workspaces/%s/environments/%s/files:complete", c.baseURL, workspaceID, url.PathEscape(environment))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build complete files request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("complete files request failed: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusOK:
		var envelope struct {
			Results []CompleteFilesResult `json:"results"`
		}
		if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
			return nil, fmt.Errorf("decode complete files response: %w", err)
		}
		for _, result := range envelope.Results {
			result.File.normalize()
		}
		return envelope.Results, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	default:
		return nil, fmt.Errorf("unexpected status: %d", res.StatusCode)
	}
}
