package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/grishalr/courier-social/internal/registry"
)

type Handlers struct {
	reg       *registry.Registry
	resolveDID func(handle string) (string, error)
	onRegister func(did string) // callback to add DID to watcher
	onRemove   func(did string) // callback to remove DID from watcher
}

func New(reg *registry.Registry, resolveDID func(string) (string, error), onRegister, onRemove func(string)) *Handlers {
	return &Handlers{
		reg:        reg,
		resolveDID: resolveDID,
		onRegister: onRegister,
		onRemove:   onRemove,
	}
}

type RegisterRequest struct {
	Handle      string                `json:"handle"`
	DID         string                `json:"did"`
	DeviceToken string                `json:"deviceToken"`
	Platform    string                `json:"platform"`
	Preferences *registry.Preferences `json:"preferences,omitempty"`
}

func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := decodeBody(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.DeviceToken == "" || req.Platform == "" {
		httpError(w, http.StatusBadRequest, "deviceToken and platform are required")
		return
	}
	log.Printf("register: did=%s token=%s… platform=%s", req.DID, req.DeviceToken[:8], req.Platform)
	if req.Platform != "ios" && req.Platform != "android" {
		httpError(w, http.StatusBadRequest, "platform must be 'ios' or 'android'")
		return
	}

	// Resolve DID if only handle provided
	did := req.DID
	handle := req.Handle
	if did == "" && handle == "" {
		httpError(w, http.StatusBadRequest, "handle or did is required")
		return
	}
	if did == "" {
		resolved, err := h.resolveDID(handle)
		if err != nil {
			httpError(w, http.StatusBadRequest, fmt.Sprintf("could not resolve handle: %v", err))
			return
		}
		did = resolved
	}
	if handle == "" {
		handle = did // we'll hydrate the handle later
	}

	prefs := registry.DefaultPreferences()
	if req.Preferences != nil {
		prefs = *req.Preferences
	}

	user := &registry.User{
		DID:         did,
		Handle:      handle,
		DeviceToken: req.DeviceToken,
		Platform:    req.Platform,
		Preferences: prefs,
	}

	if err := h.reg.Register(r.Context(), user); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to register")
		return
	}

	if h.onRegister != nil {
		h.onRegister(did)
	}

	jsonResp(w, http.StatusCreated, map[string]string{"did": did, "status": "registered"})
}

func (h *Handlers) UpdatePreferences(w http.ResponseWriter, r *http.Request) {
	var prefs registry.Preferences
	if err := decodeBody(r, &prefs); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	did := r.Header.Get("X-DID")
	if did == "" {
		httpError(w, http.StatusBadRequest, "X-DID header required")
		return
	}

	if err := h.reg.UpdatePreferences(r.Context(), did, prefs); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to update preferences")
		return
	}

	jsonResp(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handlers) UpdateDeviceToken(w http.ResponseWriter, r *http.Request) {
	did := AuthedDID(r)
	if did == "" {
		httpError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		DeviceToken string `json:"deviceToken"`
	}
	if err := decodeBody(r, &req); err != nil || req.DeviceToken == "" {
		httpError(w, http.StatusBadRequest, "deviceToken is required")
		return
	}
	if err := h.reg.UpdateDeviceToken(r.Context(), did, req.DeviceToken); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to update token")
		return
	}
	log.Printf("token refresh: %s → %s…", did, req.DeviceToken[:8])
	jsonResp(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handlers) Unregister(w http.ResponseWriter, r *http.Request) {
	did := r.Header.Get("X-DID")
	if did == "" {
		httpError(w, http.StatusBadRequest, "X-DID header required")
		return
	}

	if err := h.reg.Unregister(r.Context(), did); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to unregister")
		return
	}

	if h.onRemove != nil {
		h.onRemove(did)
	}

	jsonResp(w, http.StatusOK, map[string]string{"status": "unregistered"})
}

func (h *Handlers) GetNotifications(w http.ResponseWriter, r *http.Request) {
	did := chi.URLParam(r, "did")
	if did == "" {
		httpError(w, http.StatusBadRequest, "did is required")
		return
	}

	notifs, err := h.reg.GetNotifications(r.Context(), did)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to fetch notifications")
		return
	}

	jsonResp(w, http.StatusOK, notifs)
}

func (h *Handlers) ClearNotifications(w http.ResponseWriter, r *http.Request) {
	did := AuthedDID(r)
	if did == "" {
		httpError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if err := h.reg.ClearNotifications(r.Context(), did); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to clear notifications")
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"status": "cleared"})
}

func (h *Handlers) DeleteNotification(w http.ResponseWriter, r *http.Request) {
	did := AuthedDID(r)
	if did == "" {
		httpError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		URI string `json:"uri"`
	}
	if err := decodeBody(r, &req); err != nil || req.URI == "" {
		httpError(w, http.StatusBadRequest, "uri is required")
		return
	}
	if err := h.reg.DeleteNotification(r.Context(), did, req.URI); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to delete notification")
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ResolveDID resolves a Bluesky handle to a DID via the public API.
func ResolveDID(bskyAPIURL string) func(string) (string, error) {
	return func(handle string) (string, error) {
		handle = strings.TrimPrefix(handle, "@")
		url := fmt.Sprintf("%s/xrpc/com.atproto.identity.resolveHandle?handle=%s", bskyAPIURL, handle)
		resp, err := http.Get(url)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return "", fmt.Errorf("resolve failed: %s", string(body))
		}
		var result struct {
			DID string `json:"did"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", err
		}
		return result.DID, nil
	}
}

func decodeBody(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func jsonResp(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
