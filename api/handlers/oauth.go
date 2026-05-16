package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/grishalr/courier-social/internal/oauth"
)

type OAuthHandlers struct {
	sessions          *oauth.SessionStore
	clientMetadataURL string
	redirectURI       string
	baseURL           string
}

func NewOAuthHandlers(sessions *oauth.SessionStore, baseURL string) *OAuthHandlers {
	var clientMetadataURL, redirectURI string
	if strings.HasPrefix(baseURL, "http://localhost") {
		// ATProto OAuth requires 127.0.0.1 per RFC 8252, not "localhost"
		loopback := strings.Replace(baseURL, "localhost", "127.0.0.1", 1)
		redirectURI = loopback + "/auth/callback"
		clientMetadataURL = loopback + "?redirect_uri=" + redirectURI + "&scope=atproto"
	} else {
		clientMetadataURL = "https://courier.social/oauth-client-metadata.json"
		redirectURI = "https://api.courier.social/auth/callback"
	}
	return &OAuthHandlers{
		sessions:          sessions,
		clientMetadataURL: clientMetadataURL,
		redirectURI:       redirectURI,
		baseURL:           baseURL,
	}
}

// GET /oauth-client-metadata.json
func (h *OAuthHandlers) ClientMetadata(w http.ResponseWriter, r *http.Request) {
	meta := map[string]interface{}{
		"client_id":                    h.clientMetadataURL,
		"client_name":                 "Courier",
		"client_uri":                  h.baseURL,
		"redirect_uris":              []string{h.redirectURI, "social.courier:/auth/callback", "social.courier.app:/oauth-redirect"},
		"scope":                       "atproto",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"dpop_bound_access_tokens":   true,
	}
	jsonResp(w, http.StatusOK, meta)
}

// POST /auth/oauth/start — starts the OAuth flow
func (h *OAuthHandlers) Start(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Handle string `json:"handle"`
		Mobile bool   `json:"mobile,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Handle == "" {
		httpError(w, http.StatusBadRequest, "handle is required")
		return
	}

	// Resolve handle → DID → PDS
	did, err := oauth.ResolveHandle(req.Handle)
	if err != nil {
		httpError(w, http.StatusBadRequest, "could not resolve handle: "+err.Error())
		return
	}

	pdsURL, err := oauth.ResolvePDS(did)
	if err != nil {
		httpError(w, http.StatusBadRequest, "could not resolve PDS: "+err.Error())
		return
	}

	authServer, err := oauth.FetchAuthServerMetadata(pdsURL)
	if err != nil {
		httpError(w, http.StatusBadGateway, "could not fetch auth server metadata")
		return
	}

	// Generate PKCE + DPoP
	verifier, challenge, err := oauth.GeneratePKCE()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "pkce generation failed")
		return
	}
	dpopKey, err := oauth.GenerateDPoPKey()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "key generation failed")
		return
	}

	// For mobile clients use the production client_id (publicly fetchable by the PDS)
	// and a custom URL scheme redirect (intercepted by the iOS/Android app directly,
	// no server-side callback hop needed). This avoids the 127.0.0.1 loopback problem
	// where the PDS can't fetch local client metadata and falls back to production metadata.
	clientMetaURL := h.clientMetadataURL
	redirectURI := h.redirectURI
	if req.Mobile {
		clientMetaURL = "https://courier.social/oauth-client-metadata.json"
		redirectURI = "social.courier:/auth/callback"
	}

	// PAR request
	requestURI, state, err := oauth.PushAuthorizationRequest(authServer, clientMetaURL, redirectURI, challenge, req.Handle, dpopKey)
	if err != nil {
		log.Printf("oauth: PAR failed: %v", err)
		httpError(w, http.StatusBadGateway, "authorization request failed: "+err.Error())
		return
	}

	// Store state in Redis keyed by the state UUID (matches callback)
	oauthState := &oauth.OAuthState{
		CodeVerifier:  verifier,
		DPoP:          oauth.DPoPKeyToState(dpopKey),
		DID:           did,
		AuthServer:    *authServer,
		Mobile:        req.Mobile,
		RedirectURI:   redirectURI,
		ClientMetaURL: clientMetaURL,
	}
	if err := h.sessions.SaveOAuthState(r.Context(), state, oauthState); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to save state")
		return
	}

	authURL := oauth.BuildAuthorizationURL(authServer, clientMetaURL, requestURI)

	jsonResp(w, http.StatusOK, map[string]string{
		"authorizationURL": authURL,
		"state":            state,
	})
}

// GET /auth/callback — handles the OAuth redirect
func (h *OAuthHandlers) Callback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		httpError(w, http.StatusBadRequest, "missing code or state")
		return
	}

	oauthState, err := h.sessions.GetOAuthState(r.Context(), state)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid or expired state")
		return
	}

	dpopKey, err := oauth.DPoPKeyFromState(oauthState.DPoP)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to restore key")
		return
	}

	token, err := oauth.ExchangeCodeForToken(&oauthState.AuthServer, code, oauthState.CodeVerifier, h.clientMetadataURL, h.redirectURI, dpopKey)
	if err != nil {
		log.Printf("oauth: token exchange failed: %v", err)
		httpError(w, http.StatusBadGateway, "token exchange failed")
		return
	}

	if token.Sub == "" {
		httpError(w, http.StatusBadGateway, "no DID in token response")
		return
	}

	// Create session
	sessionToken, err := h.sessions.CreateSession(r.Context(), token.Sub, "")
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	// Check if this is a mobile OAuth flow (state stored with mobile flag)
	if oauthState.Mobile {
		// Redirect to the iOS app via custom URL scheme
		redirectURL := fmt.Sprintf("social.courier:/auth/callback?session=%s&did=%s",
			url.QueryEscape(sessionToken), url.QueryEscape(token.Sub))
		http.Redirect(w, r, redirectURL, http.StatusFound)
		return
	}

	// Web flow — redirect to web app
	redirectURL := fmt.Sprintf("https://courier.social/api#demo&session=%s", url.QueryEscape(sessionToken))
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// POST /auth/oauth/exchange — mobile: app received code+state via custom scheme,
// server completes the token exchange and returns a session token.
func (h *OAuthHandlers) Exchange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code  string `json:"code"`
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" || req.State == "" {
		httpError(w, http.StatusBadRequest, "code and state are required")
		return
	}

	oauthState, err := h.sessions.GetOAuthState(r.Context(), req.State)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid or expired state")
		return
	}

	dpopKey, err := oauth.DPoPKeyFromState(oauthState.DPoP)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to restore key")
		return
	}

	clientMetaURL := oauthState.ClientMetaURL
	redirectURI := oauthState.RedirectURI
	if clientMetaURL == "" {
		clientMetaURL = "https://courier.social/oauth-client-metadata.json"
	}
	if redirectURI == "" {
		redirectURI = "social.courier:/auth/callback"
	}

	token, err := oauth.ExchangeCodeForToken(&oauthState.AuthServer, req.Code, oauthState.CodeVerifier, clientMetaURL, redirectURI, dpopKey)
	if err != nil {
		log.Printf("oauth: exchange failed: %v", err)
		httpError(w, http.StatusBadGateway, "token exchange failed")
		return
	}
	if token.Sub == "" {
		httpError(w, http.StatusBadGateway, "no DID in token response")
		return
	}

	sessionToken, err := h.sessions.CreateSession(r.Context(), token.Sub, "")
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	jsonResp(w, http.StatusOK, map[string]string{
		"sessionToken": sessionToken,
		"did":          token.Sub,
	})
}

// GET /auth/session — check current session
func (h *OAuthHandlers) GetSession(w http.ResponseWriter, r *http.Request) {
	token := ""
	// Check Bearer token first, then cookie
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimPrefix(auth, "Bearer ")
	} else if cookie, err := r.Cookie("session"); err == nil {
		token = cookie.Value
	}
	if token == "" {
		httpError(w, http.StatusUnauthorized, "not logged in")
		return
	}

	session, err := h.sessions.GetSession(r.Context(), token)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "invalid session")
		return
	}

	jsonResp(w, http.StatusOK, session)
}

// POST /auth/logout — destroy session
func (h *OAuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	// Delete Bearer token session if present
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		bearerToken := strings.TrimPrefix(auth, "Bearer ")
		h.sessions.DeleteSession(r.Context(), bearerToken)
	}

	// Delete cookie session if present
	cookie, err := r.Cookie("session")
	if err == nil {
		h.sessions.DeleteSession(r.Context(), cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	jsonResp(w, http.StatusOK, map[string]string{"status": "logged out"})
}
