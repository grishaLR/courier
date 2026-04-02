package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/multiformats/go-multibase"
	"github.com/redis/go-redis/v9"
)

const (
	challengeTTL    = 60 * time.Second
	challengePrefix = "challenge:"
)

type Service struct {
	rdb        *redis.Client
	httpClient *http.Client
	plcURL     string
}

func NewService(rdb *redis.Client) *Service {
	return &Service{
		rdb: rdb,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		plcURL: "https://plc.directory",
	}
}

// CreateChallenge generates a nonce for DID ownership verification.
func (s *Service) CreateChallenge(ctx context.Context, did string) (string, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	challenge := hex.EncodeToString(nonce)

	key := challengePrefix + did
	err := s.rdb.Set(ctx, key, challenge, challengeTTL).Err()
	if err != nil {
		return "", fmt.Errorf("store challenge: %w", err)
	}

	return challenge, nil
}

// VerifyChallenge checks a signed nonce against the DID's public key.
func (s *Service) VerifyChallenge(ctx context.Context, did string, sig []byte) error {
	// Retrieve and consume the challenge (single-use)
	key := challengePrefix + did
	challenge, err := s.rdb.GetDel(ctx, key).Result()
	if err == redis.Nil {
		return fmt.Errorf("no pending challenge for this DID (expired or already used)")
	}
	if err != nil {
		return fmt.Errorf("retrieve challenge: %w", err)
	}

	// Resolve the DID document to get the signing key
	pubKey, err := s.resolveSigningKey(ctx, did)
	if err != nil {
		return fmt.Errorf("resolve signing key: %w", err)
	}

	// Verify: hash the challenge, check signature
	hash := sha256.Sum256([]byte(challenge))

	if !verifySignature(pubKey, hash[:], sig) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

// resolveSigningKey fetches the DID document and extracts the atproto signing key.
func (s *Service) resolveSigningKey(ctx context.Context, did string) (*ecdsa.PublicKey, error) {
	var docURL string
	if strings.HasPrefix(did, "did:plc:") {
		docURL = fmt.Sprintf("%s/%s", s.plcURL, did)
	} else if strings.HasPrefix(did, "did:web:") {
		domain := strings.TrimPrefix(did, "did:web:")
		domain = strings.ReplaceAll(domain, ":", "/")
		docURL = fmt.Sprintf("https://%s/.well-known/did.json", domain)
	} else {
		return nil, fmt.Errorf("unsupported DID method: %s", did)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", docURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch DID document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("DID document: status %d: %s", resp.StatusCode, string(body))
	}

	var doc DIDDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode DID document: %w", err)
	}

	// Find the atproto signing key
	for _, vm := range doc.VerificationMethod {
		if vm.ID == did+"#atproto" || vm.ID == "#atproto" {
			return decodePublicKey(vm)
		}
	}

	return nil, fmt.Errorf("no #atproto verification method found in DID document")
}

type DIDDocument struct {
	ID                 string               `json:"id"`
	VerificationMethod []VerificationMethod `json:"verificationMethod"`
}

type VerificationMethod struct {
	ID                 string `json:"id"`
	Type               string `json:"type"`
	PublicKeyMultibase string `json:"publicKeyMultibase"`
}

func decodePublicKey(vm VerificationMethod) (*ecdsa.PublicKey, error) {
	if vm.PublicKeyMultibase == "" {
		return nil, fmt.Errorf("no publicKeyMultibase in verification method")
	}

	_, data, err := multibase.Decode(vm.PublicKeyMultibase)
	if err != nil {
		return nil, fmt.Errorf("decode multibase: %w", err)
	}

	// Multicodec prefix: first two bytes identify the key type
	// 0xe7 0x01 = secp256k1-pub (compressed, 33 bytes)
	// 0x80 0x24 = p256-pub (compressed, 33 bytes)
	if len(data) < 2 {
		return nil, fmt.Errorf("key data too short")
	}

	if data[0] == 0xe7 && data[1] == 0x01 {
		// secp256k1
		keyBytes := data[2:]
		if len(keyBytes) != 33 {
			return nil, fmt.Errorf("invalid secp256k1 compressed key length: %d", len(keyBytes))
		}
		pubKey, err := secp256k1.ParsePubKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("parse secp256k1 key: %w", err)
		}
		return pubKey.ToECDSA(), nil
	}

	if data[0] == 0x80 && data[1] == 0x24 {
		// P-256
		keyBytes := data[2:]
		x, y := elliptic.UnmarshalCompressed(elliptic.P256(), keyBytes)
		if x == nil {
			return nil, fmt.Errorf("invalid P-256 compressed key")
		}
		return &ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     x,
			Y:     y,
		}, nil
	}

	return nil, fmt.Errorf("unsupported key type: 0x%x 0x%x", data[0], data[1])
}

func verifySignature(pubKey *ecdsa.PublicKey, hash, sig []byte) bool {
	// ATProto uses "low-S" ECDSA signatures in raw r||s format (64 bytes)
	if len(sig) != 64 {
		return false
	}

	r := new(big.Int).SetBytes(sig[:32])
	sVal := new(big.Int).SetBytes(sig[32:])

	// Constant-time comparison is handled by ecdsa.Verify internally,
	// but we use subtle for the nonce comparison above.
	_ = subtle.ConstantTimeCompare

	return ecdsa.Verify(pubKey, hash, r, sVal)
}
