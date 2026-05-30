package blob

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

var httpClient = &http.Client{Timeout: 5 * time.Minute}

// Upload sends a PUT request to the s3 signed URL
func Upload(ctx context.Context, signedURL string, body io.Reader, size int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, signedURL, body)
	if err != nil {
		return fmt.Errorf("build upload request: %w", err)
	}
	req.ContentLength = size

	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload request failed: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("upload returned status %d", res.StatusCode)
	}
	return nil
}

// Download streams a GET request from the s3 signed URL into dst
func Download(ctx context.Context, signedURL string, dst io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("download returned status %d", res.StatusCode)
	}

	if _, err := io.Copy(dst, res.Body); err != nil {
		return fmt.Errorf("download copy failed: %w", err)
	}
	return nil
}
