package blob

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

var httpClient = &http.Client{}

// Upload sends a POST request to the s3 signed URL
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
