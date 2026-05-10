package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/grishalr/courier-social/internal/subscriptions"
)

type BlogHandlers struct {
	subMgr *subscriptions.Manager
}

func NewBlogHandlers(subMgr *subscriptions.Manager) *BlogHandlers {
	return &BlogHandlers{subMgr: subMgr}
}

// GetBlogSubs returns the authenticated user's blog subscriptions.
func (h *BlogHandlers) GetBlogSubs(w http.ResponseWriter, r *http.Request) {
	did := AuthedDID(r)
	if did == "" {
		httpError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	subs, err := h.subMgr.GetUserSubs(r.Context(), did)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to fetch subscriptions")
		return
	}
	if subs == nil {
		subs = []subscriptions.BlogSub{}
	}
	jsonResp(w, http.StatusOK, subs)
}

// SetBlogPref toggles notifications for a specific blog subscription.
func (h *BlogHandlers) SetBlogPref(w http.ResponseWriter, r *http.Request) {
	did := AuthedDID(r)
	if did == "" {
		httpError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		PublicationURI string `json:"publicationUri"`
		Enabled        bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PublicationURI == "" {
		httpError(w, http.StatusBadRequest, "publicationUri is required")
		return
	}

	if err := h.subMgr.SetBlogEnabled(r.Context(), did, req.PublicationURI, req.Enabled); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to update preference")
		return
	}

	jsonResp(w, http.StatusOK, map[string]string{"status": "updated"})
}

// RefreshBlogSubs re-discovers the user's blog subscriptions from their PDS.
func (h *BlogHandlers) RefreshBlogSubs(w http.ResponseWriter, r *http.Request) {
	did := AuthedDID(r)
	if did == "" {
		httpError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if err := h.subMgr.DiscoverAndStore(r.Context(), did); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to refresh subscriptions")
		return
	}

	// Return the updated list
	subs, _ := h.subMgr.GetUserSubs(r.Context(), did)
	if subs == nil {
		subs = []subscriptions.BlogSub{}
	}
	jsonResp(w, http.StatusOK, subs)
}
