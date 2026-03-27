package types

import "errors"

// ErrTest is a sentinel error for use in tests.
var ErrTest = errors.New("test error")

// APIError is a typed error intended for HTTP responses.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func NewAPIError(code, message string) *APIError {
	return &APIError{Code: code, Message: message}
}

func IsAPIError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr)
}
