package spacedust

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Event is the raw Spacedust WebSocket message.
type Event struct {
	Kind   string `json:"kind"`   // "link"
	Origin string `json:"origin"` // "live" or "backfill"
	Link   Link   `json:"link"`
}

// Link represents an extracted interaction.
type Link struct {
	Operation    string `json:"operation"`     // "create" or "delete"
	Source       string `json:"source"`        // e.g., "app.bsky.feed.like:subject.uri"
	SourceRecord string `json:"source_record"` // AT URI of the record that created the link
	SourceRev    string `json:"source_rev"`
	Subject      string `json:"subject"` // the target AT URI or DID
}

// Collection returns the collection from the source (before the colon).
func (l *Link) Collection() string {
	parts := strings.SplitN(l.Source, ":", 2)
	return parts[0]
}

// SourceDID extracts the DID of the actor from the source_record AT URI.
func (l *Link) SourceDID() string {
	// at://did:plc:xxx/collection/rkey → did:plc:xxx
	stripped := strings.TrimPrefix(l.SourceRecord, "at://")
	parts := strings.SplitN(stripped, "/", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// SourceRKey extracts the rkey from the source_record AT URI.
func (l *Link) SourceRKey() string {
	stripped := strings.TrimPrefix(l.SourceRecord, "at://")
	parts := strings.Split(stripped, "/")
	if len(parts) == 3 {
		return parts[2]
	}
	return ""
}

// Source defines a link source to subscribe to.
type Source string

var DefaultSources = []Source{
	"app.bsky.feed.like:subject.uri",
	"app.bsky.feed.repost:subject.uri",
	"app.bsky.feed.post:reply.parent.uri",
	"app.bsky.feed.post:embed.record.uri",
	"app.bsky.feed.post:facets.features.did",
	"app.bsky.graph.follow:subject",
	// Tangled
	"sh.tangled.feed.reaction:subject.uri",
	"sh.tangled.repo.comment:issue",
}

type Client struct {
	url     string
	sources []Source
	dids    map[string]bool
	mu      sync.RWMutex
	conn    *websocket.Conn
	onEvent func(*Event)
	instant bool
}

type Option func(*Client)

func WithInstant(instant bool) Option {
	return func(c *Client) {
		c.instant = instant
	}
}

func WithSources(sources []Source) Option {
	return func(c *Client) {
		c.sources = sources
	}
}

func NewClient(wsURL string, dids []string, onEvent func(*Event), opts ...Option) *Client {
	didSet := make(map[string]bool, len(dids))
	for _, d := range dids {
		didSet[d] = true
	}
	c := &Client{
		url:     wsURL,
		sources: DefaultSources,
		dids:    didSet,
		onEvent: onEvent,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) buildURL() string {
	u, _ := url.Parse(c.url)
	q := u.Query()

	for _, src := range c.sources {
		q.Add("wantedSources", string(src))
	}

	c.mu.RLock()
	for did := range c.dids {
		q.Add("wantedSubjectDids", did)
	}
	c.mu.RUnlock()

	if c.instant {
		q.Set("instant", "true")
	}

	u.RawQuery = q.Encode()
	return u.String()
}

func (c *Client) Run(ctx context.Context) error {
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := c.connect(ctx)
		if err != nil {
			attempt++
			backoff := time.Duration(math.Min(float64(time.Second)*math.Pow(2, float64(attempt)), 5)) * time.Second
			log.Printf("spacedust: disconnected (%v), reconnecting in %s", err, backoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}
		attempt = 0
	}
}

func (c *Client) connect(ctx context.Context) error {
	wsURL := c.buildURL()
	log.Printf("spacedust: connecting to %s", wsURL)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	c.conn = conn
	defer conn.Close()

	log.Println("spacedust: connected")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var event Event
		if err := json.Unmarshal(msg, &event); err != nil {
			log.Printf("spacedust: unmarshal error: %v", err)
			continue
		}

		// Only process create operations
		if event.Link.Operation != "create" {
			continue
		}

		c.onEvent(&event)
	}
}

func (c *Client) AddDID(did string) {
	c.mu.Lock()
	c.dids[did] = true
	c.mu.Unlock()
}

func (c *Client) RemoveDID(did string) {
	c.mu.Lock()
	delete(c.dids, did)
	c.mu.Unlock()
}

func (c *Client) Reconnect() {
	if c.conn != nil {
		c.conn.Close()
	}
}
