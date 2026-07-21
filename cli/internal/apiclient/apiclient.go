package apiclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

var ErrUnauthorized = errors.New("unauthorized")

// APIError represents a structured error response from the API.
// The API returns errors in the shape {"error": "<message>", "code": "<CODE>"}.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %s (status %d)", e.Code, e.Message, e.StatusCode)
	}
	return fmt.Sprintf("%s (status %d)", e.Message, e.StatusCode)
}

// parseAPIError attempts to decode the API's {"error","code"} envelope from res.
// If decoding fails or the body doesn't match the expected shape, it falls back
// to a generic "unexpected status: <n>" error.
func parseAPIError(res *http.Response) error {
	var envelope struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}

	body, err := io.ReadAll(res.Body)
	if err != nil || len(body) == 0 {
		return fmt.Errorf("unexpected status: %d", res.StatusCode)
	}

	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("unexpected status: %d", res.StatusCode)
	}

	if envelope.Error == "" {
		return fmt.Errorf("unexpected status: %d", res.StatusCode)
	}

	return &APIError{
		StatusCode: res.StatusCode,
		Code:       envelope.Code,
		Message:    envelope.Error,
	}
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}
