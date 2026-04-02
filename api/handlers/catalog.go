package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/grishalr/courier-social/internal/registry"
)

type CatalogHandlers struct {
	reg *registry.Registry
}

func NewCatalogHandlers(reg *registry.Registry) *CatalogHandlers {
	return &CatalogHandlers{reg: reg}
}

type AppGroup struct {
	Category string               `json:"category"`
	Apps     []registry.CatalogApp `json:"apps"`
}

type UserAppsResponse struct {
	YourApps   []AppGroup `json:"yourApps"`
	DiscoverApps []AppGroup `json:"discoverApps"`
}

// GET /catalog — full app catalog grouped by category
func (h *CatalogHandlers) GetCatalog(w http.ResponseWriter, r *http.Request) {
	catalog := registry.CatalogAll()
	groups := groupByCategory(catalog)
	jsonResp(w, http.StatusOK, map[string]interface{}{"groups": groups})
}

// GET /catalog/user — optional ?actor= (DID or handle)
// Without actor: returns all apps grouped by category
// With actor: returns split view of "your apps" and "discover"
func (h *CatalogHandlers) GetUserApps(w http.ResponseWriter, r *http.Request) {
	actor := r.URL.Query().Get("actor")
	// Support legacy ?did= param too
	if actor == "" {
		actor = r.URL.Query().Get("did")
	}

	allApps := registry.CatalogAll()

	// No actor — return everything
	if actor == "" {
		groups := groupByCategory(allApps)
		jsonResp(w, http.StatusOK, UserAppsResponse{
			YourApps:     groups,
			DiscoverApps: nil,
		})
		return
	}

	// Resolve handle to DID if needed
	did := actor
	if !strings.HasPrefix(actor, "did:") {
		resolved, err := resolveDIDFromHandle(actor)
		if err != nil {
			httpError(w, http.StatusBadRequest, fmt.Sprintf("could not resolve handle: %v", err))
			return
		}
		did = resolved
	}

	refresh := r.URL.Query().Get("refresh") == "true"

	// Check cache first
	if !refresh {
		if cached, err := h.reg.GetUserCatalog(r.Context(), did); cached != nil && err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write(cached)
			return
		}
	}

	// Build fresh catalog
	result := h.buildUserCatalog(r.Context(), did, allApps)

	// Cache it
	if data, err := json.Marshal(result); err == nil {
		h.reg.SaveUserCatalog(r.Context(), did, data)
	}

	jsonResp(w, http.StatusOK, result)
}

func (h *CatalogHandlers) buildUserCatalog(ctx context.Context, did string, allApps []registry.CatalogApp) UserAppsResponse {
	collections, _ := getUserCollections(did)
	catalogMap := registry.CatalogByCollection()

	userPrefixes := make(map[string]bool)
	for _, col := range collections {
		app := registry.MatchApp(col, catalogMap)
		if app != nil {
			userPrefixes[app.CollectionPrefix] = true
		}
	}
	pinned, _ := h.reg.GetPinnedApps(ctx, did)
	for prefix := range pinned {
		userPrefixes[prefix] = true
	}

	preferred, _ := h.reg.GetPreferredApps(ctx, did)

	var yourApps, discoverApps []registry.CatalogApp
	seen := make(map[string]bool)
	for _, app := range allApps {
		// Skip alternatives — accessed via picker
		if app.AlternativeFor != "" {
			if prefURL, ok := preferred[app.AlternativeFor]; ok && prefURL == app.AppURL {
				if !seen[app.AlternativeFor] && userPrefixes[app.AlternativeFor] {
					seen[app.AlternativeFor] = true
					yourApps = append(yourApps, app)
				}
			}
			continue
		}

		if seen[app.CollectionPrefix] {
			continue
		}

		if prefURL, hasPreferred := preferred[app.CollectionPrefix]; hasPreferred {
			if prefURL != app.AppURL {
				continue
			}
		}

		seen[app.CollectionPrefix] = true
		if userPrefixes[app.CollectionPrefix] {
			yourApps = append(yourApps, app)
		} else {
			discoverApps = append(discoverApps, app)
		}
	}

	return UserAppsResponse{
		YourApps:     groupByCategory(yourApps),
		DiscoverApps: groupByCategory(discoverApps),
	}
}

func resolveDIDFromHandle(handle string) (string, error) {
	handle = strings.TrimPrefix(handle, "@")
	url := fmt.Sprintf("https://public.api.bsky.app/xrpc/com.atproto.identity.resolveHandle?handle=%s", handle)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		DID string `json:"did"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.DID, nil
}

func groupByCategory(apps []registry.CatalogApp) []AppGroup {
	categoryOrder := []registry.AppCategory{
		registry.CategorySocial,
		registry.CategoryClient,
		registry.CategoryChat,
		registry.CategoryMessaging,
		registry.CategoryCommunities,
		registry.CategoryBlog,
		registry.CategoryPublishing,
		registry.CategoryCode,
		registry.CategoryDeveloperTools,
		registry.CategoryDeveloper,
		registry.CategoryEvents,
		registry.CategoryPhotos,
		registry.CategoryVideo,
		registry.CategoryAudio,
		registry.CategoryMedia,
		registry.CategoryNews,
		registry.CategoryFeeds,
		registry.CategoryReviews,
		registry.CategoryFood,
		registry.CategoryGaming,
		registry.CategoryLinks,
		registry.CategoryHosting,
		registry.CategoryIdentity,
		registry.CategoryTools,
		registry.CategoryMarketplace,
		registry.CategoryJobs,
		registry.CategoryBookmarks,
		registry.CategoryAnalytics,
		registry.CategorySupport,
		registry.CategoryOther,
	}

	byCategory := make(map[registry.AppCategory][]registry.CatalogApp)
	for _, app := range apps {
		byCategory[app.Category] = append(byCategory[app.Category], app)
	}

	var groups []AppGroup
	for _, cat := range categoryOrder {
		if apps, ok := byCategory[cat]; ok && len(apps) > 0 {
			sort.Slice(apps, func(i, j int) bool {
				return apps[i].AppName < apps[j].AppName
			})
			groups = append(groups, AppGroup{
				Category: string(cat),
				Apps:     apps,
			})
		}
	}
	return groups
}

// GET /catalog/user/preferred?did=... — get preferred app choices
func (h *CatalogHandlers) GetPreferredApps(w http.ResponseWriter, r *http.Request) {
	did := r.URL.Query().Get("did")
	if did == "" {
		httpError(w, http.StatusBadRequest, "did required")
		return
	}
	prefs, err := h.reg.GetPreferredApps(r.Context(), did)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to get preferred apps")
		return
	}
	if prefs == nil {
		prefs = make(map[string]string)
	}
	jsonResp(w, http.StatusOK, prefs)
}

// PUT /catalog/user/preferred — set preferred app choices
func (h *CatalogHandlers) SetPreferredApps(w http.ResponseWriter, r *http.Request) {
	did := r.Header.Get("X-DID")
	if did == "" {
		httpError(w, http.StatusBadRequest, "X-DID header required")
		return
	}
	var prefs map[string]string
	if err := json.NewDecoder(r.Body).Decode(&prefs); err != nil {
		httpError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := h.reg.SetPreferredApps(r.Context(), did, prefs); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to save")
		return
	}
	h.reg.InvalidateUserCatalog(r.Context(), did)
	jsonResp(w, http.StatusOK, map[string]string{"status": "updated"})
}

// GET /catalog/alternatives?prefix=app.bsky — get alternative apps for a collection
func (h *CatalogHandlers) GetAlternatives(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	if prefix == "" {
		httpError(w, http.StatusBadRequest, "prefix required")
		return
	}
	allApps := registry.CatalogAll()
	var alternatives []registry.CatalogApp
	for _, app := range allApps {
		if app.CollectionPrefix == prefix || app.AlternativeFor == prefix {
			alternatives = append(alternatives, app)
		}
	}
	jsonResp(w, http.StatusOK, alternatives)
}

// POST /catalog/user/pin — add app to "apps I'm on"
func (h *CatalogHandlers) PinApp(w http.ResponseWriter, r *http.Request) {
	did := r.Header.Get("X-DID")
	if did == "" {
		httpError(w, http.StatusBadRequest, "X-DID header required")
		return
	}
	var body struct {
		CollectionPrefix string `json:"collectionPrefix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CollectionPrefix == "" {
		httpError(w, http.StatusBadRequest, "collectionPrefix required")
		return
	}
	if err := h.reg.AddPinnedApp(r.Context(), did, body.CollectionPrefix); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to pin app")
		return
	}
	h.reg.InvalidateUserCatalog(r.Context(), did)
	// also enable notifications for this app
	if prefs, err := h.reg.GetAppPreferences(r.Context(), did); err == nil {
		if prefs == nil {
			prefs = make(map[string]bool)
		}
		prefs[body.CollectionPrefix] = true
		h.reg.SetAppPreferences(r.Context(), did, prefs)
	}
	jsonResp(w, http.StatusOK, map[string]string{"status": "pinned"})
}

// DELETE /catalog/user/pin — remove app from "apps I'm on"
func (h *CatalogHandlers) UnpinApp(w http.ResponseWriter, r *http.Request) {
	did := r.Header.Get("X-DID")
	if did == "" {
		httpError(w, http.StatusBadRequest, "X-DID header required")
		return
	}
	var body struct {
		CollectionPrefix string `json:"collectionPrefix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CollectionPrefix == "" {
		httpError(w, http.StatusBadRequest, "collectionPrefix required")
		return
	}
	if err := h.reg.RemovePinnedApp(r.Context(), did, body.CollectionPrefix); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to unpin app")
		return
	}
	h.reg.InvalidateUserCatalog(r.Context(), did)
	jsonResp(w, http.StatusOK, map[string]string{"status": "unpinned"})
}

// GET /catalog/user/prefs?did=... — get per-app preferences
func (h *CatalogHandlers) GetAppPrefs(w http.ResponseWriter, r *http.Request) {
	did := r.URL.Query().Get("did")
	if did == "" {
		httpError(w, http.StatusBadRequest, "did required")
		return
	}
	prefs, err := h.reg.GetAppPreferences(r.Context(), did)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to get preferences")
		return
	}
	if prefs == nil {
		prefs = make(map[string]bool)
	}
	jsonResp(w, http.StatusOK, prefs)
}

// PUT /catalog/user/prefs — set per-app preferences
func (h *CatalogHandlers) SetAppPrefs(w http.ResponseWriter, r *http.Request) {
	did := r.Header.Get("X-DID")
	if did == "" {
		httpError(w, http.StatusBadRequest, "X-DID header required")
		return
	}
	var prefs map[string]bool
	if err := json.NewDecoder(r.Body).Decode(&prefs); err != nil {
		httpError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := h.reg.SetAppPreferences(r.Context(), did, prefs); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to save")
		return
	}
	h.reg.InvalidateUserCatalog(r.Context(), did)
	jsonResp(w, http.StatusOK, map[string]string{"status": "updated"})
}

func getUserCollections(did string) ([]string, error) {
	// Resolve PDS from PLC directory
	pdsURL, err := resolvePDS(did)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/xrpc/com.atproto.repo.describeRepo?repo=%s", pdsURL, did)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Collections []string `json:"collections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Collections, nil
}

func resolvePDS(did string) (string, error) {
	var docURL string
	if strings.HasPrefix(did, "did:plc:") {
		docURL = fmt.Sprintf("https://plc.directory/%s", did)
	} else if strings.HasPrefix(did, "did:web:") {
		domain := strings.TrimPrefix(did, "did:web:")
		domain = strings.ReplaceAll(domain, ":", "/")
		docURL = fmt.Sprintf("https://%s/.well-known/did.json", domain)
	} else {
		return "", fmt.Errorf("unsupported DID method")
	}

	resp, err := http.Get(docURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var doc struct {
		Service []struct {
			Type            string `json:"type"`
			ServiceEndpoint string `json:"serviceEndpoint"`
		} `json:"service"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", err
	}

	for _, svc := range doc.Service {
		if svc.Type == "AtprotoPersonalDataServer" {
			return svc.ServiceEndpoint, nil
		}
	}
	return "", fmt.Errorf("no PDS found for %s", did)
}
