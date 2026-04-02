package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Resolver resolves AT URIs into web-accessible deep links.
type Resolver struct {
	rdb        *redis.Client
	httpClient *http.Client
	plcURL     string

	// Cache PDS endpoints: DID → PDS URL
	pdsMu    sync.RWMutex
	pdsCache map[string]string
}

func New(rdb *redis.Client) *Resolver {
	return &Resolver{
		rdb:    rdb,
		plcURL: "https://plc.directory",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		pdsCache: make(map[string]string),
	}
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
		return fmt.Sprintf("https://atmo.rsvp/p/%s/e/%s", handle, parsed.RKey)

	case strings.HasPrefix(parsed.Collection, "com.whtwnd.blog"):
		return fmt.Sprintf("https://whtwnd.com/%s/%s", handle, parsed.RKey)

	case strings.HasPrefix(parsed.Collection, "fyi.unravel.frontpage"):
		return fmt.Sprintf("https://frontpage.fyi/post/%s/%s", handle, parsed.RKey)

	case strings.HasPrefix(parsed.Collection, "events.smokesignal"):
		return fmt.Sprintf("https://smokesignal.events/%s/%s", handle, parsed.RKey)

	case strings.HasPrefix(parsed.Collection, "blue.pico"):
		return fmt.Sprintf("https://pico.blue/%s", handle)

	default:
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

	// Tangled uses sequential issue numbers, not rkeys, in URLs.
	// Link to the repo's issues list — one click away from the specific issue.
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
