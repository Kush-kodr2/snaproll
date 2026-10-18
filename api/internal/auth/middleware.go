package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type Principal struct {
	ID   int64
	Role string
}

type ctxKey struct{}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// Authenticated rejects requests without a valid "Authorization: Bearer <token>".
// The role comes from the token, so a role change takes effect when it expires.
func Authenticated(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeErr(w, http.StatusUnauthorized, "missing bearer token")
				return
			}
			claims, err := Parse(secret, tokenStr)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}
			id, err := strconv.ParseInt(claims.Subject, 10, 64)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "invalid token")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKey{}, Principal{ID: id, Role: claims.Role})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AdminOnly must be wrapped inside Authenticated.
func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := FromContext(r.Context())
		if !ok || p.Role != "admin" {
			writeErr(w, http.StatusForbidden, "admin only")
			return
		}
		next.ServeHTTP(w, r)
	})
}