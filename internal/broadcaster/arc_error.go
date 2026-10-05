package broadcaster

import (
	"errors"
	"fmt"
	"net/http"
)

type ArcError struct {
	StatusCode int
	Body       string
}

func (e *ArcError) Error() string {
	return fmt.Sprintf("arc status %d: %s", e.StatusCode, e.Body)
}

func IsUnreachable(err error) bool {
	if err == nil {
		return false
	}

	var arcErr *ArcError
	if !errors.As(err, &arcErr) {
		return true
	}

	switch arcErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}

	return arcErr.StatusCode >= http.StatusInternalServerError
}

func IsNotFound(err error) bool {
	var arcErr *ArcError
	return errors.As(err, &arcErr) && arcErr.StatusCode == http.StatusNotFound
}
