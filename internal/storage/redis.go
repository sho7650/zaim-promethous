package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type RequestTokenStore interface {
	Set(ctx context.Context, token, secret string) error
	Get(ctx context.Context, token string) (string, error)
	Delete(ctx context.Context, token string) error
	Close() error
}

type RedisRequestTokenStore struct {
	client *redis.Client
	ttl    time.Duration
	logger *zap.Logger
}

func NewRedisRequestTokenStore(redisURL string, ttl time.Duration, logger *zap.Logger) (*RedisRequestTokenStore, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis URL: %w", err)
	}

	client := redis.NewClient(opt)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	logger.Info("connected to redis", zap.String("addr", opt.Addr))

	return &RedisRequestTokenStore{
		client: client,
		ttl:    ttl,
		logger: logger,
	}, nil
}

func (s *RedisRequestTokenStore) Set(ctx context.Context, token, secret string) error {
	key := fmt.Sprintf("zaim:request_token:%s", token)

	s.logger.Debug("storing request token in redis", zap.String("token", token))

	err := s.client.Set(ctx, key, secret, s.ttl).Err()
	if err != nil {
		s.logger.Error("failed to store request token", zap.Error(err))
		return err
	}

	return nil
}

func (s *RedisRequestTokenStore) Get(ctx context.Context, token string) (string, error) {
	key := fmt.Sprintf("zaim:request_token:%s", token)

	secret, err := s.client.Get(ctx, key).Result()
	if err == redis.Nil {
		s.logger.Debug("request token not found", zap.String("token", token))
		return "", fmt.Errorf("token not found")
	}
	if err != nil {
		s.logger.Error("failed to get request token", zap.Error(err))
		return "", err
	}

	s.logger.Debug("retrieved request token from redis", zap.String("token", token))
	return secret, nil
}

func (s *RedisRequestTokenStore) Delete(ctx context.Context, token string) error {
	key := fmt.Sprintf("zaim:request_token:%s", token)

	err := s.client.Del(ctx, key).Err()
	if err != nil {
		s.logger.Error("failed to delete request token", zap.Error(err))
		return err
	}

	s.logger.Debug("deleted request token from redis", zap.String("token", token))
	return nil
}

func (s *RedisRequestTokenStore) Close() error {
	return s.client.Close()
}

// Memory implementation for development/testing
type MemoryRequestTokenStore struct {
	tokens map[string]tokenData
	logger *zap.Logger
}

type tokenData struct {
	secret    string
	expiresAt time.Time
}

func NewMemoryRequestTokenStore(logger *zap.Logger) *MemoryRequestTokenStore {
	return &MemoryRequestTokenStore{
		tokens: make(map[string]tokenData),
		logger: logger,
	}
}

func (s *MemoryRequestTokenStore) Set(ctx context.Context, token, secret string) error {
	s.tokens[token] = tokenData{
		secret:    secret,
		expiresAt: time.Now().Add(10 * time.Minute),
	}
	s.logger.Debug("stored request token in memory", zap.String("token", token))
	return nil
}

func (s *MemoryRequestTokenStore) Get(ctx context.Context, token string) (string, error) {
	data, exists := s.tokens[token]
	if !exists {
		return "", fmt.Errorf("token not found")
	}

	if time.Now().After(data.expiresAt) {
		delete(s.tokens, token)
		return "", fmt.Errorf("token expired")
	}

	s.logger.Debug("retrieved request token from memory", zap.String("token", token))
	return data.secret, nil
}

func (s *MemoryRequestTokenStore) Delete(ctx context.Context, token string) error {
	delete(s.tokens, token)
	s.logger.Debug("deleted request token from memory", zap.String("token", token))
	return nil
}

func (s *MemoryRequestTokenStore) Close() error {
	return nil
}

// Session store for access tokens
type SessionStore struct {
	client *redis.Client
	ttl    time.Duration
	logger *zap.Logger
}

type SessionData struct {
	AccessToken  string    `json:"access_token"`
	AccessSecret string    `json:"access_secret"`
	CreatedAt    time.Time `json:"created_at"`
}

func NewSessionStore(redisURL string, ttl time.Duration, logger *zap.Logger) (*SessionStore, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis URL: %w", err)
	}

	client := redis.NewClient(opt)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &SessionStore{
		client: client,
		ttl:    ttl,
		logger: logger,
	}, nil
}

func (s *SessionStore) CreateSession(ctx context.Context, sessionID string, data *SessionData) error {
	key := fmt.Sprintf("zaim:session:%s", sessionID)

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	err = s.client.Set(ctx, key, jsonData, s.ttl).Err()
	if err != nil {
		s.logger.Error("failed to create session", zap.Error(err))
		return err
	}

	s.logger.Info("created session", zap.String("session_id", sessionID))
	return nil
}

func (s *SessionStore) GetSession(ctx context.Context, sessionID string) (*SessionData, error) {
	key := fmt.Sprintf("zaim:session:%s", sessionID)

	jsonData, err := s.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("session not found")
	}
	if err != nil {
		s.logger.Error("failed to get session", zap.Error(err))
		return nil, err
	}

	var data SessionData
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return nil, err
	}

	// Refresh TTL on access
	s.client.Expire(ctx, key, s.ttl)

	return &data, nil
}

func (s *SessionStore) DeleteSession(ctx context.Context, sessionID string) error {
	key := fmt.Sprintf("zaim:session:%s", sessionID)

	err := s.client.Del(ctx, key).Err()
	if err != nil {
		s.logger.Error("failed to delete session", zap.Error(err))
		return err
	}

	s.logger.Info("deleted session", zap.String("session_id", sessionID))
	return nil
}

func (s *SessionStore) Close() error {
	return s.client.Close()
}