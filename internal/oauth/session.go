package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type SessionStore struct {
	rdb *redis.Client
}

type Session struct {
	DID    string `json:"did"`
	Handle string `json:"handle,omitempty"`
}

func NewSessionStore(rdb *redis.Client) *SessionStore {
	return &SessionStore{rdb: rdb}
}

// SaveOAuthState stores the OAuth flow state in Redis keyed by the state param.
func (s *SessionStore) SaveOAuthState(ctx context.Context, state string, oauthState *OAuthState) error {
	data, err := json.Marshal(oauthState)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, "oauth_state:"+state, data, 10*time.Minute).Err()
}

// GetOAuthState retrieves and deletes the OAuth flow state (single-use).
func (s *SessionStore) GetOAuthState(ctx context.Context, state string) (*OAuthState, error) {
	data, err := s.rdb.GetDel(ctx, "oauth_state:"+state).Bytes()
	if err != nil {
		return nil, fmt.Errorf("invalid or expired state")
	}
	var oauthState OAuthState
	if err := json.Unmarshal(data, &oauthState); err != nil {
		return nil, err
	}
	return &oauthState, nil
}

// CreateSession creates a new session for an authenticated DID.
func (s *SessionStore) CreateSession(ctx context.Context, did, handle string) (string, error) {
	token := generateSessionToken()
	session := Session{DID: did, Handle: handle}
	data, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	if err := s.rdb.Set(ctx, "session:"+token, data, 7*24*time.Hour).Err(); err != nil {
		return "", err
	}
	return token, nil
}

// GetSession retrieves a session by token.
func (s *SessionStore) GetSession(ctx context.Context, token string) (*Session, error) {
	data, err := s.rdb.Get(ctx, "session:"+token).Bytes()
	if err != nil {
		return nil, fmt.Errorf("invalid or expired session")
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// DeleteSession removes a session.
func (s *SessionStore) DeleteSession(ctx context.Context, token string) error {
	return s.rdb.Del(ctx, "session:"+token).Err()
}

func generateSessionToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
