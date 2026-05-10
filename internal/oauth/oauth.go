package oauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AuthServerMetadata from the PDS's .well-known/oauth-authorization-server
type AuthServerMetadata struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	PAREndpoint           string `json:"pushed_authorization_request_endpoint,omitempty"`
	RevocationEndpoint    string `json:"revocation_endpoint,omitempty"`
}

// TokenResponse from the auth server's token endpoint
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Sub         string `json:"sub"`
	Scope       string `json:"scope"`
}

// PARResponse from the pushed authorization request
type PARResponse struct {
	RequestURI string `json:"request_uri"`
}

// DIDDocument for resolving PDS endpoint
type DIDDocument struct {
	Service []DIDService `json:"service"`
}

type DIDService struct {
	Type            string `json:"type"`
	ServiceEndpoint string `json:"serviceEndpoint"`
}

// OAuthState stored in Redis during the auth flow
type OAuthState struct {
	CodeVerifier string `json:"codeVerifier"`
	DPoP         DPoPState `json:"dpop"`
	DID          string `json:"did"`
	AuthServer   AuthServerMetadata `json:"authServer"`
	Mobile       bool   `json:"mobile,omitempty"`
}

type DPoPState struct {
	D string `json:"d"`
	X string `json:"x"`
	Y string `json:"y"`
}

// ResolveHandle resolves an AT Protocol handle to a DID.
func ResolveHandle(handle string) (string, error) {
	handle = strings.TrimPrefix(handle, "@")
	resp, err := http.Get("https://public.api.bsky.app/xrpc/com.atproto.identity.resolveHandle?handle=" + url.QueryEscape(handle))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct{ DID string `json:"did"` }
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.DID == "" {
		return "", fmt.Errorf("could not resolve handle %s", handle)
	}
	return result.DID, nil
}

// ResolvePDS resolves a DID to its PDS endpoint.
func ResolvePDS(did string) (string, error) {
	if !strings.HasPrefix(did, "did:plc:") && !strings.HasPrefix(did, "did:web:") {
		return "", fmt.Errorf("unsupported DID method")
	}

	var docURL string
	if strings.HasPrefix(did, "did:plc:") {
		docURL = "https://plc.directory/" + did
	} else {
		host := strings.TrimPrefix(did, "did:web:")
		docURL = "https://" + host + "/.well-known/did.json"
	}

	resp, err := http.Get(docURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var doc DIDDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", err
	}

	for _, svc := range doc.Service {
		if svc.Type == "AtprotoPersonalDataServer" {
			return svc.ServiceEndpoint, nil
		}
	}
	return "", fmt.Errorf("no PDS found for %s", did)
}

// FetchAuthServerMetadata resolves a PDS to its authorization server and fetches
// the AS metadata. The PDS advertises its AS via /.well-known/oauth-protected-resource;
// for bsky.social users that points to https://bsky.social, while for self-hosted
// PDSes it typically points back at the PDS itself.
func FetchAuthServerMetadata(pdsURL string) (*AuthServerMetadata, error) {
	prResp, err := http.Get(pdsURL + "/.well-known/oauth-protected-resource")
	if err != nil {
		return nil, err
	}
	defer prResp.Body.Close()
	if prResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth-protected-resource returned %d", prResp.StatusCode)
	}
	var pr struct {
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.NewDecoder(prResp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	if len(pr.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization_servers advertised by PDS %s", pdsURL)
	}
	asURL := strings.TrimRight(pr.AuthorizationServers[0], "/")

	resp, err := http.Get(asURL + "/.well-known/oauth-authorization-server")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth-authorization-server returned %d", resp.StatusCode)
	}
	var meta AuthServerMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// GeneratePKCE creates a code verifier and S256 challenge.
func GeneratePKCE() (verifier, challenge string) {
	b := make([]byte, 32)
	rand.Read(b)
	verifier = base64URLEncode(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64URLEncode(h[:])
	return
}

// GenerateDPoPKey creates an ephemeral P-256 ECDSA keypair.
func GenerateDPoPKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// DPoPKeyToState serializes a DPoP key for Redis storage.
func DPoPKeyToState(key *ecdsa.PrivateKey) DPoPState {
	return DPoPState{
		D: base64URLEncode(key.D.Bytes()),
		X: base64URLEncode(key.PublicKey.X.Bytes()),
		Y: base64URLEncode(key.PublicKey.Y.Bytes()),
	}
}

// DPoPKeyFromState deserializes a DPoP key from Redis.
func DPoPKeyFromState(state DPoPState) (*ecdsa.PrivateKey, error) {
	d, err := base64URLDecode(state.D)
	if err != nil {
		return nil, err
	}
	x, err := base64URLDecode(state.X)
	if err != nil {
		return nil, err
	}
	y, err := base64URLDecode(state.Y)
	if err != nil {
		return nil, err
	}
	key := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(x),
			Y:     new(big.Int).SetBytes(y),
		},
		D: new(big.Int).SetBytes(d),
	}
	return key, nil
}

// JWK returns the public key as a JWK map.
func JWK(key *ecdsa.PrivateKey) map[string]string {
	return map[string]string{
		"kty": "EC",
		"crv": "P-256",
		"x":   base64URLEncode(key.PublicKey.X.Bytes()),
		"y":   base64URLEncode(key.PublicKey.Y.Bytes()),
	}
}

// JWKThumbprint computes the RFC 7638 thumbprint for a DPoP key.
func JWKThumbprint(key *ecdsa.PrivateKey) string {
	jwk := JWK(key)
	// Canonical JSON with sorted keys per RFC 7638
	j := fmt.Sprintf(`{"crv":"%s","kty":"%s","x":"%s","y":"%s"}`, jwk["crv"], jwk["kty"], jwk["x"], jwk["y"])
	h := sha256.Sum256([]byte(j))
	return base64URLEncode(h[:])
}

// CreateDPoPJWT creates a DPoP proof JWT.
func CreateDPoPJWT(key *ecdsa.PrivateKey, method, targetURL string, nonce string) (string, error) {
	jwk := JWK(key)
	header := fmt.Sprintf(`{"alg":"ES256","typ":"dpop+jwt","jwk":{"kty":"%s","crv":"%s","x":"%s","y":"%s"}}`,
		jwk["kty"], jwk["crv"], jwk["x"], jwk["y"])

	payload := fmt.Sprintf(`{"jti":"%s","htm":"%s","htu":"%s","iat":%d`,
		uuid.New().String(), method, targetURL, time.Now().Unix())
	if nonce != "" {
		payload += fmt.Sprintf(`,"nonce":"%s"`, nonce)
	}
	payload += "}"

	signingInput := base64URLEncode([]byte(header)) + "." + base64URLEncode([]byte(payload))
	hash := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, hash[:])
	if err != nil {
		return "", err
	}

	// Encode r and s as fixed-size 32-byte big-endian
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	sig := make([]byte, 64)
	copy(sig[32-len(rBytes):32], rBytes)
	copy(sig[64-len(sBytes):64], sBytes)

	return signingInput + "." + base64URLEncode(sig), nil
}

// PushAuthorizationRequest sends a PAR request and returns the request_uri and state.
func PushAuthorizationRequest(authServer *AuthServerMetadata, clientMetadataURL, redirectURI, codeChallenge, loginHint string, dpopKey *ecdsa.PrivateKey) (requestURI string, state string, err error) {
	if authServer.PAREndpoint == "" {
		return "", "", fmt.Errorf("authorization server doesn't support PAR")
	}

	dpopJWT, err := CreateDPoPJWT(dpopKey, "POST", authServer.PAREndpoint, "")
	if err != nil {
		return "", "", err
	}

	thumbprint := JWKThumbprint(dpopKey)
	state = uuid.New().String()

	body := url.Values{
		"client_id":             {clientMetadataURL},
		"response_type":        {"code"},
		"redirect_uri":         {redirectURI},
		"scope":                {"atproto"},
		"state":                {state},
		"code_challenge":       {codeChallenge},
		"code_challenge_method": {"S256"},
		"login_hint":           {loginHint},
		"dpop_jkt":             {thumbprint},
	}

	req, _ := http.NewRequest("POST", authServer.PAREndpoint, strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("DPoP", dpopJWT)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	// Handle DPoP nonce retry
	if (resp.StatusCode == 400 || resp.StatusCode == 401) && resp.Header.Get("DPoP-Nonce") != "" {
		nonce := resp.Header.Get("DPoP-Nonce")
		dpopJWT2, err := CreateDPoPJWT(dpopKey, "POST", authServer.PAREndpoint, nonce)
		if err != nil {
			return "", "", err
		}
		req2, _ := http.NewRequest("POST", authServer.PAREndpoint, strings.NewReader(body.Encode()))
		req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req2.Header.Set("DPoP", dpopJWT2)
		resp2, err := http.DefaultClient.Do(req2)
		if err != nil {
			return "", "", err
		}
		defer resp2.Body.Close()
		respBody2, _ := io.ReadAll(resp2.Body)
		log.Printf("oauth: PAR nonce retry response (%d): %s", resp2.StatusCode, string(respBody2))
		var par PARResponse
		if err := json.Unmarshal(respBody2, &par); err != nil {
			return "", "", fmt.Errorf("PAR retry parse error: %v (body: %s)", err, string(respBody2))
		}
		if par.RequestURI == "" {
			return "", "", fmt.Errorf("PAR retry returned empty request_uri (body: %s)", string(respBody2))
		}
		return par.RequestURI, state, nil
	}

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("PAR failed (%d): %s", resp.StatusCode, string(respBody))
	}

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("oauth: PAR response (%d): %s", resp.StatusCode, string(respBody))
	var par PARResponse
	if err := json.Unmarshal(respBody, &par); err != nil {
		return "", "", fmt.Errorf("PAR response parse error: %v (body: %s)", err, string(respBody))
	}
	if par.RequestURI == "" {
		return "", "", fmt.Errorf("PAR returned empty request_uri (body: %s)", string(respBody))
	}
	return par.RequestURI, state, nil
}

// BuildAuthorizationURL constructs the URL to redirect the user to.
func BuildAuthorizationURL(authServer *AuthServerMetadata, clientMetadataURL, requestURI string) string {
	return fmt.Sprintf("%s?client_id=%s&request_uri=%s",
		authServer.AuthorizationEndpoint,
		url.QueryEscape(clientMetadataURL),
		url.QueryEscape(requestURI),
	)
}

// ExchangeCodeForToken exchanges an authorization code for tokens.
func ExchangeCodeForToken(authServer *AuthServerMetadata, code, codeVerifier, clientMetadataURL, redirectURI string, dpopKey *ecdsa.PrivateKey) (*TokenResponse, error) {
	dpopJWT, err := CreateDPoPJWT(dpopKey, "POST", authServer.TokenEndpoint, "")
	if err != nil {
		return nil, err
	}

	body := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientMetadataURL},
		"code_verifier": {codeVerifier},
	}

	req, _ := http.NewRequest("POST", authServer.TokenEndpoint, strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("DPoP", dpopJWT)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Handle DPoP nonce retry
	if resp.StatusCode == 400 && resp.Header.Get("DPoP-Nonce") != "" {
		nonce := resp.Header.Get("DPoP-Nonce")
		dpopJWT2, err := CreateDPoPJWT(dpopKey, "POST", authServer.TokenEndpoint, nonce)
		if err != nil {
			return nil, err
		}
		req2, _ := http.NewRequest("POST", authServer.TokenEndpoint, strings.NewReader(body.Encode()))
		req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req2.Header.Set("DPoP", dpopJWT2)
		resp2, err := http.DefaultClient.Do(req2)
		if err != nil {
			return nil, err
		}
		defer resp2.Body.Close()
		var token TokenResponse
		if err := json.NewDecoder(resp2.Body).Decode(&token); err != nil {
			return nil, err
		}
		return &token, nil
	}

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var token TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, err
	}
	return &token, nil
}

func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func base64URLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
