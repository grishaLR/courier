package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

type User struct {
	DID         string      `json:"did"`
	Handle      string      `json:"handle"`
	DeviceToken string      `json:"deviceToken"`
	Platform    string      `json:"platform"` // "ios" or "android"
	Preferences Preferences `json:"preferences"`
}

type Preferences struct {
	Likes    bool `json:"likes"`
	Replies  bool `json:"replies"`
	Reposts  bool `json:"reposts"`
	Follows  bool `json:"follows"`
	Mentions bool `json:"mentions"`
	Quotes   bool `json:"quotes"`
	Generic  bool `json:"generic"`
}

func DefaultPreferences() Preferences {
	return Preferences{
		Likes:    true,
		Replies:  true,
		Reposts:  true,
		Follows:  true,
		Mentions: true,
		Quotes:   true,
		Generic:  true,
	}
}

type Registry struct {
	rdb *redis.Client
}

func appPrefsKey(did string) string      { return "app_prefs:" + did }
func preferredAppsKey(did string) string { return "preferred_apps:" + did }
func pinnedAppsKey(did string) string    { return "pinned_apps:" + did }
func userCatalogKey(did string) string   { return "user_catalog:" + did }
func tokenKey(token string) string       { return "token:" + token }

const userPrefsTTL = 365 * 24 * time.Hour
const tokenTTL = 60 * 24 * time.Hour

// SaveUserCatalog stores the user's resolved app list (your apps + discover) in Redis.
func (r *Registry) SaveUserCatalog(ctx context.Context, did string, data []byte) error {
	return r.rdb.Set(ctx, userCatalogKey(did), string(data), 24*time.Hour).Err()
}

// GetUserCatalog returns the cached user catalog, or nil if not cached.
func (r *Registry) GetUserCatalog(ctx context.Context, did string) ([]byte, error) {
	val, err := r.rdb.Get(ctx, userCatalogKey(did)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []byte(val), nil
}

// InvalidateUserCatalog deletes the cached catalog so it gets rebuilt next load.
func (r *Registry) InvalidateUserCatalog(ctx context.Context, did string) error {
	return r.rdb.Del(ctx, userCatalogKey(did)).Err()
}

// AddPinnedApp adds an app to the user's "apps I'm on" list.
func (r *Registry) AddPinnedApp(ctx context.Context, did, collectionPrefix string) error {
	return r.rdb.SAdd(ctx, pinnedAppsKey(did), collectionPrefix).Err()
}

// RemovePinnedApp removes an app from the user's "apps I'm on" list.
func (r *Registry) RemovePinnedApp(ctx context.Context, did, collectionPrefix string) error {
	return r.rdb.SRem(ctx, pinnedAppsKey(did), collectionPrefix).Err()
}

// GetPinnedApps returns the user's manually pinned apps.
func (r *Registry) GetPinnedApps(ctx context.Context, did string) (map[string]bool, error) {
	members, err := r.rdb.SMembers(ctx, pinnedAppsKey(did)).Result()
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(members))
	for _, m := range members {
		result[m] = true
	}
	return result, nil
}

// SetPreferredApps stores which app the user prefers for shared lexicons.
// Keys are collection prefixes, values are app URLs.
func (r *Registry) SetPreferredApps(ctx context.Context, did string, prefs map[string]string) error {
	data, _ := json.Marshal(prefs)
	return r.rdb.Set(ctx, preferredAppsKey(did), string(data), userPrefsTTL).Err()
}

// GetPreferredApps returns the user's preferred app per collection prefix.
func (r *Registry) GetPreferredApps(ctx context.Context, did string) (map[string]string, error) {
	val, err := r.rdb.Get(ctx, preferredAppsKey(did)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var prefs map[string]string
	json.Unmarshal([]byte(val), &prefs)
	return prefs, nil
}

// GetPreferredAppURL returns the preferred app URL for a collection, or empty string.
func (r *Registry) GetPreferredAppURL(ctx context.Context, did, collection string) string {
	prefs, err := r.GetPreferredApps(ctx, did)
	if err != nil || prefs == nil {
		return ""
	}
	catalog := CatalogByCollection()
	app := MatchApp(collection, catalog)
	if app == nil {
		return ""
	}
	if url, ok := prefs[app.CollectionPrefix]; ok {
		return url
	}
	return ""
}

// SetAppPreferences stores which apps a user wants notifications from.
// Keys are collection prefixes, values are booleans.
func (r *Registry) SetAppPreferences(ctx context.Context, did string, prefs map[string]bool) error {
	key := appPrefsKey(did)
	data, _ := json.Marshal(prefs)
	return r.rdb.Set(ctx, key, string(data), userPrefsTTL).Err()
}

// GetAppPreferences returns the user's per-app notification preferences.
func (r *Registry) GetAppPreferences(ctx context.Context, did string) (map[string]bool, error) {
	key := appPrefsKey(did)
	val, err := r.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil // no preferences set — allow all
	}
	if err != nil {
		return nil, err
	}
	var prefs map[string]bool
	if err := json.Unmarshal([]byte(val), &prefs); err != nil {
		return nil, err
	}
	return prefs, nil
}

// IsAppEnabled checks if a user wants notifications from a given collection.
// Returns true if no app preferences are set (default: all enabled).
func (r *Registry) IsAppEnabled(ctx context.Context, did, collection string) bool {
	prefs, err := r.GetAppPreferences(ctx, did)
	if err != nil || prefs == nil {
		return true // default: all enabled
	}
	// Find the best matching prefix
	catalog := CatalogByCollection()
	app := MatchApp(collection, catalog)
	if app == nil {
		return true // unknown app, allow by default
	}
	enabled, exists := prefs[app.CollectionPrefix]
	if !exists {
		return true // not in prefs, allow by default
	}
	return enabled
}

func New(rdb *redis.Client) *Registry {
	return &Registry{rdb: rdb}
}

func userKey(did string) string         { return "user:" + did }
func notifKey(did string) string        { return "notifications:" + did }

func (r *Registry) Register(ctx context.Context, u *User) error {
	prefs, _ := json.Marshal(u.Preferences)
	pipe := r.rdb.Pipeline()
	pipe.HSet(ctx, userKey(u.DID), map[string]interface{}{
		"did":         u.DID,
		"handle":      u.Handle,
		"deviceToken": u.DeviceToken,
		"platform":    u.Platform,
		"preferences": string(prefs),
	})
	pipe.SAdd(ctx, "watched_dids", u.DID)
	if u.DeviceToken != "" {
		pipe.Set(ctx, tokenKey(u.DeviceToken), u.DID, tokenTTL)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Registry) Unregister(ctx context.Context, did string) error {
	pipe := r.rdb.Pipeline()
	pipe.Del(ctx, userKey(did))
	pipe.SRem(ctx, "watched_dids", did)
	// Keep notifications — user may log back in
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Registry) GetUser(ctx context.Context, did string) (*User, error) {
	vals, err := r.rdb.HGetAll(ctx, userKey(did)).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("user not found: %s", did)
	}

	u := &User{
		DID:         vals["did"],
		Handle:      vals["handle"],
		DeviceToken: vals["deviceToken"],
		Platform:    vals["platform"],
	}
	if p, ok := vals["preferences"]; ok {
		json.Unmarshal([]byte(p), &u.Preferences)
	}
	return u, nil
}

func (r *Registry) UpdatePreferences(ctx context.Context, did string, prefs Preferences) error {
	p, _ := json.Marshal(prefs)
	return r.rdb.HSet(ctx, userKey(did), "preferences", string(p)).Err()
}

func (r *Registry) UpdateDeviceToken(ctx context.Context, did string, token string) error {
	if token != "" {
		r.rdb.Set(ctx, tokenKey(token), did, tokenTTL)
	}
	return r.rdb.HSet(ctx, userKey(did), "deviceToken", token).Err()
}

func (r *Registry) GetWatchedDIDs(ctx context.Context) ([]string, error) {
	return r.rdb.SMembers(ctx, "watched_dids").Result()
}

func (r *Registry) IsWatched(ctx context.Context, did string) (bool, error) {
	return r.rdb.SIsMember(ctx, "watched_dids", did).Result()
}

// RemoveByToken clears the device token for a user with a bad push token.
// Does NOT unregister — keeps the user and DID in the watcher so they can
// re-register their token next time they open the app.
func (r *Registry) RemoveByToken(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	did, err := r.rdb.Get(ctx, tokenKey(token)).Result()
	if err == redis.Nil {
		return nil // token not in reverse index — already cleaned up or pre-dates index
	}
	if err != nil {
		return err
	}
	log.Printf("cleared bad token for %s (%s…)", did, token[:8])
	pipe := r.rdb.Pipeline()
	pipe.HSet(ctx, userKey(did), "deviceToken", "")
	pipe.Del(ctx, tokenKey(token))
	_, err = pipe.Exec(ctx)
	return err
}

// StoreNotification appends a notification to the user's history, capped at 50.
func (r *Registry) StoreNotification(ctx context.Context, did string, notif interface{}) error {
	data, err := json.Marshal(notif)
	if err != nil {
		return err
	}
	key := notifKey(did)
	pipe := r.rdb.Pipeline()
	pipe.LPush(ctx, key, string(data))
	pipe.LTrim(ctx, key, 0, 49)
	pipe.Expire(ctx, key, 30*24*time.Hour)
	_, err = pipe.Exec(ctx)
	return err
}

// GetNotifications returns recent notifications for a user.
func (r *Registry) GetNotifications(ctx context.Context, did string) ([]json.RawMessage, error) {
	vals, err := r.rdb.LRange(ctx, notifKey(did), 0, 49).Result()
	if err != nil {
		return nil, err
	}
	result := make([]json.RawMessage, len(vals))
	for i, v := range vals {
		result[i] = json.RawMessage(v)
	}
	return result, nil
}

// ClearNotifications removes all stored notifications for a user.
func (r *Registry) ClearNotifications(ctx context.Context, did string) error {
	return r.rdb.Del(ctx, notifKey(did)).Err()
}

// DeleteNotification removes a specific notification by its URI from the user's list.
func (r *Registry) DeleteNotification(ctx context.Context, did string, uri string) error {
	key := notifKey(did)
	vals, err := r.rdb.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		return err
	}
	for _, v := range vals {
		var notif struct {
			URI       string `json:"uri"`
			CreatedAt string `json:"createdAt"`
		}
		if json.Unmarshal([]byte(v), &notif) == nil && notif.URI == uri {
			r.rdb.LRem(ctx, key, 1, v)
			return nil
		}
	}
	return nil
}

// SaveCursor persists the Jetstream cursor.
func (r *Registry) SaveCursor(ctx context.Context, cursor int64) error {
	return r.rdb.Set(ctx, "cursor", cursor, 0).Err()
}

// GetCursor retrieves the last saved Jetstream cursor.
func (r *Registry) GetCursor(ctx context.Context) (int64, error) {
	val, err := r.rdb.Get(ctx, "cursor").Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return val, err
}
