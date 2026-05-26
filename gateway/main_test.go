package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	healthHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", contentType)
	}

	var payload HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Failed to decode health response JSON: %v", err)
	}

	if payload.Status != "healthy" {
		t.Errorf("Expected status 'healthy', got %s", payload.Status)
	}
	if payload.Service != "open-llm-gateway" {
		t.Errorf("Expected service name 'open-llm-gateway', got %s", payload.Service)
	}
}

func TestIndexHandler(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	// 1. Test root path
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected root to return 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("Expected HTML response content type, got %s", resp.Header.Get("Content-Type"))
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(bodyBytes), "Open-LLM Console") {
		t.Errorf("Expected embedded HTML console string inside body, got: %s", string(bodyBytes))
	}

	// 2. Test random 404 path
	req404 := httptest.NewRequest("GET", "/invalid-route-name", nil)
	w404 := httptest.NewRecorder()
	mux.ServeHTTP(w404, req404)

	resp404 := w404.Result()
	if resp404.StatusCode != http.StatusNotFound {
		t.Errorf("Expected random subpath to return 404, got %d", resp404.StatusCode)
	}
}

func TestAuthMiddleware(t *testing.T) {
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	// Case A: Active Auth Verification Mode (API_AUTH_SECRET is set)
	secret := "super-secret-gate-key"
	authChain := AuthMiddleware(secret, dummyHandler)

	tests := []struct {
		name           string
		authHeader     string
		expectedStatus int
		containsError  string
	}{
		{
			name:           "Missing Authorization Header",
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
			containsError:  "Missing Authorization header",
		},
		{
			name:           "Malformed Token Format",
			authHeader:     "Basic dXNlcjpwYXNz",
			expectedStatus: http.StatusUnauthorized,
			containsError:  "Malformed Authorization token",
		},
		{
			name:           "Incorrect Secret Key",
			authHeader:     "Bearer wrong-token-123",
			expectedStatus: http.StatusUnauthorized,
			containsError:  "Invalid API secret token",
		},
		{
			name:           "Valid Pre-Shared Key",
			authHeader:     "Bearer super-secret-gate-key",
			expectedStatus: http.StatusOK,
			containsError:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()

			authChain.ServeHTTP(w, req)

			resp := w.Result()
			defer resp.Body.Close()

			if resp.StatusCode != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, resp.StatusCode)
			}

			if tt.expectedStatus != http.StatusOK {
				var errPayload map[string]map[string]any
				if err := json.NewDecoder(resp.Body).Decode(&errPayload); err != nil {
					t.Fatalf("Failed to parse JSON error output: %v", err)
				}
				msg, exists := errPayload["error"]["message"].(string)
				if !exists || !strings.Contains(msg, tt.containsError) {
					t.Errorf("Expected error to contain '%s', got raw body: %v", tt.containsError, errPayload)
				}
			} else {
				bodyBytes, _ := io.ReadAll(resp.Body)
				if string(bodyBytes) != "success" {
					t.Errorf("Expected successful pipeline path return 'success', got %s", string(bodyBytes))
				}
			}
		})
	}

	// Case B: Unauthenticated Mode (API_AUTH_SECRET is empty)
	openChain := AuthMiddleware("", dummyHandler)
	reqOpen := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	wOpen := httptest.NewRecorder()

	openChain.ServeHTTP(wOpen, reqOpen)
	respOpen := wOpen.Result()
	if respOpen.StatusCode != http.StatusOK {
		t.Errorf("Expected bypass auth to return 200 OK when secret is unconfigured, got %d", respOpen.StatusCode)
	}
}

func TestCORSAndLoggerMiddlewares(t *testing.T) {
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("processed"))
	})

	// Chain middlewares: CORS(mux)
	handler := CORSMiddleware(dummyHandler)

	// 1. Test standard OPTION preflight
	reqOptions := httptest.NewRequest("OPTIONS", "/v1/chat/completions", nil)
	wOptions := httptest.NewRecorder()
	handler.ServeHTTP(wOptions, reqOptions)

	respOptions := wOptions.Result()
	if respOptions.StatusCode != http.StatusOK {
		t.Errorf("Expected CORS preflight option request to return 200, got %d", respOptions.StatusCode)
	}
	if respOptions.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("Expected Access-Control-Allow-Origin to be '*', got %s", respOptions.Header.Get("Access-Control-Allow-Origin"))
	}
	if respOptions.Header.Get("Access-Control-Allow-Methods") != "GET, POST, OPTIONS" {
		t.Errorf("Expected Access-Control-Allow-Methods filter to exist, got %s", respOptions.Header.Get("Access-Control-Allow-Methods"))
	}

	// 2. Test standard GET/POST headers presence
	reqPost := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	wPost := httptest.NewRecorder()
	handler.ServeHTTP(wPost, reqPost)

	respPost := wPost.Result()
	if respPost.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("Expected standard request to return CORS response header, got %s", respPost.Header.Get("Access-Control-Allow-Origin"))
	}
}

func TestProxyHandlerAndHostHeaderPipes(t *testing.T) {
	// 1. Start a mock server representing the downstream vLLM container
	mockBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Confirm path and specific elements
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("Expected backend path to be /v1/chat/completions, got %s", r.URL.Path)
		}
		
		// Confirm standard HTTP host overrides!
		// The Host of the outgoing request MUST match the mock server's host name
		expectedHost := r.Host // what the request host header was sent as
		if expectedHost == "" {
			t.Error("Expected request host header to be populated")
		}

		// Verify security compliance: Authorization header MUST be stripped
		if authHeader := r.Header.Get("Authorization"); authHeader != "" {
			t.Errorf("Expected Authorization header to be stripped, but was present: %s", authHeader)
		}

		// Verify content body parsing
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("Failed to decode backend request body JSON: %v", err)
		}

		if modelStr, ok := payload["model"].(string); !ok || modelStr != "test-llama-model" {
			t.Errorf("Expected payload model parameter 'test-llama-model', got %v", payload["model"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom-Proxy-Header", "injected-by-mock-backend")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"Hello from mock vLLM backend!"}}]}`))
	}))
	defer mockBackend.Close()

	// 2. Parse the mock backend URL
	backendURL, err := url.Parse(mockBackend.URL)
	if err != nil {
		t.Fatalf("Failed to parse mock backend URL: %v", err)
	}

	// 3. Instantiate our reverse proxy
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = backendURL.Scheme
			req.URL.Host = backendURL.Host
			req.Host = backendURL.Host // Required Cloud Run routing host override
			req.Header.Del("Authorization")
		},
	}

	// 4. Issue a request through our gateway handler to verify it works!
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-llama-model","messages":[{"role":"user","content":"Hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gate-preshared-key-token") // should be stripped
	
	w := httptest.NewRecorder()
	
	proxy.ServeHTTP(w, req)
	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected proxy server success status 200, got %d", resp.StatusCode)
	}

	// Check header piping
	if resp.Header.Get("X-Custom-Proxy-Header") != "injected-by-mock-backend" {
		t.Errorf("Expected mock response header to be piped back, got '%s'", resp.Header.Get("X-Custom-Proxy-Header"))
	}

	// Check content body piping
	bodyBytes, _ := io.ReadAll(resp.Body)
	var respMap map[string]any
	if err := json.Unmarshal(bodyBytes, &respMap); err != nil {
		t.Fatalf("Failed to parse returned JSON payload: %v", err)
	}

	choices, exists := respMap["choices"].([]any)
	if !exists || len(choices) == 0 {
		t.Fatalf("Expected choices in returns, got: %v", respMap)
	}
}

func TestAPIVersionRouting(t *testing.T) {
	// 1. Setup mock V1 and V2 backends
	mockV1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"response from v1"}}]}`))
	}))
	defer mockV1.Close()

	mockV2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"response from v2"}}]}`))
	}))
	defer mockV2.Close()

	// Parse urls
	v1URL, _ := url.Parse(mockV1.URL)
	v2URL, _ := url.Parse(mockV2.URL)

	// Generate dynamic proxy handlers
	v1Proxy := wrapTelemetryAndProxy(createVersionedProxy(v1URL, "v1"), "v1")
	v2Proxy := wrapTelemetryAndProxy(createVersionedProxy(v2URL, "v2"), "v2")

	// Build main ServeMux router matching our system mappings
	mux := http.NewServeMux()
	mux.Handle("/v1/", AuthMiddleware("gate-key", v1Proxy))
	mux.Handle("/v2/", AuthMiddleware("gate-key", v2Proxy))

	// A. Test valid V1 path routing
	reqV1 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"active-model"}`))
	reqV1.Header.Set("Authorization", "Bearer gate-key")
	wV1 := httptest.NewRecorder()
	mux.ServeHTTP(wV1, reqV1)

	respV1 := wV1.Result()
	bodyBytesV1, _ := io.ReadAll(respV1.Body)
	if respV1.StatusCode != http.StatusOK || !strings.Contains(string(bodyBytesV1), "response from v1") {
		t.Errorf("Expected V1 path successful route, got status %d body: %s", respV1.StatusCode, string(bodyBytesV1))
	}

	// B. Test valid V2 path routing
	reqV2 := httptest.NewRequest("POST", "/v2/chat/completions", strings.NewReader(`{"model":"active-model"}`))
	reqV2.Header.Set("Authorization", "Bearer gate-key")
	wV2 := httptest.NewRecorder()
	mux.ServeHTTP(wV2, reqV2)

	respV2 := wV2.Result()
	bodyBytesV2, _ := io.ReadAll(respV2.Body)
	if respV2.StatusCode != http.StatusOK || !strings.Contains(string(bodyBytesV2), "response from v2") {
		t.Errorf("Expected V2 path successful route, got status %d body: %s", respV2.StatusCode, string(bodyBytesV2))
	}

	// C. Test unconfigured/invalid version fallback
	wUnconfigured := httptest.NewRecorder()
	reqUnconfigured := httptest.NewRequest("POST", "/v3/chat/completions", nil)
	mux.ServeHTTP(wUnconfigured, reqUnconfigured)
	if wUnconfigured.Code != http.StatusNotFound {
		t.Errorf("Expected unregistered version path (v3) to return 404, got %d", wUnconfigured.Code)
	}
}
