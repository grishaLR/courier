package jetstream

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	url        string
	dids       map[string]bool
	mu         sync.RWMutex
	conn       *websocket.Conn
	cursor     int64
	onEvent    func(*Event)
	onCursor   func(int64)
	collections []string
}

type Option func(*Client)

func WithCollections(cols []string) Option {
	return func(c *Client) {
		c.collections = cols
	}
}

func WithCursor(cursor int64) Option {
	return func(c *Client) {
		c.cursor = cursor
	}
}

func WithOnCursor(fn func(int64)) Option {
	return func(c *Client) {
		c.onCursor = fn
	}
}

func NewClient(url string, dids []string, onEvent func(*Event), opts ...Option) *Client {
	didSet := make(map[string]bool, len(dids))
	for _, d := range dids {
		didSet[d] = true
	}
	c := &Client{
		url:     url,
		dids:    didSet,
		onEvent: onEvent,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) buildURL() string {
	u := c.url + "?"

	c.mu.RLock()
	dids := make([]string, 0, len(c.dids))
	for did := range c.dids {
		dids = append(dids, did)
	}
	c.mu.RUnlock()

	// Pass wantedDIDs when the set fits within Jetstream's 10k limit.
	// This shifts DID filtering server-side, eliminating the O(N) scan per event.
	if len(dids) > 0 && len(dids) <= 10000 {
		for _, did := range dids {
			u += "wantedDIDs=" + url.QueryEscape(did) + "&"
		}
	}

	for _, col := range c.collections {
		u += "wantedCollections=" + col + "&"
	}

	if c.cursor > 0 {
		u += fmt.Sprintf("cursor=%d", c.cursor)
	}

	return u
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
			log.Printf("jetstream: disconnected (%v), reconnecting in %s", err, backoff)
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
	url := c.buildURL()
	log.Printf("jetstream: connecting to %s", url)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	c.conn = conn
	defer conn.Close()

	log.Println("jetstream: connected")

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
			log.Printf("jetstream: unmarshal error: %v", err)
			continue
		}

		// Update cursor
		if event.TimeUS > 0 {
			c.cursor = event.TimeUS
			if c.onCursor != nil {
				c.onCursor(event.TimeUS)
			}
		}

		c.onEvent(&event)
	}
}

// AddDID adds a DID to the watched set. In this initial version,
// it requires a reconnect. Future: send options update message.
func (c *Client) AddDID(did string) {
	c.mu.Lock()
	c.dids[did] = true
	c.mu.Unlock()
}

// RemoveDID removes a DID from the watched set.
func (c *Client) RemoveDID(did string) {
	c.mu.Lock()
	delete(c.dids, did)
	c.mu.Unlock()
}

// Reconnect closes the active connection so Run() reconnects with the current DID set.
func (c *Client) Reconnect() {
	if c.conn != nil {
		c.conn.Close()
	}
}
