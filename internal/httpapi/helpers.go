package httpapi

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"
)

func subtleTokenMismatch(left, right string) bool {
	if len(left) != len(right) {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) != 1
}

func timeboxedContext(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), timeout)
}

func detachedContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}
