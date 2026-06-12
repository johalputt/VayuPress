package api

import (
	"errors"
	"net/http"
)

// Sentinel errors returned by ArticleService methods. Handlers map these to
// HTTP status codes via HTTPStatus and ErrorCode rather than checking literals.
var (
	ErrNotFound     = errors.New("not found")
	ErrSlugConflict = errors.New("slug already exists")
	ErrInvalidSlug  = errors.New("invalid slug")
	ErrStorageQuota = errors.New("storage quota exceeded")
	ErrBulkLimit    = errors.New("max 1000 articles per request")
)

// HTTPStatus maps a service error to an HTTP status code.
func HTTPStatus(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrSlugConflict):
		return http.StatusConflict
	case errors.Is(err, ErrInvalidSlug):
		return http.StatusBadRequest
	case errors.Is(err, ErrStorageQuota):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrBulkLimit):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// ErrorCode maps a service error to a machine-readable code string.
func ErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrSlugConflict):
		return "slug_conflict"
	case errors.Is(err, ErrInvalidSlug):
		return "invalid_slug"
	case errors.Is(err, ErrStorageQuota):
		return "storage_quota_exceeded"
	case errors.Is(err, ErrBulkLimit):
		return "too_many_articles"
	default:
		return "internal_error"
	}
}
