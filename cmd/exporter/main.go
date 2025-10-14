package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"strconv"
	"syscall"
	"time"

	"github.com/dghubble/oauth1"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/yourusername/zaim-prometheus-exporter/internal/auth"
	"github.com/yourusername/zaim-prometheus-exporter/internal/metrics"
	"github.com/yourusername/zaim-prometheus-exporter/internal/server"
	"github.com/yourusername/zaim-prometheus-exporter/internal/storage"
	"github.com/yourusername/zaim-prometheus-exporter/internal/zaim"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	_ = godotenv.Load()

	// Parse command flags
	var (
		healthCheck = flag.Bool("health", false, "Run health check and exit")
		debugMode   = flag.Bool("debug", false, "Enable debug logging")
	)
	flag.Parse()

	// Initialize logger
	logger := initLogger(*debugMode)
	defer logger.Sync()

	// Health check mode
	if *healthCheck {
		runHealthCheck(logger)
		return
	}

	// Load configuration
	config := loadConfig()

	// Validate configuration
	if config.ConsumerKey == "" || config.ConsumerSecret == "" {
		logger.Fatal("ZAIM_CONSUMER_KEY and ZAIM_CONSUMER_SECRET must be set")
	}

	// Initialize token storage
	tokenStorage, err := auth.NewFileTokenStorage(config.TokenFile, config.EncryptionKey)
	if err != nil {
		logger.Fatal("failed to initialize token storage", zap.Error(err))
	}

	// Initialize OAuth manager
	oauthMgr := auth.NewManager(config.ConsumerKey, config.ConsumerSecret, tokenStorage, logger)

	// Initialize Zaim client if authenticated
	if oauthMgr.IsAuthenticated() {
		token, err := oauthMgr.GetClient(context.Background())
		if err == nil {
			oauthConfig := &oauth1.Config{
				ConsumerKey:    config.ConsumerKey,
				ConsumerSecret: config.ConsumerSecret,
			}
			zaimClient := zaim.NewClient(oauthConfig, token, logger)
			aggregator := metrics.NewAggregator()
			collector := metrics.NewZaimCollector(zaimClient, aggregator, logger)
			prometheus.MustRegister(collector)
			logger.Info("registered Zaim metrics collector")
		} else {
			logger.Warn("failed to initialize Zaim client", zap.Error(err))
		}
	} else {
		logger.Warn("not authenticated with Zaim API, metrics will not be available")
	}

	// Initialize request token store
	var requestTokenStore storage.RequestTokenStore
	if redisURL := config.RedisURL; redisURL != "" {
		store, err := storage.NewRedisRequestTokenStore(redisURL, 10*time.Minute, logger)
		if err != nil {
			logger.Fatal("failed to initialize redis store", zap.Error(err))
		}
		defer store.Close()
		requestTokenStore = store
		logger.Info("using redis for request token storage")
	} else {
		requestTokenStore = storage.NewMemoryRequestTokenStore(logger)
		logger.Warn("using in-memory request token storage (not suitable for multiple instances)")
	}

	// Initialize HTTP server
	srv := server.NewServer(oauthMgr, requestTokenStore, logger)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", config.Port),
		Handler:      srv.Router(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		logger.Info("starting server", zap.Int("port", config.Port))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server failed", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Fatal("server forced to shutdown", zap.Error(err))
	}

	logger.Info("server exited")
}

type Config struct {
	ConsumerKey    string
	ConsumerSecret string
	CallbackURL    string
	TokenFile      string
	EncryptionKey  string

	// Redis configuration components
	RedisHost     string
	RedisPort     int
	RedisPassword string
	RedisDB       int
	RedisURL      string  // Constructed or explicitly provided

	Port          int
}

func loadConfig() *Config {
	cfg := &Config{
		ConsumerKey:    getEnv("ZAIM_CONSUMER_KEY", ""),
		ConsumerSecret: getEnv("ZAIM_CONSUMER_SECRET", ""),
		CallbackURL:    getEnv("ZAIM_CALLBACK_URL", "http://localhost:8080/zaim/auth/callback"),
		TokenFile:      getEnv("TOKEN_FILE", "/data/oauth_tokens.json"),
		EncryptionKey:  getSecretOrEnv("ENCRYPTION_KEY", ""),

		// Redis components (password auto-loaded from secrets)
		RedisHost:     getEnv("REDIS_HOST", "redis"),
		RedisPort:     getEnvInt("REDIS_PORT", 6379),
		RedisPassword: getSecretOrEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvInt("REDIS_DB", 0),

		Port:          getEnvInt("PORT", 8080),
	}

	// REDIS_URL priority:
	// 1. Explicit REDIS_URL environment variable (if provided)
	// 2. Constructed from components
	if redisURL := getEnv("REDIS_URL", ""); redisURL != "" {
		cfg.RedisURL = redisURL
	} else if cfg.RedisHost != "" && cfg.RedisPassword != "" {
		cfg.RedisURL = buildRedisURL(cfg.RedisHost, cfg.RedisPort, cfg.RedisPassword, cfg.RedisDB)
	}

	return cfg
}

// getSecretOrEnv: Docker Secrets (/run/secrets/) を優先、次に環境変数を確認
func getSecretOrEnv(key, fallback string) string {
	// Docker Secrets: /run/secrets/<key_lowercase>
	secretPath := fmt.Sprintf("/run/secrets/%s", strings.ToLower(key))
	if data, err := os.ReadFile(secretPath); err == nil {
		return strings.TrimSpace(string(data))
	}
	// Fallback to environment variable
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return fallback
}

// buildRedisURL constructs Redis connection string from components
func buildRedisURL(host string, port int, password string, db int) string {
	if password != "" {
		return fmt.Sprintf("redis://:%s@%s:%d/%d", password, host, port, db)
	}
	return fmt.Sprintf("redis://%s:%d/%d", host, port, db)
}

func initLogger(debug bool) *zap.Logger {
	config := zap.NewProductionConfig()
	if debug {
		config.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	}

	// Output to stdout
	config.OutputPaths = []string{"stdout"}
	config.ErrorOutputPaths = []string{"stderr"}

	// Use human-readable timestamps
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	logger, err := config.Build()
	if err != nil {
		panic(err)
	}

	return logger
}

func runHealthCheck(logger *zap.Logger) {
	resp, err := http.Get("http://localhost:8080/health")
	if err != nil {
		logger.Error("health check failed", zap.Error(err))
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Error("health check failed", zap.Int("status", resp.StatusCode))
		os.Exit(1)
	}

	logger.Info("health check passed")
	os.Exit(0)
}