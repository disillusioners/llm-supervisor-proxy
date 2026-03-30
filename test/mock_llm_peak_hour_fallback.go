//go:build ignore

// Peak Hour Fallback Test
// Tests that when a peak hour upstream model fails, the fallback proxy_model is invoked.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// MockLLMServer handles requests and returns different responses based on model name
type MockLLMServer struct {
	port          int
	failingModels map[string]bool // Map of model names that should return 500
}

func (m *MockLLMServer) Start() error {
	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		// Read the request body
		reqBodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()

		var reqBody map[string]interface{}
		if err := json.Unmarshal(reqBodyBytes, &reqBody); err != nil {
			http.Error(w, "Failed to parse request body as JSON", http.StatusBadRequest)
			return
		}

		// Extract model name
		model := "unknown"
		if modelVal, ok := reqBody["model"].(string); ok {
			model = modelVal
		}
		log.Printf("[MOCK] Request for model: %s", model)

		// Check if this is a failing model
		if m.failingModels[model] {
			log.Printf("[MOCK] Returning 500 for model: %s", model)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":{"message":"Internal Server Error","type":"internal_error"}}`))
			return
		}

		// Success response
		log.Printf("[MOCK] Returning 200 for model: %s", model)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		response := map[string]interface{}{
			"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]string{
						"role":    "assistant",
						"content": fmt.Sprintf("Response from model: %s", model),
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]int{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		json.NewEncoder(w).Encode(response)
	})

	addr := fmt.Sprintf(":%d", m.port)
	log.Printf("[MOCK] Starting mock server on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		return err
	}
	return nil
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("=== Peak Hour Fallback Test ===")

	// Configuration
	mockPort := 19001
	proxyPort := 19002
	timeout := 60 * time.Second

	// Cleanup function
	cleanup := func() {
		log.Println("[CLEANUP] Killing processes on ports...")
		exec.Command("bash", "-c", fmt.Sprintf("lsof -ti :%d 2>/dev/null | xargs kill -9 2>/dev/null || true", mockPort)).Run()
		exec.Command("bash", "-c", fmt.Sprintf("lsof -ti :%d 2>/dev/null | xargs kill -9 2>/dev/null || true", proxyPort)).Run()
	}

	// Kill any existing processes on these ports first
	log.Println("[SETUP] Killing existing processes on ports...")
	cleanup()
	time.Sleep(1 * time.Second)

	// Start mock server in background
	// Peak model fails, fallback's peak model also fails, but fallback's normal model succeeds
	mockServer := &MockLLMServer{
		port: mockPort,
		failingModels: map[string]bool{
			"mock-peak-upstream": true, // Primary peak model fails
			"mock-fallback-peak": true, // Fallback's peak model also fails
		},
	}
	go func() {
		if err := mockServer.Start(); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			log.Printf("[MOCK] Server error: %v", err)
		}
	}()
	time.Sleep(1 * time.Second)

	// Verify mock server is running
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/v1/chat/completions", mockPort))
	if err != nil {
		log.Fatalf("[SETUP] Mock server not responding: %v", err)
	}
	resp.Body.Close()
	log.Println("[SETUP] Mock server is running")

	// Start proxy with environment variables
	log.Println("[SETUP] Starting proxy...")
	proxyCmd := exec.Command("go", "run", "cmd/main.go")
	proxyCmd.Stdout = os.Stdout
	proxyCmd.Stderr = os.Stderr
	proxyCmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", proxyPort),
		"APPLY_ENV_OVERRIDES=true",
		"RACE_RETRY_ENABLED=true",
		"RACE_PARALLEL_ON_IDLE=true",
		"RACE_MAX_PARALLEL=3",
		"IDLE_TIMEOUT=5s",
		"STREAM_DEADLINE=30s",
		"MAX_GENERATION_TIME=60s",
		"LOOP_DETECTION_ENABLED=false",
		"DATABASE_URL=", // Use SQLite
	)
	if err := proxyCmd.Start(); err != nil {
		log.Fatalf("[SETUP] Failed to start proxy: %v", err)
	}
	time.Sleep(4 * time.Second)

	// Verify proxy is running
	resp, err = http.Get(fmt.Sprintf("http://localhost:%d/v1/models", proxyPort))
	if err != nil {
		proxyCmd.Process.Kill()
		log.Fatalf("[SETUP] Proxy not responding: %v", err)
	}
	resp.Body.Close()
	log.Println("[SETUP] Proxy is running")

	// Create API token
	log.Println("[SETUP] Creating API token...")
	tokenData := map[string]interface{}{
		"name":       "test-token",
		"created_by": "test",
	}
	tokenJSON, _ := json.Marshal(tokenData)
	resp, err = http.Post(
		fmt.Sprintf("http://localhost:%d/fe/api/tokens", proxyPort),
		"application/json",
		bytes.NewReader(tokenJSON),
	)
	if err != nil {
		proxyCmd.Process.Kill()
		log.Fatalf("[SETUP] Failed to create token: %v", err)
	}
	tokenBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("[SETUP] Token creation returned status: %d, body: %s", resp.StatusCode, string(tokenBody))
		proxyCmd.Process.Kill()
		log.Fatalf("[SETUP] Token creation failed")
	}
	var tokenResp map[string]interface{}
	json.Unmarshal(tokenBody, &tokenResp)
	testAPIKey := ""
	if token, ok := tokenResp["token"].(string); ok {
		testAPIKey = token
	}
	if testAPIKey == "" {
		proxyCmd.Process.Kill()
		log.Fatalf("[SETUP] Token creation did not return token")
	}
	log.Printf("[SETUP] API token created: %s...", testAPIKey[:20])

	// Create credential via API
	log.Println("[SETUP] Creating credential...")
	credData := map[string]interface{}{
		"id":       "test-peak-cred",
		"provider": "openai",
		"api_key":  "mock-key",
	}
	credJSON, _ := json.Marshal(credData)
	resp, err = http.Post(
		fmt.Sprintf("http://localhost:%d/fe/api/credentials", proxyPort),
		"application/json",
		bytes.NewReader(credJSON),
	)
	if err != nil {
		proxyCmd.Process.Kill()
		log.Fatalf("[SETUP] Failed to create credential: %v", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("[SETUP] Credential creation returned status: %d (may already exist, that's OK)", resp.StatusCode)
	}
	log.Println("[SETUP] Credential created/exists")

	// Use unique model IDs for this test run
	testID := fmt.Sprintf("test-%d", time.Now().UnixNano())
	testPeakModel := testID + "-peak"
	testFallbackModel := testID + "-fallback"

	// Create test-peak model with peak hour config and fallback chain
	log.Printf("[SETUP] Creating %s model...", testPeakModel)
	peakModelData := map[string]interface{}{
		"id":                 testPeakModel,
		"name":               "Test Peak Model",
		"enabled":            true,
		"internal":           true,
		"credential_id":      "test-peak-cred",
		"internal_base_url":  fmt.Sprintf("http://localhost:%d/v1", mockPort),
		"internal_model":     "mock-normal-upstream",
		"fallback_chain":     []string{testFallbackModel},
		"peak_hour_enabled":  true,
		"peak_hour_start":    "00:00", // Always active
		"peak_hour_end":      "23:59",
		"peak_hour_timezone": "+0",
		"peak_hour_model":    "mock-peak-upstream", // This will fail
	}
	peakModelJSON, _ := json.Marshal(peakModelData)

	resp, err = http.Post(
		fmt.Sprintf("http://localhost:%d/fe/api/models", proxyPort),
		"application/json",
		bytes.NewReader(peakModelJSON),
	)
	if err != nil {
		proxyCmd.Process.Kill()
		log.Fatalf("[SETUP] Failed to create peak model: %v", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("[SETUP] Peak model creation returned status: %d", resp.StatusCode)
	}
	log.Printf("[SETUP] Peak model created")

	// Create test-fallback model WITH peak hour config
	log.Printf("[SETUP] Creating %s model with peak hour...", testFallbackModel)
	fallbackModelData := map[string]interface{}{
		"id":                 testFallbackModel,
		"name":               "Test Fallback Model",
		"enabled":            true,
		"internal":           true,
		"credential_id":      "test-peak-cred",
		"internal_base_url":  fmt.Sprintf("http://localhost:%d/v1", mockPort),
		"internal_model":     "mock-fallback-normal", // Normal model for fallback (succeeds)
		"peak_hour_enabled":  true,
		"peak_hour_start":    "00:00", // Always active
		"peak_hour_end":      "23:59",
		"peak_hour_timezone": "+0",
		"peak_hour_model":    "mock-fallback-peak", // Peak hour model for fallback (fails)
	}

	fallbackModelJSON, _ := json.Marshal(fallbackModelData)

	resp, err = http.Post(
		fmt.Sprintf("http://localhost:%d/fe/api/models", proxyPort),
		"application/json",
		bytes.NewReader(fallbackModelJSON),
	)
	if err != nil {
		proxyCmd.Process.Kill()
		log.Fatalf("[SETUP] Failed to create fallback model: %v", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("[SETUP] Fallback model creation returned status: %d", resp.StatusCode)
	}
	log.Printf("[SETUP] Fallback model created")

	log.Printf("[TEST] Sending request to proxy for model '%s'...", testPeakModel)
	reqBody := map[string]interface{}{
		"model": testPeakModel,
		"messages": []map[string]string{
			{"role": "user", "content": "Hello, respond with your model name."},
		},
		"stream": false,
	}
	reqBodyJSON, _ := json.Marshal(reqBody)

	// Create request context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://localhost:%d/v1/chat/completions", proxyPort), bytes.NewReader(reqBodyJSON))
	if err != nil {
		proxyCmd.Process.Kill()
		log.Fatalf("[TEST] Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", testAPIKey))

	log.Printf("[TEST] Request body: %s", string(reqBodyJSON))

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		proxyCmd.Process.Kill()
		log.Fatalf("[TEST] Request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("[TEST] Response status: %d", resp.StatusCode)
	log.Printf("[TEST] Response body: %s", string(body))

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(body, &response); err != nil {
		log.Printf("[TEST] Failed to parse response: %v", err)
		proxyCmd.Process.Kill()
		log.Fatalf("[TEST] Test FAILED - could not parse response")
	}

	// Check for error response
	if errObj, ok := response["error"].(map[string]interface{}); ok {
		errMsg := ""
		if msg, ok := errObj["message"].(string); ok {
			errMsg = msg
		}
		log.Printf("[TEST] Got error response: %v", errObj)
		log.Printf("[TEST] ❌ FAILED - Fallback was NOT invoked!")
		log.Printf("[TEST] Expected: fallback model 'mock-fallback-upstream' should have handled the request")
		log.Printf("[TEST] Got error instead: %s", errMsg)
		proxyCmd.Process.Kill()
		cleanup()
		os.Exit(1)
	}

	// Extract model from response
	if choices, ok := response["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := msg["content"].(string); ok {
					log.Printf("[TEST] Response content: %s", content)

					// Check if response came from fallback model's normal model
					// (Fallback peak model also fails, so fallback's normal model handles it)
					if strings.Contains(content, "mock-fallback-normal") {
						log.Println("[TEST] ✅ SUCCESS - Fallback model's normal model was invoked!")
						proxyCmd.Process.Kill()
						cleanup()
						os.Exit(0)
					} else if strings.Contains(content, "mock-peak-upstream") {
						log.Printf("[TEST] ❌ FAILED - Peak model succeeded (unexpected - it should fail)")
						proxyCmd.Process.Kill()
						cleanup()
						os.Exit(1)
					} else if strings.Contains(content, "mock-normal-upstream") {
						log.Printf("[TEST] ❌ FAILED - Primary model's normal model was used (peak hour not working)")
						proxyCmd.Process.Kill()
						cleanup()
						os.Exit(1)
					} else if strings.Contains(content, "mock-fallback-upstream") {
						log.Printf("[TEST] ❌ FAILED - Fallback's peak model was used (peak hour incorrectly applied to fallback)")
						proxyCmd.Process.Kill()
						cleanup()
						os.Exit(1)
					} else {
						log.Printf("[TEST] ❌ FAILED - Unexpected response: %s", content)
						proxyCmd.Process.Kill()
						cleanup()
						os.Exit(1)
					}
				}
			}
		}
	}

	log.Printf("[TEST] ❌ FAILED - Could not determine response source")
	proxyCmd.Process.Kill()
	cleanup()
	os.Exit(1)
}
