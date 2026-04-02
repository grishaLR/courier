package registry

import (
	"encoding/json"
	"os"
)

// AppCatalog contains the known ATProto app catalog with categories.
// Auto-generated from atproto.brussels/apps-data.json with manual overrides for collection prefixes.

type AppCategory string

const (
	CategorySocial         AppCategory = "Social"
	CategoryClient         AppCategory = "Client"
	CategoryChat           AppCategory = "Chat"
	CategoryMessaging      AppCategory = "Messaging"
	CategoryCommunities    AppCategory = "Communities"
	CategoryBlog           AppCategory = "Blog"
	CategoryPublishing     AppCategory = "Publishing"
	CategoryCode           AppCategory = "Code"
	CategoryDeveloperTools AppCategory = "Developer Tools"
	CategoryDeveloper      AppCategory = "Developer"
	CategoryEvents         AppCategory = "Events"
	CategoryMedia          AppCategory = "Media"
	CategoryPhotos         AppCategory = "Photos"
	CategoryVideo          AppCategory = "Video"
	CategoryAudio          AppCategory = "Audio"
	CategoryGaming         AppCategory = "Games"
	CategoryNews           AppCategory = "News"
	CategoryReviews        AppCategory = "Reviews"
	CategoryFood           AppCategory = "Food"
	CategoryTools          AppCategory = "Tools"
	CategoryHosting        AppCategory = "Hosting"
	CategoryLinks          AppCategory = "Links"
	CategoryIdentity       AppCategory = "Identity"
	CategoryAnalytics      AppCategory = "Analytics"
	CategoryMarketplace    AppCategory = "Marketplace"
	CategoryJobs           AppCategory = "Jobs"
	CategoryBookmarks      AppCategory = "Bookmarks"
	CategoryFeeds          AppCategory = "Feeds"
	CategorySupport        AppCategory = "Support"
	CategoryOther          AppCategory = "Other"
)

type CatalogApp struct {
	CollectionPrefix string      `json:"collectionPrefix"`
	AppName          string      `json:"appName"`
	AppURL           string      `json:"appUrl"`
	Category         AppCategory `json:"category"`
	Description      string      `json:"description,omitempty"`
	FaviconURL       string      `json:"faviconUrl,omitempty"`
	// AlternativeFor marks this app as an alternative client for another collection prefix.
	// e.g., Graysky is an alternative for "app.bsky"
	AlternativeFor   string      `json:"alternativeFor,omitempty"`
}

// LoadCatalogFromFile loads the app catalog from a JSON file.
func LoadCatalogFromFile(path string) ([]CatalogApp, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var apps []CatalogApp
	if err := json.Unmarshal(data, &apps); err != nil {
		return nil, err
	}
	return apps, nil
}

// DefaultCatalog returns a minimal built-in catalog as fallback when JSON file is unavailable.
func DefaultCatalog() []CatalogApp {
	return []CatalogApp{
		{CollectionPrefix: "app.bsky", AppName: "Bluesky", AppURL: "https://bsky.app", Category: CategorySocial, Description: "Social networking"},
		{CollectionPrefix: "sh.tangled", AppName: "Tangled", AppURL: "https://tangled.org", Category: CategoryDeveloperTools, Description: "Code collaboration"},
		{CollectionPrefix: "community.lexicon.calendar", AppName: "atmo.rsvp", AppURL: "https://atmo.rsvp", Category: CategoryEvents, Description: "Events & RSVPs"},
		{CollectionPrefix: "com.whtwnd", AppName: "WhiteWind", AppURL: "https://whtwnd.com", Category: CategoryPublishing, Description: "Blogging"},
		{CollectionPrefix: "fyi.unravel.frontpage", AppName: "Frontpage", AppURL: "https://frontpage.fyi", Category: CategoryNews, Description: "Link aggregator"},
		{CollectionPrefix: "events.smokesignal", AppName: "Smoke Signal", AppURL: "https://smokesignal.events", Category: CategoryEvents, Description: "Events"},
		{CollectionPrefix: "app.protoimsg.chat", AppName: "ProtoIMSG", AppURL: "https://protoimsg.app", Category: CategoryMessaging, Description: "Messaging"},
		{CollectionPrefix: "space.roomy", AppName: "Roomy", AppURL: "https://roomy.space", Category: CategoryCommunities, Description: "Chat spaces"},
		{CollectionPrefix: "pub.leaflet", AppName: "Leaflet.pub", AppURL: "https://leaflet.pub", Category: CategoryPublishing, Description: "Publishing"},
		{CollectionPrefix: "blog.pckt", AppName: "pckt.blog", AppURL: "https://pckt.blog", Category: CategoryPublishing, Description: "Blogging"},
	}
}

var loadedCatalog []CatalogApp

// SetCatalog sets the in-memory catalog (called once at startup).
func SetCatalog(apps []CatalogApp) {
	loadedCatalog = apps
}

// CatalogAll returns the loaded catalog, falling back to the built-in default.
func CatalogAll() []CatalogApp {
	if len(loadedCatalog) > 0 {
		return loadedCatalog
	}
	return DefaultCatalog()
}

// CatalogByCollection returns a map of collection prefix → CatalogApp for fast lookup.
func CatalogByCollection() map[string]CatalogApp {
	catalog := CatalogAll()
	m := make(map[string]CatalogApp, len(catalog))
	for _, app := range catalog {
		m[app.CollectionPrefix] = app
	}
	return m
}

// MatchApp finds the best matching app for a collection name.
func MatchApp(collection string, catalog map[string]CatalogApp) *CatalogApp {
	bestLen := 0
	var best *CatalogApp
	for prefix, app := range catalog {
		if len(prefix) > bestLen && len(collection) >= len(prefix) && collection[:len(prefix)] == prefix {
			a := app
			best = &a
			bestLen = len(prefix)
		}
	}
	return best
}
