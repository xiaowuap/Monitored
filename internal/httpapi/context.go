package httpapi

import (
	"context"
	"net/http"
)

type actorContextKey struct{}

func withActor(r *http.Request, actor identity) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), actorContextKey{}, actor))
}

func actorFrom(r *http.Request) identity {
	if actor, ok := r.Context().Value(actorContextKey{}).(identity); ok {
		return actor
	}
	return identity{Email: "unknown", Role: "viewer"}
}
