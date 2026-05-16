package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/grishalr/courier-social/internal/oauth"
	"github.com/grishalr/courier-social/internal/registry"
)

type contextKey string

const authedDIDKey contextKey = "authedDID"

// RequireAuth middleware verifies the caller has a valid session.
// Accepts either:
//   - Authorization: Bearer <session-token> (from OAuth flow)
//   - X-DID header with a previously verified DID (from challenge-verify flow)
//
// On success, sets the authenticated DID in the request context.
func RequireAuth(sessions *oauth.SessionStore, reg *registry.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try Bearer token first (OAuth sessions)
			if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				token := strings.TrimPrefix(auth, "Bearer ")
				session, err := sessions.GetSession(r.Context(), token)
				if err == nil && session.DID != "" {
					ctx := context.WithValue(r.Context(), authedDIDKey, session.DID)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			// Try session cookie
			if cookie, err := r.Cookie("session"); err == nil {
				session, err := sessions.GetSession(r.Context(), cookie.Value)
				if err == nil && session.DID != "" {
					ctx := context.WithValue(r.Context(), authedDIDKey, session.DID)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			// Try X-DID header (iOS app, after challenge-verify)
			if did := r.Header.Get("X-DID"); did != "" && reg != nil {
				if user, err := reg.GetUser(r.Context(), did); err == nil && user != nil {
					ctx := context.WithValue(r.Context(), authedDIDKey, did)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			httpError(w, http.StatusUnauthorized, "authentication required")
		})
	}
}

// AuthedDID returns the authenticated DID from the request context.
func AuthedDID(r *http.Request) string {
	did, _ := r.Context().Value(authedDIDKey).(string)
	return did
}
