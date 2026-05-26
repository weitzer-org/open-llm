package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

//go:embed templates/index.html
var indexHTML []byte

type chatRequestMetadata struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

type HealthResponse struct {
	Status    string    `json:"status"`
	Service   string    `json:"service"`
	Timestamp time.Time `json:"timestamp"`
}

// CORSMiddleware sets standard modern headers for simple integrations and test tools
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequestLogger logs request activities and captures metrics like durations for Cloud Logging (N3)
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		// Create a custom status interceptor to record HTTP status code
		subWriter := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
		
		next.ServeHTTP(subWriter, r)
		
		log.Printf("[OPEN-LLM-GATEWAY] %s %s %d took %s (IP: %s)",
			r.Method, r.URL.Path, subWriter.status, time.Since(start), getClientIP(r))
	})
}

type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func getClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.Split(ip, ",")[0]
	}
	return r.RemoteAddr
}

// AuthMiddleware inspects the pre-shared Bearer key configuration
func AuthMiddleware(authSecret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If gateway is run in unauthenticated mode (local developer loops) bypass this step
		if authSecret == "" {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeAuthError(w, "Missing Authorization header. Please specify the Bearer token.")
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeAuthError(w, "Malformed Authorization token. Expected format: 'Bearer <token>'.")
			return
		}

		token := parts[1]
		if token != authSecret {
			writeAuthError(w, "Invalid API secret token provided.")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
			"param":   nil,
			"code":    "invalid_api_key",
		},
	})
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(HealthResponse{
		Status:    "healthy",
		Service:   "open-llm-gateway",
		Timestamp: time.Now().UTC(),
	})
}

func main() {
	// 1. Gather configuration elements
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	authSecret := os.Getenv("API_AUTH_SECRET")
	if authSecret == "" {
		log.Println("[GATEWAY-WARN] API_AUTH_SECRET environment variable is empty! Running in OPEN (UNAUTHENTICATED) MODE.")
	} else {
		log.Println("[GATEWAY-INFO] API_AUTH_SECRET is configured. Bearer token checks are ACTIVE.")
	}

	vllmURLStr := os.Getenv("VLLM_API_URL")
	if vllmURLStr == "" {
		vllmURLStr = "http://localhost:8000"
		log.Printf("[GATEWAY-INFO] VLLM_API_URL is empty, defaulting to local loop: %s", vllmURLStr)
	} else {
		log.Printf("[GATEWAY-INFO] Routing targets vLLM backend address: %s", vllmURLStr)
	}

	vllmURL, err := url.Parse(vllmURLStr)
	if err != nil {
		log.Fatalf("[GATEWAY-FATAL] Invalid vLLM URL structure: %v", err)
	}

	// 2. Initialize Reverse Proxy Engine
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = vllmURL.Scheme
			req.URL.Host = vllmURL.Host
			req.Host = vllmURL.Host // Crucial override for Cloud Run routing!
			
			// Clean outgoing credentials for security hygiene
			req.Header.Del("Authorization")
		},
		FlushInterval: -1, // Flush chunks immediately to provide real-time streaming experiences (SSE)
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("[GATEWAY-PROXY-ERROR] Connection error to inference node: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": fmt.Sprintf("Failed to communicate with inference model node: %v. The service might be starting up from a cold status or limits are exceeded.", err),
					"type":    "api_error",
					"code":    "backend_connection_failed",
				},
			})
		},
	}

	// 3. Define routers and endpoints
	mux := http.NewServeMux()

	// Serves embedded console Web UI
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	// Health probes
	mux.HandleFunc("/health", healthHandler)

	// API Proxy Endpoints
	proxyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Telemetry intercept: Read body to parse metadata
		var bodyBytes []byte
		if r.Body != nil {
			var err error
			bodyBytes, err = io.ReadAll(r.Body)
			if err == nil {
				// Replace request body so proxy can read it
				r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			}
		}

		var reqMeta chatRequestMetadata
		if len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, &reqMeta); err != nil {
				// Non-fatal parse issue, let the downstream validator throw direct standard JSON details
				log.Printf("[GATEWAY-DEBUG] Failed to parse request payload metadata: %v", err)
			}
		}

		log.Printf("[GATEWAY-PROXY-START] Proxying request to vLLM | Path: %s | Model: %s | Stream: %t", 
			r.URL.Path, reqMeta.Model, reqMeta.Stream)

		proxy.ServeHTTP(w, r)
	})

	// Apply Auth and routing boundaries
	authProxyChain := AuthMiddleware(authSecret, proxyHandler)
	mux.Handle("/v1/chat/completions", authProxyChain)
	mux.Handle("/v1/models", authProxyChain)

	// 4. Configure Server boundaries
	// We do NOT set tight Read/Write timeouts because HTTP SSE streams and cold boots can hold connections open!
	// IdleTimeout is kept safe to recycle connection pools.
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      RequestLogger(CORSMiddleware(mux)),
		ReadTimeout:  0, // No limit to prevent cutting off slow clients or streaming uploads
		WriteTimeout: 0, // No limit to support unlimited token stream generation & cold boot loading
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("[GATEWAY-START] Open-LLM Gateway console initialized on port :%s", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[GATEWAY-FATAL] Server closed with runtime error: %v", err)
	}
}
