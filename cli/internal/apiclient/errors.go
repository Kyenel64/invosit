package apiclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var ErrUnauthorized = errors.New("unauthorized")

// APIError carries the API's `{"error","code"}` envelope so commands can show
// the server's stable code and safe message rather than a bare status number.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	switch {
	case e.Code != "" && e.Message != "":
		return fmt.Sprintf("%s: %s (status %d)", e.Code, e.Message, e.StatusCode)
	case e.Message != "":
		return fmt.Sprintf("%s (status %d)", e.Message, e.StatusCode)
	case e.Code != "":
		return fmt.Sprintf("%s (status %d)", e.Code, e.StatusCode)
	default:
		return fmt.Sprintf("unexpected status: %d", e.StatusCode)
	}
}

// maxErrorBodyBytes caps the non-2xx body read: the envelope is a two-field
// JSON object, so anything past this is not one worth decoding.
const maxErrorBodyBytes = 64 << 10

// parseAPIError builds an *APIError from a non-2xx response. Bodies that
// aren't the API's error envelope leave Code and Message empty, which renders
// as "unexpected status: <n>".
func parseAPIError(res *http.Response) error {
	apiErr := &APIError{StatusCode: res.StatusCode}

	var envelope struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxErrorBodyBytes)).Decode(&envelope); err == nil {
		apiErr.Code = strings.TrimSpace(envelope.Code)
		apiErr.Message = strings.TrimSpace(envelope.Error)
	}

	return apiErr
}
