package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
	"github.com/redis/go-redis/v9"
	"google.golang.org/api/option"

	"github.com/grishalr/courier-social/api/handlers"
	"github.com/grishalr/courier-social/internal/auth"
	"github.com/grishalr/courier-social/internal/classifier"
	"github.com/grishalr/courier-social/internal/oauth"
	"github.com/grishalr/courier-social/internal/hydrator"
	"github.com/grishalr/courier-social/internal/jetstream"
	"github.com/grishalr/courier-social/internal/push"
	"github.com/grishalr/courier-social/internal/registry"
	"github.com/grishalr/courier-social/internal/resolver"
	"github.com/grishalr/courier-social/internal/spacedust"
)

func main() {
	redisURL := env("REDIS_URL", "localhost:6379")
	port := env("PORT", "8080")
	jetstreamURL := env("JETSTREAM_URL", "wss://jetstream2.us-east.bsky.network/subscribe")
	spacedustURL := env("SPACEDUST_URL", "wss://spacedust.microcosm.blue/subscribe")
	useSpacedust := env("USE_SPACEDUST", "true") == "true"
	bskyAPIURL := env("BSKY_API_URL", "https://public.api.bsky.app")

	// Redis — supports both redis:// URLs and host:port format
	var rdb *redis.Client
	if strings.HasPrefix(redisURL, "redis://") || strings.HasPrefix(redisURL, "rediss://") {
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			log.Fatalf("redis: invalid URL: %v", err)
		}
		rdb = redis.NewClient(opts)
	} else {
		rdb = redis.NewClient(&redis.Options{Addr: redisURL})
	}
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("redis: %v", err)
	}
	log.Println("redis: connected")

	reg := registry.New(rdb)
	hyd := hydrator.New(rdb, bskyAPIURL)
	linkResolver := resolver.New(rdb)

	// Push dispatcher (optional — works without credentials for dev)
	dispatcher := initPushDispatcher()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load watched DIDs from Redis
	watchedDIDs, err := reg.GetWatchedDIDs(ctx)
	if err != nil {
		log.Fatalf("failed to load watched DIDs: %v", err)
	}
	log.Printf("loaded %d watched DIDs", len(watchedDIDs))

	// Load app catalog from JSON file (fallback to built-in)
	catalogPath := env("CATALOG_PATH", "web/public/app_catalog.json")
	if apps, err := registry.LoadCatalogFromFile(catalogPath); err == nil {
		registry.SetCatalog(apps)
		log.Printf("loaded %d apps from %s", len(apps), catalogPath)
	} else {
		log.Printf("catalog file not found (%s), using built-in catalog", catalogPath)
	}

	// Seed app registry
	if err := reg.SeedDefaultApps(ctx); err != nil {
		log.Printf("warning: failed to seed app registry: %v", err)
	}

	var didMu sync.RWMutex
	didSet := make(map[string]bool, len(watchedDIDs))
	for _, d := range watchedDIDs {
		didSet[d] = true
	}

	cursor, _ := reg.GetCursor(ctx)

	// Collections for Jetstream fallback (no filter = full firehose)
	_ = []string{} // unused when Spacedust is active

	// Cursor persistence (throttled to every 5s)
	var lastCursorSave time.Time
	onCursor := func(c int64) {
		if time.Since(lastCursorSave) > 5*time.Second {
			reg.SaveCursor(context.Background(), c)
			lastCursorSave = time.Now()
		}
	}

	// Bad token cleanup channel
	badTokens := make(chan string, 100)
	go func() {
		for token := range badTokens {
			if err := reg.RemoveByToken(context.Background(), token); err != nil {
				log.Printf("bad token cleanup error: %v", err)
			} else {
				log.Printf("removed user with bad token %s…", token[:8])
			}
		}
	}()

	var notifHub *handlers.NotifHub // initialized after oauthSessions

	// Shared notification processing — used by both Spacedust and Jetstream
	processNotification := func(notif *classifier.Notification) {
		did := notif.ForDID

		user, err := reg.GetUser(context.Background(), did)
		if err != nil {
			return
		}
		if !wantsNotification(user.Preferences, notif.Type) {
			return
		}
		// Check per-app preferences
		if !reg.IsAppEnabled(context.Background(), did, notif.Collection) {
			return
		}

		profile, _ := hyd.GetProfile(context.Background(), notif.FromDID)
		enriched := enrichNotification(notif, profile, linkResolver, reg)

		reg.StoreNotification(context.Background(), did, enriched)

		log.Printf("📬 %s from %s → %s", notif.Type, notif.FromDID, did)

		notifHub.Broadcast(did, enriched)

		if dispatcher != nil && user.DeviceToken != "" {
			title, body := formatPush(enriched)
			pushNotif := &push.Notification{
				Title:    title,
				Body:     body,
				Token:    user.DeviceToken,
				Platform: user.Platform,
				Category: string(notif.Type),
				Data: map[string]string{
					"type":    string(notif.Type),
					"uri":     notif.URI,
					"fromDid": notif.FromDID,
				},
			}
			go func(token string) {
				result := dispatcher.Send(pushNotif)
				if result.BadToken {
					badTokens <- token
				}
			}(user.DeviceToken)
		}
	}

	// Jetstream event handler (legacy/fallback)
	onJetstreamEvent := func(event *jetstream.Event) {
		// When running hybrid (Spacedust + Jetstream), skip app.bsky events
		// since Spacedust handles those more efficiently
		if useSpacedust && event.Commit != nil && strings.HasPrefix(event.Commit.Collection, "app.bsky.") {
			return
		}

		didMu.RLock()
		defer didMu.RUnlock()

		if didSet[event.Did] {
			return
		}

		for did := range didSet {
			notif := classifier.Classify(event, did)
			if notif == nil {
				continue
			}
			processNotification(notif)
		}
	}

	// Spacedust event handler
	onSpacedustEvent := func(event *spacedust.Event) {
		notif := spacedust.ClassifyLink(event)
		if notif == nil {
			return
		}
		// Skip own writes
		didMu.RLock()
		isOwnWrite := didSet[notif.FromDID]
		didMu.RUnlock()
		if isOwnWrite {
			return
		}
		// ForDID from Spacedust is the subject (could be AT URI or DID)
		// Extract DID if it's an AT URI
		if strings.HasPrefix(notif.ForDID, "at://") {
			parts := strings.SplitN(strings.TrimPrefix(notif.ForDID, "at://"), "/", 2)
			if len(parts) > 0 {
				notif.ForDID = parts[0]
			}
		}
		processNotification(notif)
	}

	// Event source: Spacedust (default) or Jetstream (fallback)
	var sdClient *spacedust.Client
	var jsClient *jetstream.Client

	if useSpacedust {
		// Spacedust for Bluesky interactions (efficient, filtered)
		var sdOpts []spacedust.Option
		if os.Getenv("SPACEDUST_INSTANT") == "true" {
			sdOpts = append(sdOpts, spacedust.WithInstant(true))
			log.Println("spacedust: instant mode enabled")
		}
		sdClient = spacedust.NewClient(spacedustURL, watchedDIDs, onSpacedustEvent, sdOpts...)
		log.Println("event source: spacedust (bluesky interactions)")

		// Jetstream for non-Bluesky collections (tangled, atmo, etc.)
		var jsOpts []jetstream.Option
		jsOpts = append(jsOpts, jetstream.WithOnCursor(onCursor))
		if cursor > 0 {
			jsOpts = append(jsOpts, jetstream.WithCursor(cursor))
		}
		jsClient = jetstream.NewClient(jetstreamURL, watchedDIDs, onJetstreamEvent, jsOpts...)
		log.Println("event source: jetstream (non-bluesky collections)")
	} else {
		var clientOpts []jetstream.Option
		clientOpts = append(clientOpts, jetstream.WithOnCursor(onCursor))
		if cursor > 0 {
			clientOpts = append(clientOpts, jetstream.WithCursor(cursor))
			log.Printf("resuming from cursor %d", cursor)
		}
		jsClient = jetstream.NewClient(jetstreamURL, watchedDIDs, onJetstreamEvent, clientOpts...)
		log.Println("event source: jetstream only")
	}

	// HTTP API
	addDID := func(did string) {
		didMu.Lock()
		didSet[did] = true
		didMu.Unlock()
		if sdClient != nil {
			sdClient.AddDID(did)
			sdClient.Reconnect() // Spacedust needs reconnect to update DIDs
		}
		if jsClient != nil {
			jsClient.AddDID(did)
		}
	}
	removeDID := func(did string) {
		didMu.Lock()
		delete(didSet, did)
		didMu.Unlock()
		if sdClient != nil {
			sdClient.RemoveDID(did)
		}
		if jsClient != nil {
			jsClient.RemoveDID(did)
		}
	}

	h := handlers.New(reg, handlers.ResolveDID(bskyAPIURL), addDID, removeDID)
	oauthSessions := oauth.NewSessionStore(rdb)
	notifHub = handlers.NewNotifHub(oauthSessions)
	authService := auth.NewService(rdb)
	ah := handlers.NewAuthHandlers(authService, oauthSessions)
	oauthBaseURL := env("BASE_URL", "https://courier.social")
	oh := handlers.NewOAuthHandlers(oauthSessions, oauthBaseURL)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(httprate.LimitByIP(60, time.Minute))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://courier.social", "https://www.courier.social", "http://localhost:*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "X-DID", "Authorization"},
		AllowCredentials: true,
	}))

	// ── Public API (no auth required) ──────────────────────
	r.Group(func(r chi.Router) {
		// Auth flow
		r.Post("/auth/challenge", ah.RequestChallenge)
		r.Post("/auth/verify", ah.VerifyChallenge)

		// OAuth flow
		r.Get("/oauth-client-metadata.json", oh.ClientMetadata)
		r.Post("/auth/oauth/start", oh.Start)
		r.Get("/auth/callback", oh.Callback)
		r.Get("/auth/session", oh.GetSession)
		r.Post("/auth/logout", oh.Logout)

		// App catalog (read-only)
		catalogH := handlers.NewCatalogHandlers(reg)
		r.Get("/catalog", catalogH.GetCatalog)
		r.Get("/catalog/user", catalogH.GetUserApps)
		r.Get("/catalog/alternatives", catalogH.GetAlternatives)

		// App registry (read-only)
		appH := handlers.NewAppHandlers(reg)
		r.Get("/apps", appH.ListApps)
		r.Get("/apps/lookup", appH.LookupApp)
	})

	// ── Protected API (requires authenticated session) ───
	r.Group(func(r chi.Router) {
		r.Use(handlers.RequireAuth(oauthSessions, reg))

		// Device registration & preferences
		r.Post("/register", h.Register)
		r.Put("/preferences", h.UpdatePreferences)
		r.Delete("/unregister", h.Unregister)

		// Notifications (REST)
		r.Get("/notifications/{did}", h.GetNotifications)

		// User catalog preferences (write)
		catalogH := handlers.NewCatalogHandlers(reg)
		r.Get("/catalog/user/prefs", catalogH.GetAppPrefs)
		r.Put("/catalog/user/prefs", catalogH.SetAppPrefs)
		r.Get("/catalog/user/preferred", catalogH.GetPreferredApps)
		r.Put("/catalog/user/preferred", catalogH.SetPreferredApps)
		r.Post("/catalog/user/pin", catalogH.PinApp)
		r.Delete("/catalog/user/pin", catalogH.UnpinApp)

		// App suggestions
		appH := handlers.NewAppHandlers(reg)
		r.Post("/apps/suggest", appH.SuggestApp)
	})

	// WebSocket (handles its own first-message auth)
	r.Get("/ws/notifications/{did}", notifHub.Subscribe)

	// Start event source
	if sdClient != nil {
		go func() {
			if err := sdClient.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("spacedust error: %v", err)
			}
		}()
	}
	if jsClient != nil {
		go func() {
			if err := jsClient.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("jetstream error: %v", err)
			}
		}()
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
	go func() {
		log.Printf("courier: HTTP server on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	// Graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down...")
	cancel()
	if dispatcher != nil {
		dispatcher.Close()
	}
	close(badTokens)
	srv.Shutdown(context.Background())
	reg.SaveCursor(context.Background(), 0)
}

func initPushDispatcher() *push.Dispatcher {
	apnsKeyPath := os.Getenv("APNS_KEY_PATH")
	apnsKeyData := os.Getenv("APNS_KEY_DATA")
	apnsKeyID := os.Getenv("APNS_KEY_ID")
	apnsTeamID := os.Getenv("APNS_TEAM_ID")
	fcmCredPath := os.Getenv("FCM_CREDENTIALS_PATH")

	var apnsCfg *push.APNsConfig
	if (apnsKeyPath != "" || apnsKeyData != "") && apnsKeyID != "" && apnsTeamID != "" {
		apnsCfg = &push.APNsConfig{
			KeyPath:  apnsKeyPath,
			KeyData:  apnsKeyData,
			KeyID:    apnsKeyID,
			TeamID:   apnsTeamID,
			BundleID: env("APNS_BUNDLE_ID", "social.courier.app"),
			Sandbox:  os.Getenv("APNS_SANDBOX") == "true",
		}
		log.Println("apns: configured")
	}

	var fcmClient *messaging.Client
	if fcmCredPath != "" {
		app, err := firebase.NewApp(context.Background(), nil, option.WithCredentialsFile(fcmCredPath))
		if err != nil {
			log.Printf("fcm: init error: %v (continuing without FCM)", err)
		} else {
			fcmClient, err = app.Messaging(context.Background())
			if err != nil {
				log.Printf("fcm: messaging error: %v (continuing without FCM)", err)
			} else {
				log.Println("fcm: configured")
			}
		}
	}

	if apnsCfg == nil && fcmClient == nil {
		log.Println("push: no credentials configured, running without push dispatch")
		return nil
	}

	d, err := push.NewDispatcher(apnsCfg, fcmClient, 10)
	if err != nil {
		log.Printf("push: dispatcher error: %v (continuing without push)", err)
		return nil
	}
	log.Println("push: dispatcher started with 10 workers")
	return d
}

func appNameFromCollection(collection string) string {
	prefixes := map[string]string{
		"sh.tangled":                  "Tangled",
		"community.lexicon.calendar":  "Atmo",
		"com.whtwnd.blog":            "WhiteWind",
		"fyi.unravel.frontpage":      "Frontpage",
		"blue.pico":                  "Picosky",
		"events.smokesignal":         "Smoke Signal",
	}
	for prefix, name := range prefixes {
		if strings.HasPrefix(collection, prefix) {
			return name
		}
	}
	return ""
}

func formatPush(n *EnrichedNotification) (title, body string) {
	name := n.FromHandle
	if n.FromName != "" {
		name = n.FromName
	}

	app := appNameFromCollection(n.Collection)
	context := ""
	if app != "" && !strings.HasPrefix(n.Collection, "app.bsky.") {
		context = " on " + app
	}

	switch classifier.NotificationType(n.Type) {
	case classifier.Like:
		return "New Like", fmt.Sprintf("%s liked your post%s", name, context)
	case classifier.Reply:
		return "New Reply", fmt.Sprintf("%s replied to you%s", name, context)
	case classifier.Repost:
		return "Reposted", fmt.Sprintf("%s reposted your post%s", name, context)
	case classifier.Follow:
		return "New Follower", fmt.Sprintf("%s followed you%s", name, context)
	case classifier.Mention:
		return "Mentioned", fmt.Sprintf("%s mentioned you%s", name, context)
	case classifier.Quote:
		return "Quoted", fmt.Sprintf("%s quoted your post%s", name, context)
	default:
		shortCollection := collectionShortName(n.Collection)
		return shortCollection, fmt.Sprintf("%s via %s", name, shortCollection)
	}
}

func wantsNotification(prefs registry.Preferences, t classifier.NotificationType) bool {
	switch t {
	case classifier.Like:
		return prefs.Likes
	case classifier.Reply:
		return prefs.Replies
	case classifier.Repost:
		return prefs.Reposts
	case classifier.Follow:
		return prefs.Follows
	case classifier.Mention:
		return prefs.Mentions
	case classifier.Quote:
		return prefs.Quotes
	case classifier.Generic:
		return prefs.Generic
	}
	return false
}

type EnrichedNotification struct {
	Type       classifier.NotificationType `json:"type"`
	FromDID    string                      `json:"fromDid"`
	ForDID     string                      `json:"forDid"`
	Collection string                      `json:"collection"`
	URI        string                      `json:"uri"`
	SubjectURI string                      `json:"subjectUri,omitempty"`
	DeepLink   string                      `json:"deepLink,omitempty"`
	FromHandle string                      `json:"fromHandle,omitempty"`
	FromName   string                      `json:"fromName,omitempty"`
	FromAvatar string                      `json:"fromAvatar,omitempty"`
	AppName    string                      `json:"appName,omitempty"`
	AppFavicon string                      `json:"appFavicon,omitempty"`
	CreatedAt  string                      `json:"createdAt"`
}

func enrichNotification(notif *classifier.Notification, profile *hydrator.ActorProfile, lr *resolver.Resolver, reg *registry.Registry) *EnrichedNotification {
	e := &EnrichedNotification{
		Type:       notif.Type,
		FromDID:    notif.FromDID,
		ForDID:     notif.ForDID,
		Collection: notif.Collection,
		URI:        notif.URI,
		SubjectURI: notif.SubjectURI,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if profile != nil {
		e.FromHandle = profile.Handle
		e.FromName = profile.DisplayName
		e.FromAvatar = profile.Avatar
	}
	// Resolve app name from user's preferred apps, then catalog
	if reg != nil {
		// Check user's preferred app for this collection
		preferredURL := reg.GetPreferredAppURL(context.Background(), notif.ForDID, notif.Collection)
		if preferredURL != "" {
			for _, app := range registry.CatalogAll() {
				if app.AppURL == preferredURL {
					e.AppName = app.AppName
					e.AppFavicon = app.FaviconURL
					break
				}
			}
		}
		// Fall back to catalog match
		if e.AppName == "" {
			catalogMap := registry.CatalogByCollection()
			if app := registry.MatchApp(notif.Collection, catalogMap); app != nil {
				e.AppName = app.AppName
				e.AppFavicon = app.FaviconURL
			}
		}
	}

	// Resolve deep link — use preferred app URL if set
	if lr != nil {
		linkURI := notif.SubjectURI
		if linkURI == "" {
			linkURI = notif.URI
		}
		e.DeepLink = lr.ResolveDeepLink(context.Background(), linkURI)

		// Override deep link domain with preferred app if user chose a different client
		if reg != nil {
			preferredURL := reg.GetPreferredAppURL(context.Background(), notif.ForDID, notif.Collection)
			if preferredURL != "" && e.DeepLink != "" {
				// Replace the base URL domain with the preferred app's domain
				// e.g., bsky.app → graysky.app for app.bsky notifications
				e.DeepLink = replaceDeepLinkDomain(e.DeepLink, preferredURL)
			}
		}
	}
	return e
}

func replaceDeepLinkDomain(deepLink, preferredAppURL string) string {
	// Simple domain swap: if deep link is https://bsky.app/profile/...
	// and preferred is https://graysky.app, replace the domain
	// This is a best-effort — not all apps have compatible URL structures
	dlParsed, err1 := url.Parse(deepLink)
	prefParsed, err2 := url.Parse(preferredAppURL)
	if err1 != nil || err2 != nil {
		return deepLink
	}
	if dlParsed.Host != prefParsed.Host {
		dlParsed.Host = prefParsed.Host
		return dlParsed.String()
	}
	return deepLink
}

func collectionShortName(collection string) string {
	parts := strings.Split(collection, ".")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], ".")
	}
	return collection
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
