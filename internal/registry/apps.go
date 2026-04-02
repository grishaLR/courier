package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/redis/go-redis/v9"
)

type AppInfo struct {
	CollectionPrefix string `json:"collectionPrefix"`
	AppName          string `json:"appName"`
	AppURL           string `json:"appURL"`
	URLPattern       string `json:"urlPattern,omitempty"` // e.g., "https://tangled.org/{handle}"
	Verified         bool   `json:"verified"`
	SuggestedBy      string `json:"suggestedBy,omitempty"`
	CreatedAt        string `json:"createdAt"`
}

const (
	appRegistryKey     = "app_registry"
	appSuggestionsKey  = "app_suggestions"
)

// SeedDefaultApps loads the default known apps into Redis if not already present.
func (r *Registry) SeedDefaultApps(ctx context.Context) error {
	exists, err := r.rdb.Exists(ctx, appRegistryKey).Result()
	if err != nil {
		return err
	}
	if exists > 0 {
		return nil // already seeded
	}

	defaults := []AppInfo{
		{CollectionPrefix: "app.bsky", AppName: "Bluesky", AppURL: "https://bsky.app", Verified: true},
		{CollectionPrefix: "sh.tangled", AppName: "Tangled", AppURL: "https://tangled.org", Verified: true},
		{CollectionPrefix: "community.lexicon.calendar", AppName: "Atmo", AppURL: "https://atmo.rsvp", Verified: true},
		{CollectionPrefix: "com.whtwnd.blog", AppName: "WhiteWind", AppURL: "https://whtwnd.com", Verified: true},
		{CollectionPrefix: "fyi.unravel.frontpage", AppName: "Frontpage", AppURL: "https://frontpage.fyi", Verified: true},
		{CollectionPrefix: "blue.pico", AppName: "Picosky", AppURL: "https://pico.blue", Verified: true},
		{CollectionPrefix: "events.smokesignal", AppName: "Smoke Signal", AppURL: "https://smokesignal.events", Verified: true},
	}

	pipe := r.rdb.Pipeline()
	for _, app := range defaults {
		app.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		data, _ := json.Marshal(app)
		pipe.HSet(ctx, appRegistryKey, app.CollectionPrefix, string(data))
	}
	_, err = pipe.Exec(ctx)
	return err
}

// GetAppByCollection finds the app that matches a collection name.
func (r *Registry) GetAppByCollection(ctx context.Context, collection string) (*AppInfo, error) {
	all, err := r.rdb.HGetAll(ctx, appRegistryKey).Result()
	if err != nil {
		return nil, err
	}

	// Find longest matching prefix
	var bestMatch *AppInfo
	bestLen := 0
	for prefix, data := range all {
		if len(prefix) > bestLen && len(collection) >= len(prefix) && collection[:len(prefix)] == prefix {
			var app AppInfo
			if err := json.Unmarshal([]byte(data), &app); err == nil {
				bestMatch = &app
				bestLen = len(prefix)
			}
		}
	}
	return bestMatch, nil
}

// GetAllApps returns all registered apps.
func (r *Registry) GetAllApps(ctx context.Context) ([]AppInfo, error) {
	all, err := r.rdb.HGetAll(ctx, appRegistryKey).Result()
	if err != nil {
		return nil, err
	}
	apps := make([]AppInfo, 0, len(all))
	for _, data := range all {
		var app AppInfo
		if err := json.Unmarshal([]byte(data), &app); err == nil {
			apps = append(apps, app)
		}
	}
	return apps, nil
}

// AddAppSuggestion stores a user suggestion for a new app.
func (r *Registry) AddAppSuggestion(ctx context.Context, collection, appName, appURL, suggestedBy string) error {
	suggestion := AppInfo{
		CollectionPrefix: collection,
		AppName:          appName,
		AppURL:           appURL,
		Verified:         false,
		SuggestedBy:      suggestedBy,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(suggestion)
	key := fmt.Sprintf("%s:%s:%s", appSuggestionsKey, collection, suggestedBy)
	return r.rdb.Set(ctx, key, string(data), 90*24*time.Hour).Err()
}

// ApproveAppSuggestion moves a suggestion into the verified registry.
func (r *Registry) ApproveApp(ctx context.Context, app AppInfo) error {
	app.Verified = true
	app.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	data, _ := json.Marshal(app)
	return r.rdb.HSet(ctx, appRegistryKey, app.CollectionPrefix, string(data)).Err()
}

// GetAppSuggestions returns all pending suggestions.
func (r *Registry) GetAppSuggestions(ctx context.Context) ([]AppInfo, error) {
	keys, err := r.rdb.Keys(ctx, appSuggestionsKey+":*").Result()
	if err != nil {
		return nil, err
	}
	suggestions := make([]AppInfo, 0, len(keys))
	for _, key := range keys {
		data, err := r.rdb.Get(ctx, key).Result()
		if err != nil {
			continue
		}
		var app AppInfo
		if err := json.Unmarshal([]byte(data), &app); err == nil {
			suggestions = append(suggestions, app)
		}
	}
	return suggestions, nil
}
