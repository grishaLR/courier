package subscriptions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/grishalr/courier-social/internal/registry"
)

// BlogSub represents a single blog subscription for a user.
type BlogSub struct {
	PublicationURI string `json:"publicationUri"` // at://did/collection/rkey
	AuthorDID      string `json:"authorDid"`
	BlogName       string `json:"blogName"`
	Platform       string `json:"platform"`       // app name from catalog
	WebURL         string `json:"webUrl,omitempty"`
	IconURL        string `json:"iconUrl,omitempty"`
	Enabled        bool   `json:"enabled"`
}

// Manager handles blog subscription discovery, storage, and lookup.
type Manager struct {
	rdb        *redis.Client
	httpClient *http.Client

	// In-memory reverse index: authorDID → set of subscriber DIDs
	// Rebuilt on startup, updated live via Jetstream events
	mu       sync.RWMutex
	watchers map[string]map[string]bool // authorDID → {subscriberDID: true}
}

// Subscription collection NSIDs we care about
var subscriptionCollections = []string{
	"pub.leaflet.graph.subscription",
	"site.standard.graph.subscription",
}

// Document collection NSIDs that produce blog post notifications
var documentCollections = map[string]string{
	"pub.leaflet.document":  "leaflet",
	"site.standard.document": "standard",
	"com.whtwnd.blog.entry": "whitewind",
}

func New(rdb *redis.Client) *Manager {
	return &Manager{
		rdb: rdb,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		watchers: make(map[string]map[string]bool),
	}
}

// Redis key helpers
func userSubsKey(did string) string     { return "blog_subs:" + did }
func authorWatchKey(did string) string   { return "blog_watchers:" + did }
func blogPrefsKey(did string) string     { return "blog_prefs:" + did }

// IsDocumentCollection returns the platform name if the collection is a blog document type.
func IsDocumentCollection(collection string) (string, bool) {
	platform, ok := documentCollections[collection]
	return platform, ok
}

// IsSubscriptionCollection returns true if this collection is a blog subscription record.
func IsSubscriptionCollection(collection string) bool {
	for _, col := range subscriptionCollections {
		if collection == col {
			return true
		}
	}
	return false
}

// GetSubscribers returns all subscriber DIDs for a given blog author.
func (m *Manager) GetSubscribers(authorDID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	subs, ok := m.watchers[authorDID]
	if !ok {
		return nil
	}
	result := make([]string, 0, len(subs))
	for did := range subs {
		result = append(result, did)
	}
	return result
}

// IsSubscribed checks if a user is subscribed to a blog author (in-memory).
func (m *Manager) IsSubscribed(subscriberDID, authorDID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if subs, ok := m.watchers[authorDID]; ok {
		return subs[subscriberDID]
	}
	return false
}

// IsBlogEnabled checks if a user has enabled notifications for a specific blog.
// Returns true by default (new subscriptions start enabled).
func (m *Manager) IsBlogEnabled(ctx context.Context, subscriberDID, publicationURI string) bool {
	val, err := m.rdb.HGet(ctx, blogPrefsKey(subscriberDID), publicationURI).Result()
	if err == redis.Nil {
		return true // default: enabled
	}
	if err != nil {
		return true
	}
	return val == "1"
}

// SetBlogEnabled toggles notifications for a specific blog subscription.
func (m *Manager) SetBlogEnabled(ctx context.Context, subscriberDID, publicationURI string, enabled bool) error {
	val := "0"
	if enabled {
		val = "1"
	}
	return m.rdb.HSet(ctx, blogPrefsKey(subscriberDID), publicationURI, val).Err()
}

// GetUserSubs returns all blog subscriptions for a user.
func (m *Manager) GetUserSubs(ctx context.Context, did string) ([]BlogSub, error) {
	val, err := m.rdb.Get(ctx, userSubsKey(did)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var subs []BlogSub
	if err := json.Unmarshal([]byte(val), &subs); err != nil {
		return nil, err
	}

	// Populate enabled state from prefs
	for i := range subs {
		subs[i].Enabled = m.IsBlogEnabled(ctx, did, subs[i].PublicationURI)
	}
	return subs, nil
}

// DiscoverAndStore fetches a user's blog subscriptions from their PDS and stores them.
// This is called on user registration and can be refreshed periodically.
func (m *Manager) DiscoverAndStore(ctx context.Context, did string) error {
	pds := m.resolvePDS(ctx, did)
	if pds == "" {
		return fmt.Errorf("could not resolve PDS for %s", did)
	}

	var allSubs []BlogSub

	for _, collection := range subscriptionCollections {
		subs, err := m.fetchSubscriptions(ctx, pds, did, collection)
		if err != nil {
			log.Printf("subscriptions: error fetching %s for %s: %v", collection, did, err)
			continue
		}
		allSubs = append(allSubs, subs...)
	}

	if len(allSubs) == 0 {
		return nil
	}

	// Store in Redis
	data, _ := json.Marshal(allSubs)
	if err := m.rdb.Set(ctx, userSubsKey(did), string(data), 365*24*time.Hour).Err(); err != nil {
		return err
	}

	// Update reverse index
	m.mu.Lock()
	for _, sub := range allSubs {
		if m.watchers[sub.AuthorDID] == nil {
			m.watchers[sub.AuthorDID] = make(map[string]bool)
		}
		m.watchers[sub.AuthorDID][did] = true
	}
	m.mu.Unlock()

	// Also persist reverse index to Redis for crash recovery
	for _, sub := range allSubs {
		m.rdb.SAdd(ctx, authorWatchKey(sub.AuthorDID), did)
	}

	log.Printf("subscriptions: discovered %d blog subs for %s", len(allSubs), did)
	return nil
}

// HandleNewSubscription processes a new subscription record from Jetstream.
func (m *Manager) HandleNewSubscription(ctx context.Context, subscriberDID, collection string, record json.RawMessage) {
	var rec struct {
		Publication string `json:"publication"`
	}
	if err := json.Unmarshal(record, &rec); err != nil || rec.Publication == "" {
		return
	}

	authorDID, blogName, platform, iconURL, webURL := m.resolvePublication(ctx, rec.Publication, collection)
	if authorDID == "" {
		return
	}

	sub := BlogSub{
		PublicationURI: rec.Publication,
		AuthorDID:      authorDID,
		BlogName:       blogName,
		Platform:       platform,
		WebURL:         webURL,
		IconURL:        iconURL,
		Enabled:        true,
	}

	// Add to user's sub list
	subs, _ := m.GetUserSubs(ctx, subscriberDID)
	// Check for duplicates
	for _, existing := range subs {
		if existing.PublicationURI == sub.PublicationURI {
			return // already tracked
		}
	}
	subs = append(subs, sub)
	data, _ := json.Marshal(subs)
	m.rdb.Set(ctx, userSubsKey(subscriberDID), string(data), 365*24*time.Hour)

	// Update reverse index
	m.mu.Lock()
	if m.watchers[authorDID] == nil {
		m.watchers[authorDID] = make(map[string]bool)
	}
	m.watchers[authorDID][subscriberDID] = true
	m.mu.Unlock()
	m.rdb.SAdd(ctx, authorWatchKey(authorDID), subscriberDID)

	log.Printf("subscriptions: %s subscribed to %s (%s)", subscriberDID, blogName, platform)
}

// HandleDeleteSubscription processes a deleted subscription record.
func (m *Manager) HandleDeleteSubscription(ctx context.Context, subscriberDID, collection, rkey string) {
	subs, _ := m.GetUserSubs(ctx, subscriberDID)
	if len(subs) == 0 {
		return
	}

	// Find and remove the subscription matching this rkey
	// The subscription URI would be at://subscriberDID/collection/rkey
	subURI := fmt.Sprintf("at://%s/%s/%s", subscriberDID, collection, rkey)
	var updated []BlogSub
	var removedAuthor string
	for _, s := range subs {
		// Match by publication URI or by the subscription record URI
		if s.PublicationURI == subURI {
			removedAuthor = s.AuthorDID
			continue
		}
		updated = append(updated, s)
	}

	// If we couldn't match by URI, we can't remove — the sub record itself
	// doesn't store enough info to match. This is a best-effort cleanup.
	if removedAuthor == "" {
		return
	}

	data, _ := json.Marshal(updated)
	m.rdb.Set(ctx, userSubsKey(subscriberDID), string(data), 365*24*time.Hour)

	// Check if subscriber still follows this author via another publication
	stillFollowing := false
	for _, s := range updated {
		if s.AuthorDID == removedAuthor {
			stillFollowing = true
			break
		}
	}
	if !stillFollowing {
		m.mu.Lock()
		if subs, ok := m.watchers[removedAuthor]; ok {
			delete(subs, subscriberDID)
			if len(subs) == 0 {
				delete(m.watchers, removedAuthor)
			}
		}
		m.mu.Unlock()
		m.rdb.SRem(ctx, authorWatchKey(removedAuthor), subscriberDID)
	}
}

// RebuildFromRedis loads the reverse index from Redis on startup.
func (m *Manager) RebuildFromRedis(ctx context.Context, watchedDIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, did := range watchedDIDs {
		val, err := m.rdb.Get(ctx, userSubsKey(did)).Result()
		if err != nil {
			continue
		}
		var subs []BlogSub
		if err := json.Unmarshal([]byte(val), &subs); err != nil {
			continue
		}
		for _, sub := range subs {
			if m.watchers[sub.AuthorDID] == nil {
				m.watchers[sub.AuthorDID] = make(map[string]bool)
			}
			m.watchers[sub.AuthorDID][did] = true
			count++
		}
	}
	if count > 0 {
		log.Printf("subscriptions: rebuilt reverse index — %d subscriptions across %d users", count, len(m.watchers))
	}
	return nil
}

// PublicationInfo holds resolved metadata about a publication.
type PublicationInfo struct {
	Name    string `json:"name"`
	IconCID string `json:"iconCid,omitempty"` // blob CID for the icon
	IconURL string `json:"iconUrl,omitempty"` // constructed CDN/PDS URL
}

// ResolvePublicationInfo fetches the publication record to get name and icon.
func (m *Manager) ResolvePublicationInfo(ctx context.Context, pubURI string) *PublicationInfo {
	if !strings.HasPrefix(pubURI, "at://") {
		return nil
	}

	parts := strings.SplitN(strings.TrimPrefix(pubURI, "at://"), "/", 3)
	if len(parts) < 3 {
		return nil
	}
	authorDID := parts[0]
	collection := parts[1]
	rkey := parts[2]

	// Check cache
	cacheKey := "pub_info:" + pubURI
	if val, err := m.rdb.Get(ctx, cacheKey).Result(); err == nil {
		var info PublicationInfo
		if json.Unmarshal([]byte(val), &info) == nil {
			return &info
		}
	}

	pds := m.resolvePDS(ctx, authorDID)
	if pds == "" {
		return nil
	}

	url := fmt.Sprintf("%s/xrpc/com.atproto.repo.getRecord?repo=%s&collection=%s&rkey=%s",
		pds, authorDID, collection, rkey)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var record struct {
		Value struct {
			Name string `json:"name"`
			Icon *struct {
				Type string `json:"$type"`
				Ref  *struct {
					Link string `json:"$link"`
				} `json:"ref"`
			} `json:"icon,omitempty"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &record); err != nil {
		return nil
	}

	info := &PublicationInfo{
		Name: record.Value.Name,
	}

	// Construct blob URL for the icon
	if record.Value.Icon != nil && record.Value.Icon.Ref != nil && record.Value.Icon.Ref.Link != "" {
		info.IconCID = record.Value.Icon.Ref.Link
		// Use the PDS blob endpoint — works for all ATProto PDSes
		info.IconURL = fmt.Sprintf("%s/xrpc/com.atproto.sync.getBlob?did=%s&cid=%s", pds, authorDID, info.IconCID)
	}

	// Cache for 24h
	data, _ := json.Marshal(info)
	m.rdb.Set(ctx, cacheKey, string(data), 24*time.Hour)

	return info
}

// FindPublicationForDocument resolves which publication a document belongs to,
// so we can match it against subscription records.
func (m *Manager) FindPublicationForDocument(ctx context.Context, authorDID, collection string, record json.RawMessage) string {
	// Leaflet documents have a "publication" field (AT-URI)
	// Standard documents have a "site" field (AT-URI or HTTPS URL)
	var doc struct {
		Publication string `json:"publication"` // leaflet
		Site        string `json:"site"`        // standard
	}
	if err := json.Unmarshal(record, &doc); err != nil {
		return ""
	}

	if doc.Publication != "" {
		return doc.Publication
	}
	if doc.Site != "" && strings.HasPrefix(doc.Site, "at://") {
		return doc.Site
	}
	return ""
}

// fetchSubscriptions fetches all subscription records from a user's PDS.
func (m *Manager) fetchSubscriptions(ctx context.Context, pds, did, collection string) ([]BlogSub, error) {
	var subs []BlogSub
	cursor := ""

	for {
		url := fmt.Sprintf("%s/xrpc/com.atproto.repo.listRecords?repo=%s&collection=%s&limit=100",
			pds, did, collection)
		if cursor != "" {
			url += "&cursor=" + cursor
		}

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return subs, err
		}

		resp, err := m.httpClient.Do(req)
		if err != nil {
			return subs, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return subs, fmt.Errorf("listRecords %s: status %d", collection, resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return subs, err
		}

		var result struct {
			Records []struct {
				URI   string          `json:"uri"`
				Value json.RawMessage `json:"value"`
			} `json:"records"`
			Cursor string `json:"cursor"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return subs, err
		}

		for _, rec := range result.Records {
			var val struct {
				Publication string `json:"publication"`
			}
			if err := json.Unmarshal(rec.Value, &val); err != nil || val.Publication == "" {
				continue
			}

			authorDID, blogName, platform, iconURL, webURL := m.resolvePublication(ctx, val.Publication, collection)
			if authorDID == "" {
				// At minimum, extract DID from the publication AT-URI
				parts := strings.SplitN(strings.TrimPrefix(val.Publication, "at://"), "/", 3)
				if len(parts) >= 1 {
					authorDID = parts[0]
				}
			}
			if authorDID == "" {
				continue
			}

			subs = append(subs, BlogSub{
				PublicationURI: val.Publication,
				AuthorDID:      authorDID,
				BlogName:       blogName,
				Platform:       platform,
				WebURL:         webURL,
				IconURL:        iconURL,
				Enabled:        true,
			})
		}

		if result.Cursor == "" || len(result.Records) == 0 {
			break
		}
		cursor = result.Cursor
	}

	return subs, nil
}

// resolvePublication fetches a publication record to get the author DID, name, platform, and icon.
func (m *Manager) resolvePublication(ctx context.Context, pubURI, subCollection string) (authorDID, blogName, platform, iconURL, webURL string) {
	if !strings.HasPrefix(pubURI, "at://") {
		return "", "", "", "", ""
	}

	parts := strings.SplitN(strings.TrimPrefix(pubURI, "at://"), "/", 3)
	if len(parts) < 3 {
		return "", "", "", "", ""
	}
	authorDID = parts[0]
	collection := parts[1]
	rkey := parts[2]

	// Determine platform from subscription or publication collection
	switch {
	case strings.Contains(collection, "leaflet") || strings.Contains(subCollection, "leaflet"):
		platform = "leaflet"
	case strings.Contains(collection, "standard") || strings.Contains(subCollection, "standard"):
		platform = "standard"
	default:
		platform = "blog"
	}

	// Fetch the publication record for the name and icon
	pds := m.resolvePDS(ctx, authorDID)
	if pds == "" {
		return authorDID, "", platform, "", ""
	}

	url := fmt.Sprintf("%s/xrpc/com.atproto.repo.getRecord?repo=%s&collection=%s&rkey=%s",
		pds, authorDID, collection, rkey)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return authorDID, "", platform, "", ""
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return authorDID, "", platform, "", ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return authorDID, "", platform, "", ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return authorDID, "", platform, "", ""
	}

	var record struct {
		Value struct {
			Name string `json:"name"`
			URL  string `json:"url"`
			Icon *struct {
				Ref *struct {
					Link string `json:"$link"`
				} `json:"ref"`
			} `json:"icon,omitempty"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &record); err != nil {
		return authorDID, "", platform, "", ""
	}

	if record.Value.Icon != nil && record.Value.Icon.Ref != nil && record.Value.Icon.Ref.Link != "" {
		iconURL = fmt.Sprintf("%s/xrpc/com.atproto.sync.getBlob?did=%s&cid=%s", pds, authorDID, record.Value.Icon.Ref.Link)
	}

	// Infer platform from publication URL domain
	if record.Value.URL != "" {
		platform = platformFromURL(record.Value.URL)
		webURL = strings.TrimRight(record.Value.URL, "/")
	}

	return authorDID, record.Value.Name, platform, iconURL, webURL
}

// platformFromURL infers the app name from a publication's web URL by matching
// against the app catalog. Falls back to the domain name if no match.
func platformFromURL(pubURL string) string {
	pubDomain := extractDomain(pubURL)
	if pubDomain == "" {
		return "blog"
	}

	// Match against catalog app URLs
	for _, app := range registry.CatalogAll() {
		appDomain := extractDomain(app.AppURL)
		if appDomain != "" && (pubDomain == appDomain || strings.HasSuffix(pubDomain, "."+appDomain)) {
			return app.AppName
		}
	}

	// No catalog match — use the domain as the name
	return pubDomain
}

func extractDomain(u string) string {
	u = strings.ToLower(u)
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	if i := strings.Index(u, "/"); i >= 0 {
		u = u[:i]
	}
	return u
}

// resolvePDS finds the PDS endpoint for a DID.
func (m *Manager) resolvePDS(ctx context.Context, did string) string {
	// Check Redis cache first
	cacheKey := "pds:" + did
	if pds, err := m.rdb.Get(ctx, cacheKey).Result(); err == nil {
		return pds
	}

	var docURL string
	if strings.HasPrefix(did, "did:plc:") {
		docURL = fmt.Sprintf("https://plc.directory/%s", did)
	} else if strings.HasPrefix(did, "did:web:") {
		domain := strings.TrimPrefix(did, "did:web:")
		domain = strings.ReplaceAll(domain, ":", "/")
		docURL = fmt.Sprintf("https://%s/.well-known/did.json", domain)
	} else {
		return ""
	}

	req, err := http.NewRequestWithContext(ctx, "GET", docURL, nil)
	if err != nil {
		return ""
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var doc struct {
		Service []struct {
			Type            string `json:"type"`
			ServiceEndpoint string `json:"serviceEndpoint"`
		} `json:"service"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return ""
	}

	for _, svc := range doc.Service {
		if svc.Type == "AtprotoPersonalDataServer" {
			m.rdb.Set(ctx, cacheKey, svc.ServiceEndpoint, 24*time.Hour)
			return svc.ServiceEndpoint
		}
	}
	return ""
}
