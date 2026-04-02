package handlers

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/grishalr/courier-social/internal/auth"
	"github.com/grishalr/courier-social/internal/oauth"
)

type AuthHandlers struct {
	authService *auth.Service
	sessions    *oauth.SessionStore
}

func NewAuthHandlers(authService *auth.Service, sessions *oauth.SessionStore) *AuthHandlers {
	return &AuthHandlers{authService: authService, sessions: sessions}
}

type ChallengeRequest struct {
	DID string `json:"did"`
}

type ChallengeResponse struct {
	Challenge string `json:"challenge"`
}

// RequestChallenge generates a nonce for a DID to sign.
// POST /auth/challenge
func (h *AuthHandlers) RequestChallenge(w http.ResponseWriter, r *http.Request) {
	var req ChallengeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.DID == "" {
		httpError(w, http.StatusBadRequest, "did is required")
		return
	}

	challenge, err := h.authService.CreateChallenge(r.Context(), req.DID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to create challenge")
		return
	}

	jsonResp(w, http.StatusOK, ChallengeResponse{Challenge: challenge})
}

type VerifyRequest struct {
	DID       string `json:"did"`
	Signature string `json:"signature"` // base64 or hex encoded
	Encoding  string `json:"encoding"`  // "base64" (default) or "hex"
}

type VerifyResponse struct {
	DID      string `json:"did"`
	Verified bool   `json:"verified"`
	Token    string `json:"token,omitempty"`
}

// VerifyChallenge checks the signed nonce.
// POST /auth/verify
func (h *AuthHandlers) VerifyChallenge(w http.ResponseWriter, r *http.Request) {
	var req VerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.DID == "" || req.Signature == "" {
		httpError(w, http.StatusBadRequest, "did and signature are required")
		return
	}

	var sig []byte
	var err error
	if req.Encoding == "hex" {
		sig, err = hex.DecodeString(req.Signature)
	} else {
		sig, err = base64.StdEncoding.DecodeString(req.Signature)
	}
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid signature encoding")
		return
	}

	if err := h.authService.VerifyChallenge(r.Context(), req.DID, sig); err != nil {
		httpError(w, http.StatusUnauthorized, err.Error())
		return
	}

	// Create session token for the verified DID
	token, err := h.sessions.CreateSession(r.Context(), req.DID, "")
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	jsonResp(w, http.StatusOK, VerifyResponse{DID: req.DID, Verified: true, Token: token})
}
