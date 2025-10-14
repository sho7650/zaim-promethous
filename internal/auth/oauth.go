package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/dghubble/oauth1"
	"go.uber.org/zap"
)

var (
	ErrTokenNotFound = errors.New("oauth token not found")
	ErrInvalidToken  = errors.New("invalid oauth token")
)

type OAuthTokens struct {
	Token       string `json:"token"`
	TokenSecret string `json:"token_secret"`
}

type TokenStorage interface {
	Load() (*OAuthTokens, error)
	Save(*OAuthTokens) error
	Clear() error
}

type FileTokenStorage struct {
	filepath      string
	encryptionKey []byte
	mu            sync.RWMutex
}

func NewFileTokenStorage(filepath, encryptionKey string) (*FileTokenStorage, error) {
	var key []byte
	if encryptionKey != "" {
		decoded, err := base64.StdEncoding.DecodeString(encryptionKey)
		if err != nil {
			// Try using raw key
			key = []byte(encryptionKey)
			if len(key) != 32 {
				return nil, fmt.Errorf("encryption key must be 32 bytes")
			}
		} else {
			key = decoded
		}
	}

	return &FileTokenStorage{
		filepath:      filepath,
		encryptionKey: key,
	}, nil
}

func (s *FileTokenStorage) Load() (*OAuthTokens, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.filepath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrTokenNotFound
		}
		return nil, err
	}

	if s.encryptionKey != nil {
		data, err = decrypt(data, s.encryptionKey)
		if err != nil {
			return nil, err
		}
	}

	var tokens OAuthTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, err
	}

	return &tokens, nil
}

func (s *FileTokenStorage) Save(tokens *OAuthTokens) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(tokens)
	if err != nil {
		return err
	}

	if s.encryptionKey != nil {
		data, err = encrypt(data, s.encryptionKey)
		if err != nil {
			return err
		}
	}

	// Ensure directory exists
	dir := strings.TrimSuffix(s.filepath, "/"+strings.Split(s.filepath, "/")[len(strings.Split(s.filepath, "/"))-1])
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(s.filepath, data, 0600)
}

func (s *FileTokenStorage) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.filepath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func encrypt(plaintext []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decrypt(ciphertext []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, ErrInvalidToken
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

type Manager struct {
	config  *oauth1.Config
	storage TokenStorage
	logger  *zap.Logger
}

func NewManager(consumerKey, consumerSecret string, storage TokenStorage, logger *zap.Logger) *Manager {
	return &Manager{
		config: &oauth1.Config{
			ConsumerKey:    consumerKey,
			ConsumerSecret: consumerSecret,
			Endpoint: oauth1.Endpoint{
				RequestTokenURL: "https://api.zaim.net/v2/auth/request",
				AuthorizeURL:    "https://auth.zaim.net/users/auth",
				AccessTokenURL:  "https://api.zaim.net/v2/auth/access",
			},
		},
		storage: storage,
		logger:  logger,
	}
}

func (m *Manager) GetAuthorizationURL(callbackURL string) (string, string, string, error) {
	m.config.CallbackURL = callbackURL
	requestToken, requestSecret, err := m.config.RequestToken()
	if err != nil {
		m.logger.Error("failed to get request token", zap.Error(err))
		return "", "", "", err
	}

	authorizationURL, err := m.config.AuthorizationURL(requestToken)
	if err != nil {
		m.logger.Error("failed to get authorization URL", zap.Error(err))
		return "", "", "", err
	}

	m.logger.Info("generated authorization URL",
		zap.String("url", authorizationURL.String()),
		zap.String("request_token", requestToken))

	return authorizationURL.String(), requestToken, requestSecret, nil
}

func (m *Manager) HandleCallback(ctx context.Context, requestToken, requestSecret, verifier string) error {
	accessToken, accessSecret, err := m.config.AccessToken(requestToken, requestSecret, verifier)
	if err != nil {
		m.logger.Error("failed to get access token", zap.Error(err))
		return err
	}

	tokens := &OAuthTokens{
		Token:       accessToken,
		TokenSecret: accessSecret,
	}

	if err := m.storage.Save(tokens); err != nil {
		m.logger.Error("failed to save tokens", zap.Error(err))
		return err
	}

	m.logger.Info("successfully saved access tokens")
	return nil
}

func (m *Manager) GetClient(ctx context.Context) (*oauth1.Token, error) {
	tokens, err := m.storage.Load()
	if err != nil {
		return nil, err
	}

	return oauth1.NewToken(tokens.Token, tokens.TokenSecret), nil
}

func (m *Manager) IsAuthenticated() bool {
	_, err := m.storage.Load()
	return err == nil
}

func (m *Manager) ResetAuth() error {
	return m.storage.Clear()
}