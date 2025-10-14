package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/yourusername/zaim-prometheus-exporter/internal/auth"
	"github.com/yourusername/zaim-prometheus-exporter/internal/storage"
	"go.uber.org/zap"
)

type Server struct {
	authManager       *auth.Manager
	requestTokenStore storage.RequestTokenStore
	logger            *zap.Logger
	router            *mux.Router
}

func NewServer(authManager *auth.Manager, requestTokenStore storage.RequestTokenStore, logger *zap.Logger) *Server {
	s := &Server{
		authManager:       authManager,
		requestTokenStore: requestTokenStore,
		logger:            logger,
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	r := mux.NewRouter()

	// Prometheus metrics endpoint
	r.Handle("/metrics", promhttp.Handler()).Methods("GET")

	// OAuth endpoints
	r.HandleFunc("/zaim/auth/status", s.handleAuthStatus).Methods("GET")
	r.HandleFunc("/zaim/auth/start", s.handleAuthStart).Methods("GET")
	r.HandleFunc("/zaim/auth/callback", s.handleAuthCallback).Methods("GET")
	r.HandleFunc("/zaim/auth/reset", s.handleAuthReset).Methods("POST")

	// Health check
	r.HandleFunc("/health", s.handleHealth).Methods("GET")

	// Readiness check
	r.HandleFunc("/ready", s.handleReady).Methods("GET")

	// Root endpoint
	r.HandleFunc("/", s.handleRoot).Methods("GET")

	s.router = r
}

func (s *Server) Router() http.Handler {
	return s.router
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
	})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if !s.authManager.IsAuthenticated() {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "not ready",
			"reason": "not authenticated",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ready",
	})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.New("index").Parse(indexHTML))
	data := struct {
		IsAuthenticated bool
	}{
		IsAuthenticated: s.authManager.IsAuthenticated(),
	}
	tmpl.Execute(w, data)
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	isAuthenticated := s.authManager.IsAuthenticated()
	status := map[string]interface{}{
		"authenticated": isAuthenticated,
	}

	json.NewEncoder(w).Encode(status)
}

func (s *Server) handleAuthStart(w http.ResponseWriter, r *http.Request) {
	// Build callback URL from request
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}

	host := r.Host
	if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = forwardedHost
	}

	callbackURL := fmt.Sprintf("%s://%s/zaim/auth/callback", scheme, host)

	authURL, requestToken, requestSecret, err := s.authManager.GetAuthorizationURL(callbackURL)
	if err != nil {
		s.logger.Error("failed to get authorization URL", zap.Error(err))
		http.Error(w, "Failed to start OAuth flow", http.StatusInternalServerError)
		return
	}

	// Store request token and secret temporarily
	ctx := r.Context()
	if err := s.requestTokenStore.Set(ctx, requestToken, requestSecret); err != nil {
		s.logger.Error("failed to store request token", zap.Error(err))
		http.Error(w, "Failed to store request token", http.StatusInternalServerError)
		return
	}

	// Redirect to Zaim authorization page
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	oauthToken := r.URL.Query().Get("oauth_token")
	oauthVerifier := r.URL.Query().Get("oauth_verifier")

	if oauthToken == "" || oauthVerifier == "" {
		s.logger.Error("missing OAuth parameters",
			zap.String("token", oauthToken),
			zap.String("verifier", oauthVerifier))
		http.Error(w, "Missing OAuth parameters", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Retrieve request secret
	requestSecret, err := s.requestTokenStore.Get(ctx, oauthToken)
	if err != nil {
		s.logger.Error("failed to get request secret", zap.Error(err))
		http.Error(w, "Failed to retrieve request token", http.StatusInternalServerError)
		return
	}

	// Exchange for access token
	if err := s.authManager.HandleCallback(ctx, oauthToken, requestSecret, oauthVerifier); err != nil {
		s.logger.Error("failed to handle OAuth callback", zap.Error(err))
		http.Error(w, "Failed to complete OAuth flow", http.StatusInternalServerError)
		return
	}

	// Clean up request token
	_ = s.requestTokenStore.Delete(ctx, oauthToken)

	// Success page
	tmpl := template.Must(template.New("success").Parse(successHTML))
	tmpl.Execute(w, nil)
}

func (s *Server) handleAuthReset(w http.ResponseWriter, r *http.Request) {
	if err := s.authManager.ResetAuth(); err != nil {
		s.logger.Error("failed to reset auth", zap.Error(err))
		http.Error(w, "Failed to reset authentication", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "success",
		"message": "Authentication reset successfully",
	})
}

const indexHTML = `<!DOCTYPE html>
<html>
<head>
    <title>Zaim Prometheus Exporter</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; }
        .status { padding: 10px; margin: 20px 0; border-radius: 5px; }
        .authenticated { background-color: #d4edda; color: #155724; }
        .not-authenticated { background-color: #f8d7da; color: #721c24; }
        button { padding: 10px 20px; margin: 10px 0; font-size: 16px; cursor: pointer; }
    </style>
</head>
<body>
    <h1>Zaim Prometheus Exporter</h1>

    {{if .IsAuthenticated}}
        <div class="status authenticated">
            ✅ Authenticated with Zaim API
        </div>
        <p>Metrics are available at <a href="/metrics">/metrics</a></p>
        <button onclick="resetAuth()">Reset Authentication</button>
    {{else}}
        <div class="status not-authenticated">
            ❌ Not authenticated
        </div>
        <a href="/zaim/auth/start"><button>Authenticate with Zaim</button></a>
    {{end}}

    <h2>Available Endpoints</h2>
    <ul>
        <li><a href="/metrics">/metrics</a> - Prometheus metrics</li>
        <li><a href="/zaim/auth/status">/zaim/auth/status</a> - Authentication status</li>
        <li><a href="/health">/health</a> - Health check</li>
        <li><a href="/ready">/ready</a> - Readiness check</li>
    </ul>

    <script>
        function resetAuth() {
            if (confirm('Are you sure you want to reset authentication?')) {
                fetch('/zaim/auth/reset', { method: 'POST' })
                    .then(response => response.json())
                    .then(data => {
                        alert(data.message);
                        location.reload();
                    })
                    .catch(error => alert('Error: ' + error));
            }
        }
    </script>
</body>
</html>`

const successHTML = `<!DOCTYPE html>
<html>
<head>
    <title>Authentication Successful</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; }
        .success { padding: 20px; background-color: #d4edda; color: #155724; border-radius: 5px; }
    </style>
</head>
<body>
    <div class="success">
        <h1>✅ Authentication Successful!</h1>
        <p>You have successfully authenticated with Zaim API.</p>
        <p>Metrics are now available at <a href="/metrics">/metrics</a></p>
        <a href="/"><button>Back to Home</button></a>
    </div>
</body>
</html>`