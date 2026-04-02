package hydrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

type ActorProfile struct {
	DisplayName string `json:"displayName"`
	Avatar      string `json:"avatar"`
	Handle      string `json:"handle"`
}

type Hydrator struct {
	rdb        *redis.Client
	apiURL     string
	httpClient *http.Client
}

func New(rdb *redis.Client, apiURL string) *Hydrator {
	return &Hydrator{
		rdb:    rdb,
		apiURL: apiURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func cacheKey(did string) string { return "actor_cache:" + did }

func (h *Hydrator) GetProfile(ctx context.Context, did string) (*ActorProfile, error) {
	// Check cache first
	vals, err := h.rdb.HGetAll(ctx, cacheKey(did)).Result()
	if err == nil && len(vals) > 0 {
		return &ActorProfile{
			DisplayName: vals["displayName"],
			Avatar:      vals["avatar"],
			Handle:      vals["handle"],
		}, nil
	}

	// Fetch from API
	profile, err := h.fetchProfile(ctx, did)
	if err != nil {
		return nil, err
	}

	// Cache with 4hr TTL
	key := cacheKey(did)
	h.rdb.HSet(ctx, key, map[string]interface{}{
		"displayName": profile.DisplayName,
		"avatar":      profile.Avatar,
		"handle":      profile.Handle,
	})
	h.rdb.Expire(ctx, key, 4*time.Hour)

	return profile, nil
}

func (h *Hydrator) fetchProfile(ctx context.Context, did string) (*ActorProfile, error) {
	url := fmt.Sprintf("%s/xrpc/app.bsky.actor.getProfile?actor=%s", h.apiURL, did)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch profile: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch profile: status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Handle      string `json:"handle"`
		DisplayName string `json:"displayName"`
		Avatar      string `json:"avatar"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode profile: %w", err)
	}

	return &ActorProfile{
		DisplayName: result.DisplayName,
		Avatar:      result.Avatar,
		Handle:      result.Handle,
	}, nil
}
