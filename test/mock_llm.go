package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func main() {
	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Received request")

		// Check request body for "interrupted" to detect retry
		body, _ := io.ReadAll(r.Body)
		isRetry := strings.Contains(string(body), "interrupted")
		if isRetry {
			log.Println("Mock: Detected retry request!")
		}

		// Set headers for SSE
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
			return
		}

		// Simulate response
		messages := []string{"Hello", " world", "!", " I", " am", " a", " skipped", " token", " and", " now", " useful", " tokens."}

		for i, msg := range messages {
			// Simulate hang on strict token ONLY if not a retry
			if msg == " skipped" && !isRetry {
				// We hang here to test timeout
				log.Println("Mock: Hanging...")
				// 15 seconds is > 10s default IDLE_TIMEOUT
				// So proxy should kill this and retry
				time.Sleep(15 * time.Second)
			}

			// Normal delay
			if !isRetry {
				time.Sleep(100 * time.Millisecond)
			} else {
				// Faster on retry
				time.Sleep(50 * time.Millisecond)
			}

			jsonResp := fmt.Sprintf(`{"choices":[{"delta":{"content":"%s"}}]}`, msg)
			fmt.Fprintf(w, "data: %s\n\n", jsonResp)
			flusher.Flush()
			log.Printf("Mock: Sent token %d: '%s'", i, msg)
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		log.Println("Mock: Done")
	})

	log.Println("Mock LLM Server listening on :4000")
	log.Fatal(http.ListenAndServe(":4000", nil))
}
