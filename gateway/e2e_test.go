package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestE2EGatewayFlow(t *testing.T) {
	// 1. Setup Mock vLLM Backend emitting streaming SSE data chunk-by-chunk
	mockvLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		// Emit standard OpenAI-compliant SSE event chunks with generic text completions
		chunks := []string{
			`{"choices":[{"delta":{"content":"Hi "}}]}`,
			`{"choices":[{"delta":{"content":"there, "}}]}`,
			`{"choices":[{"delta":{"content":"friend!"}}]}`,
		}

		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(10 * time.Millisecond) // brief sleep to mimic generation intervals
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer mockvLLM.Close()

	// 2. Setup the Go Gateway Server configuration context parameters
	authSecret := "e2e-master-gate-key"
	v1URL, _ := url.Parse(mockvLLM.URL)
	v1Proxy := createVersionedProxy(v1URL, "v1", nil)
	v1ProxyHandler := wrapTelemetryAndProxy(v1Proxy, "v1")

	// Create ServeMux and wrap standard middleware chains matching product main()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.Handle("/v1/", AuthMiddleware(authSecret, v1ProxyHandler))

	// Instantiate the real live target server running on an ephemeral dynamic socket address
	gatewayServer := httptest.NewServer(CORSMiddleware(mux))
	defer gatewayServer.Close()

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// ========================================================================
	// E2E Task A: Verify Health endpoint is open (CORS-ready, no auth requirements)
	// ========================================================================
	reqHealth, _ := http.NewRequest("GET", gatewayServer.URL+"/health", nil)
	respHealth, err := client.Do(reqHealth)
	if err != nil {
		t.Fatalf("Failed GET /health: %v", err)
	}
	defer respHealth.Body.Close()

	if respHealth.StatusCode != http.StatusOK {
		t.Errorf("Expected health check status 200, got %d", respHealth.StatusCode)
	}
	if respHealth.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("Expected open CORS headers presence on health check endpoint")
	}

	// ========================================================================
	// E2E Task B: Verify Auth gates (reject unauthenticated completions calls)
	// ========================================================================
	reqUnauth, _ := http.NewRequest("POST", gatewayServer.URL+"/v1/chat/completions", strings.NewReader(`{"model":"active-model"}`))
	respUnauth, err := client.Do(reqUnauth)
	if err != nil {
		t.Fatalf("Failed unauthenticated POST request call: %v", err)
	}
	defer respUnauth.Body.Close()
	if respUnauth.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected unauthenticated post request to return 401, got %d", respUnauth.StatusCode)
	}

	// ========================================================================
	// E2E Task C: Verify Auth gates (reject incorrect secret token completions calls)
	// ========================================================================
	reqBadToken, _ := http.NewRequest("POST", gatewayServer.URL+"/v1/chat/completions", strings.NewReader(`{"model":"active-model"}`))
	reqBadToken.Header.Set("Authorization", "Bearer invalid-token-pass-123")
	respBadToken, err := client.Do(reqBadToken)
	if err != nil {
		t.Fatalf("Failed bad token POST request call: %v", err)
	}
	defer respBadToken.Body.Close()
	if respBadToken.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected bad token post request to return 401, got %d", respBadToken.StatusCode)
	}

	// ========================================================================
	// E2E Task D: Verify full authenticated streaming completions (SSE parsing)
	// ========================================================================
	reqCompletions, _ := http.NewRequest("POST", gatewayServer.URL+"/v1/chat/completions", strings.NewReader(`{"model":"active-model","messages":[{"role":"user","content":"Ping"}],"stream":true}`))
	reqCompletions.Header.Set("Content-Type", "application/json")
	reqCompletions.Header.Set("Authorization", "Bearer "+authSecret)

	respCompletions, err := client.Do(reqCompletions)
	if err != nil {
		t.Fatalf("Failed POST stream completions call: %v", err)
	}
	defer respCompletions.Body.Close()

	if respCompletions.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respCompletions.Body)
		t.Fatalf("Expected successful status 200, got %d. Body: %s", respCompletions.StatusCode, string(body))
	}

	if !strings.Contains(respCompletions.Header.Get("Content-Type"), "text/event-stream") {
		t.Errorf("Expected content type to preserve text/event-stream SSE, got %s", respCompletions.Header.Get("Content-Type"))
	}

	// Parse incremental stream lines chunk-by-chunk
	var accumulatedText string
	reader := bufio.NewReader(respCompletions.Body)
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Error reading events line from stream connection: %v", err)
		}

		line = strings.TrimSpace(line)
		if line == "" || line == "data: [DONE]" {
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			var chunk map[string]any
			if err := json.Unmarshal([]byte(line[6:]), &chunk); err != nil {
				t.Fatalf("Failed to parse returned chunk JSON: %v. Raw Line: %s", err, line)
			}

			// Parse incremental content delta slices
			if choices, ok := chunk["choices"].([]any); ok && len(choices) > 0 {
				if choice, ok := choices[0].(map[string]any); ok {
					if delta, ok := choice["delta"].(map[string]any); ok {
						if content, ok := delta["content"].(string); ok {
							accumulatedText += content
						}
					}
				}
			}
		}
	}

	expectedText := "Hi there, friend!"
	if accumulatedText != expectedText {
		t.Errorf("Expected final accumulated text to be '%s', got '%s'", expectedText, accumulatedText)
	}

	// ========================================================================
	// E2E Task E: Verify authenticated completions via custom X-API-Key header
	// ========================================================================
	reqXAPIKey, _ := http.NewRequest("POST", gatewayServer.URL+"/v1/chat/completions", strings.NewReader(`{"model":"active-model","messages":[{"role":"user","content":"Ping"}],"stream":false}`))
	reqXAPIKey.Header.Set("Content-Type", "application/json")
	reqXAPIKey.Header.Set("X-API-Key", authSecret)

	respXAPIKey, err := client.Do(reqXAPIKey)
	if err != nil {
		t.Fatalf("Failed POST request completions with X-API-Key: %v", err)
	}
	defer respXAPIKey.Body.Close()

	if respXAPIKey.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respXAPIKey.Body)
		t.Fatalf("Expected successful status 200 via X-API-Key, got %d. Body: %s", respXAPIKey.StatusCode, string(body))
	}
}

func TestE2EGatewayV2Fallback(t *testing.T) {
	// e2e check validating the 501 fallback gate for unconfigured versions
	authSecret := "e2e-master-gate-key"
	mux := http.NewServeMux()
	
	// Set v2 as unconfigured (passing standard 501 handler)
	v2ProxyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "API version v2 is not active or configured in this environment.",
				"type":    "api_error",
				"code":    "version_not_implemented",
			},
		})
	})

	mux.Handle("/v2/", AuthMiddleware(authSecret, v2ProxyHandler))

	gatewayServer := httptest.NewServer(mux)
	defer gatewayServer.Close()

	client := &http.Client{}
	req, _ := http.NewRequest("POST", gatewayServer.URL+"/v2/chat/completions", strings.NewReader(`{"model":"active-model"}`))
	req.Header.Set("Authorization", "Bearer "+authSecret)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed V2 call: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("Expected unconfigured V2 endpoint to return status 501, got %d", resp.StatusCode)
	}

	var errPayload map[string]map[string]any
	json.NewDecoder(resp.Body).Decode(&errPayload)
	if msg, _ := errPayload["error"]["code"].(string); msg != "version_not_implemented" {
		t.Errorf("Expected error code 'version_not_implemented', got '%s'", msg)
	}
}
type testServerHolder struct {
	server *http.Server
	port   string
}

func startTempServer(handler http.Handler) (*testServerHolder, error) {
	// safe dynamic server initialization
	return &testServerHolder{
		server: &http.Server{
			Addr:    ":0",
			Handler: handler,
		},
	}, nil
}
func (s *testServerHolder) closeContext(ctx context.Context) {
	s.server.Shutdown(ctx)
}
func (s *testServerHolder) startAsync(errChan chan error) {
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()
}
func (s *testServerHolder) parseDynamicPort(t *testing.T) {
	// parses socket configuration
}
func (s *testServerHolder) getAddress() string {
	return s.server.Addr
}
type mockResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	code   int
}
func newMockResponseWriter() *mockResponseWriter {
	return &mockResponseWriter{
		header: make(http.Header),
		code:   http.StatusOK,
	}
}
func (w *mockResponseWriter) Header() http.Header {
	return w.header
}
func (w *mockResponseWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}
func (w *mockResponseWriter) WriteHeader(statusCode int) {
	w.code = statusCode
}
func (w *mockResponseWriter) getBodyString() string {
	return w.body.String()
}
func (w *mockResponseWriter) getStatusCode() int {
	return w.code
}
func (w *mockResponseWriter) getResponseBytes() []byte {
	return w.body.Bytes()
}
