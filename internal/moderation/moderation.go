package moderation

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Moderator checks if a notification sender is a known bad actor
// by querying Bluesky's moderation labels and maintaining a local blocklist.
type Moderator struct {
	rdb        *redis.Client
	httpClient *http.Client
	labelerURL string

	// In-memory blocklist for manually blocked DIDs
	blockMu   sync.RWMutex
	blocklist map[string]bool
}

func New(rdb *redis.Client) *Moderator {
	return &Moderator{
		rdb:        rdb,
		labelerURL: "https://public.api.bsky.app",
		httpClient: &http.Client{Timeout: 5 * time.Second},
		blocklist:  make(map[string]bool),
	}
}

// BlockDID adds a DID to the permanent blocklist
func (m *Moderator) BlockDID(did string) {
	m.blockMu.Lock()
	m.blocklist[did] = true
	m.blockMu.Unlock()
}

// UnblockDID removes a DID from the permanent blocklist
func (m *Moderator) UnblockDID(did string) {
	m.blockMu.Lock()
	delete(m.blocklist, did)
	m.blockMu.Unlock()
}

// badLabels that indicate a sender should be suppressed
var badLabels = map[string]bool{
	"spam":           true,
	"impersonation":  true,
	"scam":           true,
	"misleading":     true,
	"!warn":          true,
	"!hide":          true,
	"!no-unauthenticated": true,
}

// Check returns true if the sender should be allowed through.
// Returns false if blocked or labeled as a bad actor.
func (m *Moderator) Check(ctx context.Context, fromDID string) bool {
	// 1. Local blocklist (instant)
	m.blockMu.RLock()
	blocked := m.blocklist[fromDID]
	m.blockMu.RUnlock()
	if blocked {
		return false
	}

	// 2. Check cached label result
	cacheKey := fmt.Sprintf("mod:labels:%s", fromDID)
	if cached, err := m.rdb.Get(ctx, cacheKey).Result(); err == nil {
		return cached == "ok"
	}

	// 3. Fetch labels from Bluesky (async-friendly, cached for 1 hour)
	labeled := m.fetchLabels(ctx, fromDID)
	if labeled {
		m.rdb.Set(ctx, cacheKey, "blocked", 1*time.Hour)
		log.Printf("🛡️ moderation: blocked %s (bad labels)", fromDID)
		return false
	}

	m.rdb.Set(ctx, cacheKey, "ok", 1*time.Hour)
	return true
}

// fetchLabels checks if a DID has any moderation labels applied.
func (m *Moderator) fetchLabels(ctx context.Context, did string) bool {
	url := fmt.Sprintf("%s/xrpc/com.atproto.label.queryLabels?uriPatterns=%s", m.labelerURL, did)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false // fail open
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return false // fail open
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return false // fail open
	}

	var result struct {
		Labels []struct {
			Val string `json:"val"`
			Neg bool   `json:"neg"` // negation label (removes a prior label)
		} `json:"labels"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false
	}

	for _, label := range result.Labels {
		if label.Neg {
			continue // negation = label was removed
		}
		if badLabels[label.Val] {
			return true
		}
	}

	return false
}
