package resolver

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
)

// AppCatalogEntry holds minimal app info for deep link resolution.
type AppCatalogEntry struct {
	AppURL           string
	CollectionPrefix string
}

// Resolver resolves AT URIs into web-accessible deep links.
type Resolver struct {
	rdb        *redis.Client
	httpClient *http.Client
	plcURL     string

	// Cache PDS endpoints: DID → PDS URL
	pdsMu    sync.RWMutex
	pdsCache map[string]string

	// App catalog for fallback deep links
	catalog map[string]AppCatalogEntry
}

func New(rdb *redis.Client) *Resolver {
	return &Resolver{
		rdb:    rdb,
		plcURL: "https://plc.directory",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		pdsCache: make(map[string]string),
		catalog:  make(map[string]AppCatalogEntry),
	}
}

// SetCatalog loads app catalog entries for deep link fallback resolution.
func (r *Resolver) SetCatalog(entries map[string]AppCatalogEntry) {
	r.catalog = entries
}

// matchCatalog finds the best matching app URL for a collection.
func (r *Resolver) matchCatalog(collection string) string {
	bestLen := 0
	bestURL := ""
	for prefix, entry := range r.catalog {
		if len(prefix) > bestLen && len(collection) >= len(prefix) && collection[:len(prefix)] == prefix {
			bestLen = len(prefix)
			bestURL = entry.AppURL
		}
	}
	return bestURL
}

type ATURI struct {
	DID        string
	Collection string
	RKey       string
}

func ParseATURI(uri string) (*ATURI, error) {
	stripped := strings.TrimPrefix(uri, "at://")
	parts := strings.SplitN(stripped, "/", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid AT URI: %s", uri)
	}
	return &ATURI{DID: parts[0], Collection: parts[1], RKey: parts[2]}, nil
}

// ResolveDeepLink takes an AT URI and returns a web URL for it.
func (r *Resolver) ResolveDeepLink(ctx context.Context, atURI string) string {
	parsed, err := ParseATURI(atURI)
	if err != nil {
		return ""
	}

	handle := r.resolveHandle(ctx, parsed.DID)
	if handle == "" {
		handle = parsed.DID
	}

	// Check known collection → URL mappings
	switch {
	case strings.HasPrefix(parsed.Collection, "app.bsky.feed.post"):
		return fmt.Sprintf("https://bsky.app/profile/%s/post/%s", handle, parsed.RKey)

	case strings.HasPrefix(parsed.Collection, "app.bsky.feed.like"),
		strings.HasPrefix(parsed.Collection, "app.bsky.feed.repost"):
		// Like/repost: fetch the record to get the subject post URI
		record := r.fetchRecord(ctx, parsed)
		if record != nil {
			if subjectURI := extractJSONString(record, "subject", "uri"); subjectURI != "" {
				return r.ResolveDeepLink(ctx, subjectURI)
			}
		}
		return fmt.Sprintf("https://bsky.app/profile/%s", handle)

	case strings.HasPrefix(parsed.Collection, "app.bsky.graph.follow"):
		return fmt.Sprintf("https://bsky.app/profile/%s", handle)

	case strings.HasPrefix(parsed.Collection, "sh.tangled.repo.issue"):
		return r.resolveTangledIssue(ctx, parsed, handle)

	case strings.HasPrefix(parsed.Collection, "sh.tangled.repo.comment"):
		return r.resolveTangledComment(ctx, parsed, handle)

	case strings.HasPrefix(parsed.Collection, "sh.tangled.repo.pr"):
		return r.resolveTangledPR(ctx, parsed, handle)

	case strings.HasPrefix(parsed.Collection, "sh.tangled"):
		return fmt.Sprintf("https://tangled.org/%s", handle)

	case strings.HasPrefix(parsed.Collection, "community.lexicon.calendar"):
		// Try to extract the event's own web URL from uris array
		if record := r.fetchRecord(ctx, parsed); record != nil {
			if webURL := extractEventWebURL(record); webURL != "" {
				return webURL
			}
		}
		return fmt.Sprintf("https://atmo.rsvp/p/%s/e/%s", handle, parsed.RKey)

	case strings.HasPrefix(parsed.Collection, "com.whtwnd.blog"):
		return fmt.Sprintf("https://whtwnd.com/%s/%s", handle, parsed.RKey)

	case strings.HasPrefix(parsed.Collection, "fyi.unravel.frontpage"):
		return fmt.Sprintf("https://frontpage.fyi/post/%s/%s", handle, parsed.RKey)

	case strings.HasPrefix(parsed.Collection, "events.smokesignal"):
		return fmt.Sprintf("https://smokesignal.events/%s/%s", parsed.DID, parsed.RKey)

	case strings.HasPrefix(parsed.Collection, "blue.pico"):
		return fmt.Sprintf("https://pico.blue/%s", handle)

	case strings.HasPrefix(parsed.Collection, "pub.leaflet.interactions"),
		strings.HasPrefix(parsed.Collection, "pub.leaflet.comment"):
		// Interaction records: resolve to the subject publication page
		record := r.fetchRecord(ctx, parsed)
		if record != nil {
			if subjectURI := extractJSONString(record, "subject"); subjectURI != "" {
				if sub, err := ParseATURI(subjectURI); err == nil {
					subHandle := r.resolveHandle(ctx, sub.DID)
					if subHandle == "" {
						subHandle = sub.DID
					}
					return fmt.Sprintf("https://leaflet.pub/p/%s/%s", subHandle, sub.RKey)
				}
			}
		}
		return "https://leaflet.pub/notifications"

	case strings.HasPrefix(parsed.Collection, "pub.leaflet"):
		return "https://leaflet.pub/notifications"

	case strings.HasPrefix(parsed.Collection, "site.standard"):
		// Documents have a "site" field and optional "path" field
		// "site" can be an https:// URL or an at:// URI to a publication record
		if record := r.fetchRecord(ctx, parsed); record != nil {
			site := extractJSONString(record, "site")
			path := extractJSONString(record, "path")
			log.Printf("resolver: site.standard doc site=%q path=%q", site, path)

			// If site is an AT-URI, resolve the publication to get its web URL
			if site != "" && strings.HasPrefix(site, "at://") {
				if pubParsed, err := ParseATURI(site); err == nil {
					if pubRecord := r.fetchRecord(ctx, pubParsed); pubRecord != nil {
						if pubURL := extractJSONString(pubRecord, "url"); pubURL != "" {
							site = pubURL
						}
					}
				}
			}

			if site != "" && strings.HasPrefix(site, "https://") {
				site = strings.TrimRight(site, "/")
				if path != "" {
					if !strings.HasPrefix(path, "/") {
						path = "/" + path
					}
					return site + path
				}
				return fmt.Sprintf("%s/%s", site, parsed.RKey)
			}
		}
		return fmt.Sprintf("https://standard.site/%s/%s", handle, parsed.RKey)

	case strings.HasPrefix(parsed.Collection, "social.arabica"):
		// Likes/interactions: resolve the subject to get the brewer/recipe URI
		if record := r.fetchRecord(ctx, parsed); record != nil {
			if subjectURI := extractJSONString(record, "subject", "uri"); subjectURI != "" {
				if sub, err := ParseATURI(subjectURI); err == nil {
					subHandle := r.resolveHandle(ctx, sub.DID)
					if subHandle == "" {
						subHandle = sub.DID
					}
					// Extract the record type from collection (e.g., "brewer" from "social.arabica.alpha.brewer")
					parts := strings.Split(sub.Collection, ".")
					recordType := parts[len(parts)-1] + "s" // pluralize: brewer→brewers, recipe→recipes
					return fmt.Sprintf("https://alpha.arabica.social/%s/%s?owner=%s", recordType, sub.RKey, subHandle)
				}
			}
		}
		// Direct record (not an interaction)
		parts := strings.Split(parsed.Collection, ".")
		recordType := parts[len(parts)-1] + "s"
		return fmt.Sprintf("https://alpha.arabica.social/%s/%s?owner=%s", recordType, parsed.RKey, handle)

	case strings.HasPrefix(parsed.Collection, "blue.flashes"),
		strings.HasPrefix(parsed.Collection, "app.flashes"):
		return fmt.Sprintf("https://www.flashes.blue/%s", handle)

	case strings.HasPrefix(parsed.Collection, "fm.plyr"):
		return fmt.Sprintf("https://plyr.fm/%s", handle)

	case strings.HasPrefix(parsed.Collection, "fm.teal"):
		return fmt.Sprintf("https://teal.fm/%s", handle)

	case strings.HasPrefix(parsed.Collection, "social.grain.photo"):
		return fmt.Sprintf("https://grain.social/profile/%s/photo/%s", parsed.DID, parsed.RKey)

	case strings.HasPrefix(parsed.Collection, "social.grain.gallery"):
		return fmt.Sprintf("https://grain.social/profile/%s/gallery/%s", parsed.DID, parsed.RKey)

	case strings.HasPrefix(parsed.Collection, "social.grain"):
		return fmt.Sprintf("https://grain.social/profile/%s", parsed.DID)

	default:
		// Look up app URL from catalog
		if appURL := r.matchCatalog(parsed.Collection); appURL != "" {
			return appURL
		}
		return fmt.Sprintf("https://bsky.app/profile/%s", handle)
	}
}

func (r *Resolver) resolveTangledIssue(ctx context.Context, parsed *ATURI, ownerHandle string) string {
	record := r.fetchRecord(ctx, parsed)
	if record == nil {
		return fmt.Sprintf("https://tangled.org/%s", ownerHandle)
	}

	repoURI := extractJSONString(record, "repo")
	if repoURI == "" {
		return fmt.Sprintf("https://tangled.org/%s", ownerHandle)
	}

	repoName := r.resolveTangledRepoName(ctx, repoURI)
	if repoName == "" {
		return fmt.Sprintf("https://tangled.org/%s", ownerHandle)
	}

	repoParsed, err := ParseATURI(repoURI)
	if err != nil {
		return fmt.Sprintf("https://tangled.org/%s", ownerHandle)
	}
	repoOwnerHandle := r.resolveHandle(ctx, repoParsed.DID)
	if repoOwnerHandle == "" {
		repoOwnerHandle = repoParsed.DID
	}

	// Tangled uses sequential issue numbers in URLs — use issueId if available
	if issueID := extractJSONNumber(record, "issueId"); issueID > 0 {
		return fmt.Sprintf("https://tangled.org/%s/%s/issues/%d", repoOwnerHandle, repoName, issueID)
	}
	return fmt.Sprintf("https://tangled.org/%s/%s/issues", repoOwnerHandle, repoName)
}

func (r *Resolver) resolveTangledComment(ctx context.Context, parsed *ATURI, handle string) string {
	// Comments reference an issue — fetch to find the parent
	record := r.fetchRecord(ctx, parsed)
	if record == nil {
		return fmt.Sprintf("https://tangled.org/%s", handle)
	}

	issueURI := extractJSONString(record, "issue")
	if issueURI == "" {
		return fmt.Sprintf("https://tangled.org/%s", handle)
	}

	// Resolve the issue's deep link
	return r.ResolveDeepLink(ctx, issueURI)
}

func (r *Resolver) resolveTangledPR(ctx context.Context, parsed *ATURI, handle string) string {
	record := r.fetchRecord(ctx, parsed)
	if record == nil {
		return fmt.Sprintf("https://tangled.org/%s", handle)
	}

	repoURI := extractJSONString(record, "repo")
	if repoURI == "" {
		return fmt.Sprintf("https://tangled.org/%s", handle)
	}

	repoName := r.resolveTangledRepoName(ctx, repoURI)
	if repoName == "" {
		return fmt.Sprintf("https://tangled.org/%s", handle)
	}

	repoParsed, _ := ParseATURI(repoURI)
	repoOwnerHandle := r.resolveHandle(ctx, repoParsed.DID)
	if repoOwnerHandle == "" {
		repoOwnerHandle = repoParsed.DID
	}

	return fmt.Sprintf("https://tangled.org/%s/%s/pulls", repoOwnerHandle, repoName)
}

func (r *Resolver) resolveTangledRepoName(ctx context.Context, repoURI string) string {
	// Cache key
	cacheKey := "repo_name:" + repoURI
	if name, err := r.rdb.Get(ctx, cacheKey).Result(); err == nil {
		return name
	}

	parsed, err := ParseATURI(repoURI)
	if err != nil {
		return ""
	}

	record := r.fetchRecord(ctx, parsed)
	if record == nil {
		return ""
	}

	name := extractJSONString(record, "name")
	if name != "" {
		// Cache for 24h
		r.rdb.Set(ctx, cacheKey, name, 24*time.Hour)
	}
	return name
}

// FetchRecordText fetches the text content of an AT record (for notification previews).
func (r *Resolver) FetchRecordText(ctx context.Context, atURI string) string {
	parsed, err := ParseATURI(atURI)
	if err != nil {
		return ""
	}
	record := r.fetchRecord(ctx, parsed)
	if record == nil {
		return ""
	}

	// App-specific rich previews
	if text := r.richPreview(ctx, parsed.Collection, record); text != "" {
		return truncate(text, 150)
	}

	// Generic fallback: try common text fields
	for _, key := range []string{"text", "title", "name", "description"} {
		if text := extractJSONString(record, key); text != "" {
			return truncate(text, 120)
		}
	}
	return ""
}

// richPreview returns a formatted preview string for known collection types.
func (r *Resolver) richPreview(ctx context.Context, collection string, record map[string]interface{}) string {
	switch {
	// Tangled: issues with title + repo name
	case strings.HasPrefix(collection, "sh.tangled.repo.issue"):
		title := extractJSONString(record, "title")
		if title == "" {
			return ""
		}
		repoName := ""
		if repoURI := extractJSONString(record, "repo"); repoURI != "" {
			repoName = r.resolveTangledRepoName(ctx, repoURI)
		}
		if repoName != "" {
			return fmt.Sprintf("%s — %s", repoName, title)
		}
		return title

	// Tangled: PRs with title + repo name
	case strings.HasPrefix(collection, "sh.tangled.repo.pull"):
		title := extractJSONString(record, "title")
		if title == "" {
			title = extractJSONString(record, "body")
		}
		repoName := ""
		if repoURI := extractJSONString(record, "repo"); repoURI != "" {
			repoName = r.resolveTangledRepoName(ctx, repoURI)
		}
		if repoName != "" && title != "" {
			return fmt.Sprintf("%s — %s", repoName, title)
		}
		if title != "" {
			return title
		}
		if repoName != "" {
			return repoName
		}
		return ""

	// Tangled: stars — resolve the repo name
	case collection == "sh.tangled.feed.star":
		if subjectURI := extractJSONString(record, "subject"); subjectURI != "" {
			if name := r.resolveTangledRepoName(ctx, subjectURI); name != "" {
				return name
			}
		}
		return ""

	// Tangled: reactions — include the emoji
	case collection == "sh.tangled.feed.reaction":
		emoji := extractJSONString(record, "reaction")
		if emoji != "" {
			return emoji
		}
		return ""

	// Calendar events: name + date + location
	case strings.HasPrefix(collection, "community.lexicon.calendar.event"):
		name := extractJSONString(record, "name")
		if name == "" {
			return ""
		}
		preview := name
		if startsAt := extractJSONString(record, "startsAt"); startsAt != "" {
			if t, err := time.Parse(time.RFC3339, startsAt); err == nil {
				preview += fmt.Sprintf(" — %s", t.Format("Jan 2"))
			}
		}
		if locs, ok := record["locations"].([]interface{}); ok && len(locs) > 0 {
			if loc, ok := locs[0].(map[string]interface{}); ok {
				if locName, _ := loc["name"].(string); locName != "" {
					// Shorten long location names
					if len(locName) > 40 {
						locName = locName[:40] + "…"
					}
					preview += ", " + locName
				}
			}
		}
		return preview

	// Calendar RSVP: include status (going/interested) + resolve event name
	case strings.HasPrefix(collection, "community.lexicon.calendar.rsvp"):
		status := extractJSONString(record, "status")
		statusLabel := ""
		switch {
		case strings.HasSuffix(status, "#going"):
			statusLabel = "going"
		case strings.HasSuffix(status, "#interested"):
			statusLabel = "interested"
		case strings.HasSuffix(status, "#notgoing"), strings.HasSuffix(status, "#declined"):
			statusLabel = "not going"
		}

		// subject is a strongRef with uri+cid
		eventURI := extractJSONString(record, "subject", "uri")
		eventName := ""
		if eventURI != "" {
			eventName = r.FetchRecordText(ctx, eventURI)
		}

		if statusLabel != "" && eventName != "" {
			return statusLabel + " — " + eventName
		}
		if eventName != "" {
			return eventName
		}
		if statusLabel != "" {
			return statusLabel
		}
		return ""

	// Standard.site documents: title
	case collection == "site.standard.document":
		title := extractJSONString(record, "title")
		if title != "" {
			return title
		}
		return ""

	// Standard.site subscriptions: resolve publication name
	case collection == "site.standard.graph.subscription":
		if pubURI := extractJSONString(record, "publication"); pubURI != "" {
			return r.FetchRecordText(ctx, pubURI)
		}
		return ""

	// Standard.site comments
	case collection == "site.standard.comment":
		return extractJSONString(record, "text")

	// Grain comments
	case collection == "social.grain.comment":
		return extractJSONString(record, "text")

	// Grain photos/galleries/stories — used as subject text in "liked your photo" etc.
	case collection == "social.grain.photo":
		// Photos may have EXIF caption or description
		if desc := extractJSONString(record, "description"); desc != "" {
			return desc
		}
		return extractJSONString(record, "title")

	case collection == "social.grain.gallery":
		return extractJSONString(record, "title")

	case collection == "social.grain.story":
		return extractJSONString(record, "title")

	// Leaflet: recommend, comment, subscription — resolve the subject article's title
	case collection == "pub.leaflet.interactions.recommend":
		if subjectURI := extractJSONString(record, "subject"); subjectURI != "" {
			return r.FetchRecordText(ctx, subjectURI)
		}
		return ""

	case collection == "pub.leaflet.comment":
		preview := extractJSONString(record, "plaintext")
		// Also resolve the article title from subject
		if subjectURI := extractJSONString(record, "subject"); subjectURI != "" {
			if articleTitle := r.FetchRecordText(ctx, subjectURI); articleTitle != "" {
				if preview != "" {
					return fmt.Sprintf("on \"%s\": %s", articleTitle, preview)
				}
				return articleTitle
			}
		}
		return preview

	case collection == "pub.leaflet.graph.subscription":
		if subjectURI := extractJSONString(record, "subject"); subjectURI != "" {
			return r.FetchRecordText(ctx, subjectURI)
		}
		return ""

	// Leaflet document — the content record that recommends/comments point to
	case collection == "pub.leaflet.document":
		return extractJSONString(record, "title")

	// Plyr/Teal: tracks and lists
	case collection == "fm.plyr.list" || collection == "fm.plyr.dev.list":
		return extractJSONString(record, "name")

	case collection == "fm.plyr.dev.track":
		title := extractJSONString(record, "title")
		artist := extractJSONString(record, "artist")
		if title != "" && artist != "" {
			return fmt.Sprintf("%s — %s", artist, title)
		}
		if title != "" {
			return title
		}
		return ""
	}

	return ""
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// fetchRecord fetches an AT record from the owner's PDS.
func (r *Resolver) fetchRecord(ctx context.Context, parsed *ATURI) map[string]interface{} {
	pds := r.resolvePDS(ctx, parsed.DID)
	if pds == "" {
		return nil
	}

	url := fmt.Sprintf("%s/xrpc/com.atproto.repo.getRecord?repo=%s&collection=%s&rkey=%s",
		pds, parsed.DID, parsed.Collection, parsed.RKey)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil
	}

	resp, err := r.httpClient.Do(req)
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

	var result struct {
		Value map[string]interface{} `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil
	}
	return result.Value
}

// resolvePDS finds the PDS endpoint for a DID.
func (r *Resolver) resolvePDS(ctx context.Context, did string) string {
	r.pdsMu.RLock()
	if pds, ok := r.pdsCache[did]; ok {
		r.pdsMu.RUnlock()
		return pds
	}
	r.pdsMu.RUnlock()

	// Check Redis cache
	cacheKey := "pds:" + did
	if pds, err := r.rdb.Get(ctx, cacheKey).Result(); err == nil {
		r.pdsMu.Lock()
		r.pdsCache[did] = pds
		r.pdsMu.Unlock()
		return pds
	}

	var docURL string
	if strings.HasPrefix(did, "did:plc:") {
		docURL = fmt.Sprintf("%s/%s", r.plcURL, did)
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

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var doc struct {
		Service []struct {
			ID              string `json:"id"`
			Type            string `json:"type"`
			ServiceEndpoint string `json:"serviceEndpoint"`
		} `json:"service"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return ""
	}

	for _, svc := range doc.Service {
		if svc.Type == "AtprotoPersonalDataServer" {
			r.pdsMu.Lock()
			r.pdsCache[did] = svc.ServiceEndpoint
			r.pdsMu.Unlock()
			r.rdb.Set(ctx, cacheKey, svc.ServiceEndpoint, 24*time.Hour)
			return svc.ServiceEndpoint
		}
	}

	return ""
}

// resolveHandle resolves a DID to a handle via the public API.
func (r *Resolver) resolveHandle(ctx context.Context, did string) string {
	cacheKey := "handle:" + did
	if handle, err := r.rdb.Get(ctx, cacheKey).Result(); err == nil {
		return handle
	}

	url := fmt.Sprintf("https://public.api.bsky.app/xrpc/app.bsky.actor.getProfile?actor=%s", did)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return ""
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		Handle string `json:"handle"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}

	if result.Handle != "" {
		r.rdb.Set(ctx, cacheKey, result.Handle, 4*time.Hour)
	}
	return result.Handle
}

// extractJSONString extracts a nested string value from a map.
// e.g., extractJSONString(record, "subject", "uri") gets record["subject"]["uri"]
func extractJSONString(m map[string]interface{}, keys ...string) string {
	var current interface{} = m
	for _, key := range keys {
		obj, ok := current.(map[string]interface{})
		if !ok {
			return ""
		}
		current = obj[key]
	}
	s, _ := current.(string)
	return s
}

// extractEventWebURL pulls the first https:// URI from a calendar event's uris array.
func extractEventWebURL(record map[string]interface{}) string {
	uris, ok := record["uris"].([]interface{})
	if !ok || len(uris) == 0 {
		return ""
	}
	for _, u := range uris {
		entry, ok := u.(map[string]interface{})
		if !ok {
			continue
		}
		if uri, _ := entry["uri"].(string); strings.HasPrefix(uri, "https://") {
			return uri
		}
	}
	return ""
}

// extractJSONNumber extracts an integer value from a map (JSON numbers are float64).
func extractJSONNumber(m map[string]interface{}, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
