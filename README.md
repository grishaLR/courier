# Courier

**One notification inbox for the whole ATmosphere.**

Courier ([courier.social](https://courier.social)) is a push-notification service for the [AT Protocol](https://atproto.com) ecosystem. Bluesky, Tangled, Grain, Smoke Signal, Leaflet and every other ATProto app write records into the same network. Courier watches that network and tells you when someone interacts with you, whichever app they used: likes, replies, follows, mentions, RSVPs, stars, new posts from blogs you subscribe to, and so on.

Sign in with your ATProto handle (OAuth). Courier then delivers notifications to the iOS or Android app over WebSocket while it's open, and through APNs/FCM push when it isn't. Tapping a notification deep-links into the app it came from.

The repo also contains the **ATmosphere App Catalog**, a crowdsourced directory of ATProto apps that maps record collections (NSIDs) to the apps that own them. Courier uses it to label notifications and build deep links. It's published at courier.social.

## How it works

```
 Jetstream (records)  ─┐
                       ├─▶  classifier ─▶ moderation ─▶ hydrator ─▶ dedup ─┬─▶ WebSocket (app open)
 Spacedust (backlinks) ┘   "does this      (label       (profiles,  (Redis) ├─▶ APNs (iOS)
                            touch a         check)       record      │      └─▶ FCM (Android)
                            watched DID?"               content)     ▼
                                                                  Redis: users, prefs,
                                                                  history, cursors, catalog
```

- **Firehose ingestion:** the server reads the full [Jetstream](https://github.com/bluesky-social/jetstream) firehose and [Spacedust](https://spacedust.microcosm.blue) backlink stream. Events come from *other* users' repos, so there is no `wantedDids` filter. The classifier finds records that reference a watched user.
- **Notification types are inferred, not hardcoded.** The type comes from the collection name (`*.like` → Like, `*.follow` → Follow, …) together with the app catalog, so new ATProto apps work without code changes. A few Bluesky collections get custom parsing.
- **Moderation:** sender DIDs are checked against Bluesky's label service, with results cached in Redis.
- **Multi-instance safe:** several server instances can consume the firehose at once. Redis `SETNX` dedup keeps each notification from being delivered twice.

## Repository layout

| Path | What it is |
|---|---|
| `cmd/server/` | Go entry point: wires up firehose consumers, the pipeline and the HTTP server |
| `api/handlers/` | HTTP + WebSocket handlers (auth, OAuth, catalog, apps, blogs, notifications) |
| `internal/jetstream`, `internal/spacedust` | Firehose clients with cursor persistence |
| `internal/classifier` | Decides whether an event is a notification for a watched user |
| `internal/hydrator` | Fetches profiles and record content for rich notifications |
| `internal/moderation` | Label-based sender filtering |
| `internal/push` | APNs and FCM delivery |
| `internal/registry` | User registry, preferences, app catalog |
| `internal/resolver` | Resolves `at://` URIs to web deep links |
| `internal/oauth`, `internal/auth` | ATProto OAuth (DPoP/PAR/PKCE) and signature-challenge auth |
| `internal/subscriptions` | Blog subscriptions (`site.standard.*`) |
| `ios/Courier/` | Native iOS app (SwiftUI) + Notification Service Extension for rich pushes |
| `android/` | Native Android app (Kotlin, Jetpack Compose, FCM) |
| `web/` | Astro site for courier.social: app catalog, privacy, terms |
| `web/public/app_catalog.json` | The app catalog data (also bundled into the server image) |

## Running locally

### Server

Requirements: Go (see `go.mod`) and Redis.

```sh
redis-server &                 # or: docker run -p 6379:6379 redis
go run ./cmd/server            # listens on :8080
```

Push delivery is optional. Without APNs/FCM credentials the server still runs and delivers over WebSocket.

| Variable | Default | Purpose |
|---|---|---|
| `REDIS_URL` | `localhost:6379` | Redis address |
| `PORT` | `8080` | HTTP port |
| `BASE_URL` | `https://courier.social` | Public URL used for OAuth client metadata/callbacks |
| `JETSTREAM_URL` | `wss://jetstream2.us-east.bsky.network/subscribe` | Jetstream endpoint |
| `SPACEDUST_URL` | `wss://spacedust.microcosm.blue/subscribe` | Spacedust endpoint |
| `USE_SPACEDUST` | `true` | Enable the Spacedust consumer |
| `SPACEDUST_INSTANT` | — | `true` to skip Spacedust's delay buffer |
| `BSKY_API_URL` | `https://public.api.bsky.app` | AppView used for profile hydration |
| `CATALOG_PATH` | `web/public/app_catalog.json` | App catalog file to seed from |
| `APNS_KEY_PATH` / `APNS_KEY_DATA` | — | APNs `.p8` key, as a file path or inline |
| `APNS_KEY_ID`, `APNS_TEAM_ID` | — | APNs key identifiers |
| `APNS_BUNDLE_ID` | `social.courier.app` | iOS bundle ID |
| `APNS_SANDBOX` | — | `true` for development builds (TestFlight/App Store use production) |
| `FCM_CREDENTIALS_PATH` / `FCM_CREDENTIALS_JSON` | — | Firebase service-account credentials |

### iOS

Open `ios/Courier/Courier.xcodeproj` in Xcode, select your own development team, and run. Push notifications need a real device and an APNs key that matches your bundle ID.

### Android

Open `android/` in Android Studio. You need your own `app/google-services.json` from a Firebase project. Release signing reads from `local.properties`.

### Web

```sh
cd web
npm install
npm run dev        # http://localhost:4321
```

## Deployment

- **API**: `api.courier.social` on [Fly.io](https://fly.io) (`fly.toml`, `Dockerfile`). Set secrets with `fly secrets set`.
- **Web**: `courier.social` on Vercel, built from `web/`.

## Status

The iOS app is in TestFlight. The Android app is on Google Play.

## License

[MIT](LICENSE)
