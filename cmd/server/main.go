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
	"github.com/grishalr/courier-social/internal/moderation"
	"github.com/grishalr/courier-social/internal/push"
	"github.com/grishalr/courier-social/internal/registry"
	"github.com/grishalr/courier-social/internal/resolver"
	"github.com/grishalr/courier-social/internal/spacedust"
	"github.com/grishalr/courier-social/internal/subscriptions"
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

	// Load app catalog into resolver for deep link fallback
	catalogMap := registry.CatalogByCollection()
	resolverCatalog := make(map[string]resolver.AppCatalogEntry, len(catalogMap))
	for prefix, app := range catalogMap {
		resolverCatalog[prefix] = resolver.AppCatalogEntry{
			AppURL:           app.AppURL,
			CollectionPrefix: prefix,
		}
	}
	linkResolver.SetCatalog(resolverCatalog)

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

	// Moderation layer — velocity + blocklist checks
	mod := moderation.New(rdb)

	// Blog subscription manager
	subMgr := subscriptions.New(rdb)
	if err := subMgr.RebuildFromRedis(ctx, watchedDIDs); err != nil {
		log.Printf("subscriptions: rebuild error: %v", err)
	}

	// Shared notification processing — used by both Spacedust and Jetstream
	processNotification := func(notif *classifier.Notification) {
		did := notif.ForDID

		// Dedup — only one machine should process each notification
		dedupKey := fmt.Sprintf("dedup:%s:%s", did, notif.URI)
		if !rdb.SetNX(context.Background(), dedupKey, "1", 60*time.Second).Val() {
			log.Printf("🔍 dedup: skipping %s for %s", notif.URI, did)
			return // another machine already processed this
		}

		// Moderation check — drop notifications from labeled bad actors
		if !mod.Check(context.Background(), notif.FromDID) {
			log.Printf("🔍 mod: blocked %s from %s", notif.Type, notif.FromDID)
			return
		}

		user, err := reg.GetUser(context.Background(), did)
		if err != nil {
			log.Printf("🔍 no user for %s: %v", did, err)
			return
		}
		if !wantsNotification(user.Preferences, notif.Type) {
			log.Printf("🔍 pref: user %s doesn't want %s", did, notif.Type)
			return
		}
		// Check per-app preferences
		if !reg.IsAppEnabled(context.Background(), did, notif.Collection) {
			log.Printf("🔍 app: user %s has %s disabled", did, notif.Collection)
			return
		}

		profile, _ := hyd.GetProfile(context.Background(), notif.FromDID)
		enriched := enrichNotification(notif, profile, linkResolver, reg)

		reg.StoreNotification(context.Background(), did, enriched)

		log.Printf("📬 %s [%s] from %s → %s", notif.Type, notif.Collection, notif.FromDID, did)

		notifHub.Broadcast(did, enriched)

		if dispatcher == nil {
			log.Printf("⚠️ push: no dispatcher configured")
		} else if user.DeviceToken == "" {
			log.Printf("⚠️ push: no device token for %s", did)
		}
		if dispatcher != nil && user.DeviceToken != "" {
			title, body := formatPush(enriched)
			pushNotif := &push.Notification{
				Title:    title,
				Body:     body,
				Token:    user.DeviceToken,
				Platform: user.Platform,
				Category: string(notif.Type),
				Data: map[string]string{
					"type":        string(notif.Type),
					"uri":         notif.URI,
					"fromDid":     notif.FromDID,
					"fromAvatar":  enriched.FromAvatar,
					"appFavicon":  enriched.AppFavicon,
					"deepLink":    enriched.DeepLink,
					"subjectText": enriched.SubjectText,
				},
			}
			log.Printf("📲 push: sending %s to %s (%s token %s…)", notif.Type, did, user.Platform, user.DeviceToken[:8])
			go func(token string) {
				result := dispatcher.Send(pushNotif)
				if result.Err != nil {
					log.Printf("⚠️ push: send failed for %s: %v", token[:8], result.Err)
				} else {
					log.Printf("✅ push: sent to %s…", token[:8])
				}
				if result.BadToken {
					select {
					case badTokens <- token:
					default:
					}
				}
			}(user.DeviceToken)
		}
	}

	// Process a blog post notification with publication metadata
	processBlogNotification := func(notif *classifier.Notification, pubInfo *subscriptions.PublicationInfo) {
		did := notif.ForDID

		dedupKey := fmt.Sprintf("dedup:%s:%s", did, notif.URI)
		if !rdb.SetNX(context.Background(), dedupKey, "1", 60*time.Second).Val() {
			return
		}

		if !mod.Check(context.Background(), notif.FromDID) {
			return
		}

		user, err := reg.GetUser(context.Background(), did)
		if err != nil {
			return
		}
		if !wantsNotification(user.Preferences, notif.Type) {
			return
		}

		profile, _ := hyd.GetProfile(context.Background(), notif.FromDID)
		enriched := enrichNotification(notif, profile, linkResolver, reg)

		// Override app name/icon with publication info
		if pubInfo != nil {
			if pubInfo.Name != "" {
				enriched.AppName = pubInfo.Name
			}
			if pubInfo.IconURL != "" {
				enriched.AppFavicon = pubInfo.IconURL
			}
		}

		reg.StoreNotification(context.Background(), did, enriched)

		log.Printf("📬 %s [%s] from %s → %s", notif.Type, notif.Collection, notif.FromDID, did)

		notifHub.Broadcast(did, enriched)

		if dispatcher == nil {
			log.Printf("⚠️ blog push: no dispatcher")
		} else if user.DeviceToken == "" {
			log.Printf("⚠️ blog push: no device token for %s", did)
		} else {
			title, body := formatPush(enriched)
			log.Printf("📤 blog push: %s → %s: %s", did, title, body)
			pushNotif := &push.Notification{
				Title:    title,
				Body:     body,
				Token:    user.DeviceToken,
				Platform: user.Platform,
				Category: string(notif.Type),
				Data: map[string]string{
					"type":        string(notif.Type),
					"uri":         notif.URI,
					"fromDid":     notif.FromDID,
					"fromAvatar":  enriched.FromAvatar,
					"appFavicon":  enriched.AppFavicon,
					"deepLink":    enriched.DeepLink,
					"subjectText": enriched.SubjectText,
				},
			}
			go func(token string) {
				result := dispatcher.Send(pushNotif)
				if result.Err != nil {
					log.Printf("⚠️ blog push error: %v", result.Err)
				}
				if result.BadToken {
					select {
					case badTokens <- token:
					default:
					}
				}
			}(user.DeviceToken)
		}
	}

	// Bounded worker pool — event handlers enqueue; workers do the slow Redis/HTTP work.
	// Keeps the Jetstream/Spacedust read goroutines free to drain the TCP buffer.
	type notifJob struct {
		notif   *classifier.Notification
		pubInfo *subscriptions.PublicationInfo // non-nil for blog posts
	}
	const workerCount = 16
	const queueDepth = 512
	notifQueue := make(chan notifJob, queueDepth)
	for i := 0; i < workerCount; i++ {
		go func() {
			for job := range notifQueue {
				if job.pubInfo != nil {
					processBlogNotification(job.notif, job.pubInfo)
				} else {
					processNotification(job.notif)
				}
			}
		}()
	}
	enqueue := func(notif *classifier.Notification, pubInfo *subscriptions.PublicationInfo) {
		select {
		case notifQueue <- notifJob{notif: notif, pubInfo: pubInfo}:
		default:
			log.Printf("⚠️ notif queue full, dropping %s for %s", notif.URI, notif.ForDID)
		}
	}

	// Blog post notification processing — when a subscribed author publishes a new document
	processBlogPost := func(event *jetstream.Event, platform string) {
		if event.Commit == nil || event.Commit.Operation != "create" {
			return
		}

		authorDID := event.Did
		subscribers := subMgr.GetSubscribers(authorDID)
		if len(subscribers) == 0 {
			return
		}

		// Find which publication this document belongs to
		pubURI := subMgr.FindPublicationForDocument(
			context.Background(), authorDID, event.Commit.Collection, event.Commit.Record,
		)

		uri := fmt.Sprintf("at://%s/%s/%s", event.Did, event.Commit.Collection, event.Commit.RKey)

		// Resolve publication info (name + icon) once for all subscribers
		var pubInfo *subscriptions.PublicationInfo
		if pubURI != "" {
			pubInfo = subMgr.ResolvePublicationInfo(context.Background(), pubURI)
		}

		for _, subDID := range subscribers {
			// Check if this subscriber has notifications enabled for this blog
			if pubURI != "" && !subMgr.IsBlogEnabled(context.Background(), subDID, pubURI) {
				continue
			}

			notif := &classifier.Notification{
				Type:       classifier.BlogPost,
				FromDID:    authorDID,
				ForDID:     subDID,
				Collection: event.Commit.Collection,
				URI:        uri,
				Record:     event.Commit.Record,
			}

			// Enqueue for async processing — keeps the event read goroutine free
			enqueue(notif, pubInfo)
		}
	}

	// Handle subscription create/delete events from Jetstream
	processSubscriptionEvent := func(event *jetstream.Event) {
		if event.Commit == nil {
			return
		}
		collection := event.Commit.Collection
		if !subscriptions.IsSubscriptionCollection(collection) {
			return
		}

		didMu.RLock()
		isWatched := didSet[event.Did]
		didMu.RUnlock()
		if !isWatched {
			return
		}

		switch event.Commit.Operation {
		case "create":
			subMgr.HandleNewSubscription(context.Background(), event.Did, collection, event.Commit.Record)
		case "delete":
			subMgr.HandleDeleteSubscription(context.Background(), event.Did, collection, event.Commit.RKey)
		}
	}

	// Jetstream event handler (legacy/fallback)
	onJetstreamEvent := func(event *jetstream.Event) {
		// In hybrid mode Jetstream only needs to handle non-bsky events.
		// wantedDIDs filters server-side so only events FROM watched users/authors arrive.
		if useSpacedust && event.Commit != nil && strings.HasPrefix(event.Commit.Collection, "app.bsky.") {
			return
		}

		if event.Commit != nil {
			// Blog post from a subscribed author
			if platform, ok := subscriptions.IsDocumentCollection(event.Commit.Collection); ok {
				processBlogPost(event, platform)
			}
			// Subscription creates/deletes from watched users
			processSubscriptionEvent(event)
		}

		// In hybrid mode, wantedDIDs already filtered to watched users and blog authors.
		// Events from those DIDs are "own writes" for notification purposes — skip the scan.
		if useSpacedust {
			return
		}

		// Pure-Jetstream mode: scan all watched users to find who this event targets.
		didMu.RLock()
		defer didMu.RUnlock()

		if didSet[event.Did] {
			return // skip own writes
		}

		for did := range didSet {
			notif := classifier.Classify(event, did)
			if notif == nil {
				continue
			}
			enqueue(notif, nil)
		}
	}

	// Spacedust event handler
	onSpacedustEvent := func(event *spacedust.Event) {
		notif := spacedust.ClassifyLink(event)
		if notif == nil {
			return
		}
		log.Printf("🔍 spacedust event: %s from %s → subject=%s", notif.Collection, notif.FromDID, notif.ForDID)
		// Skip own writes
		didMu.RLock()
		isOwnWrite := didSet[notif.FromDID]
		didMu.RUnlock()
		if isOwnWrite {
			log.Printf("🔍 spacedust: skipping own write from %s", notif.FromDID)
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
		enqueue(notif, nil)
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
		// Also watch subscribed blog authors so their posts arrive via wantedDIDs filter
		for _, authorDID := range subMgr.GetAuthorDIDs() {
			jsClient.AddDID(authorDID)
		}
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
			jsClient.Reconnect() // Reconnect so wantedDIDs filter includes new DID
		}
		// Discover blog subscriptions in background; reconnect Jetstream again
		// once author DIDs are known so they're included in wantedDIDs.
		go func() {
			if err := subMgr.DiscoverAndStore(context.Background(), did); err != nil {
				log.Printf("subscriptions: discovery error for %s: %v", did, err)
				return
			}
			// Seed newly discovered author DIDs into Jetstream
			if jsClient != nil {
				for _, authorDID := range subMgr.GetAuthorDIDs() {
					jsClient.AddDID(authorDID)
				}
				jsClient.Reconnect()
			}
		}()
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
	blogH := handlers.NewBlogHandlers(subMgr)
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
		r.Post("/auth/oauth/exchange", oh.Exchange)
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

		// Notifications (read-only, scoped by DID in path)
		r.Get("/notifications/{did}", h.GetNotifications)

		// Registration is public — DID ownership is asserted but not cryptographically
		// verified at this layer; the DeviceToken only delivers to the registered app.
		r.Post("/register", h.Register)
	})

	// ── Protected API (requires authenticated session) ───
	r.Group(func(r chi.Router) {
		r.Use(handlers.RequireAuth(oauthSessions, reg))
		r.Put("/preferences", h.UpdatePreferences)
		r.Put("/device-token", h.UpdateDeviceToken)
		r.Delete("/unregister", h.Unregister)
		r.Delete("/notifications", h.ClearNotifications)
		r.Post("/notifications/delete", h.DeleteNotification)

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

		// Blog subscriptions
		r.Get("/subscriptions/blogs", blogH.GetBlogSubs)
		r.Put("/subscriptions/blogs", blogH.SetBlogPref)
		r.Post("/subscriptions/blogs/refresh", blogH.RefreshBlogSubs)
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
	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	srv.Shutdown(shutdownCtx)
	reg.SaveCursor(context.Background(), 0)
}

func initPushDispatcher() *push.Dispatcher {
	apnsKeyPath := os.Getenv("APNS_KEY_PATH")
	apnsKeyData := os.Getenv("APNS_KEY_DATA")
	apnsKeyID := os.Getenv("APNS_KEY_ID")
	apnsTeamID := os.Getenv("APNS_TEAM_ID")
	fcmCredPath := os.Getenv("FCM_CREDENTIALS_PATH")
	fcmCredJSON := os.Getenv("FCM_CREDENTIALS_JSON")

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
	var fcmApp *firebase.App
	var fcmErr error
	if fcmCredPath != "" {
		fcmApp, fcmErr = firebase.NewApp(context.Background(), nil, option.WithCredentialsFile(fcmCredPath))
	} else if fcmCredJSON != "" {
		fcmApp, fcmErr = firebase.NewApp(context.Background(), nil, option.WithCredentialsJSON([]byte(fcmCredJSON)))
	}
	if fcmApp != nil && fcmErr == nil {
		fcmClient, fcmErr = fcmApp.Messaging(context.Background())
		if fcmErr != nil {
			log.Printf("fcm: messaging error: %v (continuing without FCM)", fcmErr)
		} else {
			log.Println("fcm: configured")
		}
	} else if fcmCredPath != "" || fcmCredJSON != "" {
		log.Printf("fcm: init error: %v (continuing without FCM)", fcmErr)
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

	appLabel := n.AppName
	if appLabel == "" {
		appLabel = appNameFromCollection(n.Collection)
	}

	// Build title suffix: " from Bluesky", " from Tangled", etc.
	titleSuffix := ""
	if appLabel != "" {
		titleSuffix = " from " + appLabel
	}

	preview := ""
	if n.SubjectText != "" {
		preview = "\n" + n.SubjectText
	}

	noun := subjectNoun(n.SubjectURI)

	switch classifier.NotificationType(n.Type) {
	case classifier.Like:
		return "New Like" + titleSuffix, fmt.Sprintf("%s liked your %s%s", name, noun, preview)
	case classifier.Favorite:
		return "New Favorite" + titleSuffix, fmt.Sprintf("%s favorited your %s%s", name, noun, preview)
	case classifier.Reply:
		return "New Reply" + titleSuffix, fmt.Sprintf("%s replied to you%s", name, preview)
	case classifier.Repost:
		return "Reposted" + titleSuffix, fmt.Sprintf("%s reposted your %s%s", name, noun, preview)
	case classifier.Follow:
		return "New Follower" + titleSuffix, fmt.Sprintf("%s followed you", name)
	case classifier.Mention:
		return "Mentioned" + titleSuffix, fmt.Sprintf("%s mentioned you%s", name, preview)
	case classifier.Quote:
		return "Quoted" + titleSuffix, fmt.Sprintf("%s quoted your post", name)
	case classifier.Star:
		return "New Star" + titleSuffix, fmt.Sprintf("%s starred your repo%s", name, preview)
	case classifier.Reaction:
		return "New Reaction" + titleSuffix, fmt.Sprintf("%s reacted to your post%s", name, preview)
	case classifier.Issue:
		return "New Issue" + titleSuffix, fmt.Sprintf("%s opened an issue%s", name, preview)
	case classifier.PullRequest:
		return "New PR" + titleSuffix, fmt.Sprintf("%s opened a pull request%s", name, preview)
	case classifier.RSVP:
		// SubjectText includes "going — Event Name" or "interested — Event Name"
		if n.SubjectText != "" && (strings.HasPrefix(n.SubjectText, "going") || strings.HasPrefix(n.SubjectText, "interested") || strings.HasPrefix(n.SubjectText, "not going")) {
			parts := strings.SplitN(n.SubjectText, " — ", 2)
			status := parts[0]
			eventPreview := ""
			if len(parts) > 1 {
				eventPreview = "\n" + parts[1]
			}
			return "New RSVP" + titleSuffix, fmt.Sprintf("%s is %s to your event%s", name, status, eventPreview)
		}
		return "New RSVP" + titleSuffix, fmt.Sprintf("%s RSVPed to your event%s", name, preview)
	case classifier.Subscription:
		return "New Subscriber" + titleSuffix, fmt.Sprintf("%s subscribed to your publication", name)
	case classifier.Play:
		return "New Play" + titleSuffix, fmt.Sprintf("%s played your track%s", name, preview)
	case classifier.Recommend:
		return "New Recommend" + titleSuffix, fmt.Sprintf("%s recommended your post%s", name, preview)
	case classifier.Vote:
		return "New Vote" + titleSuffix, fmt.Sprintf("%s voted on your poll%s", name, preview)
	case classifier.BlogPost:
		return "New Post" + titleSuffix, fmt.Sprintf("%s published a new post%s", name, preview)
	default:
		if appLabel == "" {
			appLabel = collectionShortName(n.Collection)
		}
		return appLabel, fmt.Sprintf("%s interacted with your content on %s", name, appLabel)
	}
}

func wantsNotification(prefs registry.Preferences, t classifier.NotificationType) bool {
	switch t {
	case classifier.Like, classifier.Favorite, classifier.Star, classifier.Reaction:
		return prefs.Likes
	case classifier.Reply:
		return prefs.Replies
	case classifier.Repost:
		return prefs.Reposts
	case classifier.Follow, classifier.Subscription:
		return prefs.Follows
	case classifier.Mention:
		return prefs.Mentions
	case classifier.Quote:
		return prefs.Quotes
	case classifier.BlogPost:
		return true // blog posts are controlled by per-blog toggles, not type preferences
	case classifier.Issue, classifier.PullRequest, classifier.RSVP, classifier.Play, classifier.Recommend, classifier.Vote, classifier.Generic:
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
	AppName     string                      `json:"appName,omitempty"`
	AppFavicon  string                      `json:"appFavicon,omitempty"`
	SubjectText string                      `json:"subjectText,omitempty"`
	CreatedAt   string                      `json:"createdAt"`
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

	// Fetch subject text preview (what was liked/replied to)
	if lr != nil {
		switch {
		case notif.Type == classifier.BlogPost:
			// Blog posts: the document IS the content — fetch title from the notification's own URI
			e.SubjectText = lr.FetchRecordText(context.Background(), notif.URI)
		case notif.SubjectURI != "":
			e.SubjectText = lr.FetchRecordText(context.Background(), notif.SubjectURI)
		}
	}

	// Resolve deep link
	// For interaction records (recommend, comment, star, reaction, rsvp), resolve using
	// the notification's own URI so the deep link goes to the source app, not the subject's app.
	// For content records (reply, quote, mention), use subjectURI to link to the content.
	if lr != nil {
		linkURI := notif.URI
		switch notif.Type {
		case classifier.Reply, classifier.Quote, classifier.Mention, classifier.Repost, classifier.Like, classifier.Favorite:
			if notif.SubjectURI != "" {
				linkURI = notif.SubjectURI
			}
		}
		e.DeepLink = lr.ResolveDeepLink(context.Background(), linkURI)

		// Override deep link domain with preferred app if user chose a different client
		// Skip for blog posts — their deep link comes from the document's own site field
		if reg != nil && notif.Type != classifier.BlogPost {
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

func subjectNoun(subjectURI string) string {
	// Specific overrides for compound or non-obvious nouns
	switch {
	case strings.Contains(subjectURI, "repo.issue"):
		return "issue"
	case strings.Contains(subjectURI, "repo.pull"):
		return "pull request"
	case strings.Contains(subjectURI, "calendar.event"):
		return "event"
	case strings.Contains(subjectURI, "document"), strings.Contains(subjectURI, "blog"), strings.Contains(subjectURI, "entry"):
		return "post"
	}

	// Extract the last segment of the collection from the AT URI as the noun.
	// e.g., "at://did/social.arabica.alpha.recipe/rkey" → "recipe"
	// e.g., "at://did/social.grain.photo/rkey" → "photo"
	if strings.HasPrefix(subjectURI, "at://") {
		parts := strings.SplitN(strings.TrimPrefix(subjectURI, "at://"), "/", 3)
		if len(parts) >= 2 {
			collection := parts[1]
			segments := strings.Split(collection, ".")
			noun := segments[len(segments)-1]
			// Skip generic collection names that don't make good nouns
			switch noun {
			case "feed", "graph", "interactions", "app", "social", "alpha", "dev":
				return "post"
			default:
				return noun
			}
		}
	}

	return "post"
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
