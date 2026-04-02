package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/grishalr/courier-social/internal/registry"
)

type AppHandlers struct {
	reg *registry.Registry
}

func NewAppHandlers(reg *registry.Registry) *AppHandlers {
	return &AppHandlers{reg: reg}
}

// GET /apps — list all registered apps
func (h *AppHandlers) ListApps(w http.ResponseWriter, r *http.Request) {
	apps, err := h.reg.GetAllApps(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to list apps")
		return
	}
	jsonResp(w, http.StatusOK, apps)
}

type SuggestAppRequest struct {
	Collection string `json:"collection"`
	AppName    string `json:"appName"`
	AppURL     string `json:"appURL"`
}

// POST /apps/suggest — user suggests a new app
func (h *AppHandlers) SuggestApp(w http.ResponseWriter, r *http.Request) {
	var req SuggestAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Collection == "" || req.AppName == "" {
		httpError(w, http.StatusBadRequest, "collection and appName are required")
		return
	}

	did := r.Header.Get("X-DID")

	if err := h.reg.AddAppSuggestion(r.Context(), req.Collection, req.AppName, req.AppURL, did); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to save suggestion")
		return
	}

	jsonResp(w, http.StatusCreated, map[string]string{"status": "submitted"})
}

// GET /apps/lookup?collection=... — lookup app for a collection
func (h *AppHandlers) LookupApp(w http.ResponseWriter, r *http.Request) {
	collection := r.URL.Query().Get("collection")
	if collection == "" {
		httpError(w, http.StatusBadRequest, "collection query param required")
		return
	}

	app, err := h.reg.GetAppByCollection(r.Context(), collection)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if app == nil {
		jsonResp(w, http.StatusOK, map[string]interface{}{"found": false})
		return
	}
	jsonResp(w, http.StatusOK, app)
}
