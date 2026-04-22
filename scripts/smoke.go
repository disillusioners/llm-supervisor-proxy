// Smoke test script for LLM Supervisor Proxy
// Usage:
//
//	go run scripts/smoke.go -model gpt-4o                    # single model
//	go run scripts/smoke.go -models gpt-4o,claude-3-5-sonnet  # multiple models
//	go run scripts/smoke.go -models @models.txt              # models from file
//	go run scripts/smoke.go -model gpt-4o -ultimate          # force ultimate model path
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	proxyURL = "http://localhost:4123/v1/chat/completions"
	timeout  = 90 * time.Second
)

var (
	models    = flag.String("models", "", "Comma-separated models (or @file for list)")
	model     = flag.String("model", "", "Single model shorthand")
	outputDir = flag.String("output", "smoke_results", "Output directory")
	compare   = flag.Bool("compare", true, "Generate comparison report")
	ultimate  = flag.Bool("ultimate", false, "Force ultimate model path (X-Force-Ultimate-Model header)")
)

func main() {
	flag.Parse()

	modelList := collectModels()
	if len(modelList) == 0 {
		fmt.Println("Error: no models (use -model or -models)")
		flag.Usage()
		os.Exit(1)
	}

	mode := ""
	if *ultimate {
		mode = " (ultimate mode)"
	}
	fmt.Printf("Testing %d model(s)%s: %v\n\n", len(modelList), mode, modelList)

	os.MkdirAll(*outputDir, 0755)

	results := make(map[string]*ModelResult)
	for _, m := range modelList {
		fmt.Printf("Testing: %s\n", m)
		r := testModel(m)
		results[m] = r
		saveResult(m, r)
		status := "✅"
		if len(r.ToolCalls) == 0 {
			status = "⚠️ "
		}
		fmt.Printf("  %s Took: %v | Tools: %d\n\n", status, r.Duration, len(r.ToolCalls))
	}

	if *compare && len(modelList) > 1 {
		genComparison(modelList, results)
	}

	fmt.Println("Results:", *outputDir)
}

func collectModels() []string {
	var list []string
	if *model != "" {
		list = append(list, *model)
	}
	if *models != "" {
		if strings.HasPrefix(*models, "@") {
			data, _ := os.ReadFile(strings.TrimPrefix(*models, "@"))
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") {
					list = append(list, line)
				}
			}
		} else {
			for _, m := range strings.Split(*models, ",") {
				m = strings.TrimSpace(m)
				if m != "" {
					list = append(list, m)
				}
			}
		}
	}
	return list
}

type ModelResult struct {
	Model      string
	Duration   time.Duration
	StatusCode int
	Content    string
	ToolCalls  []ToolCall
	RawJSON    string
	StreamData []string
	Error      string
}

type ToolCall struct {
	Name string
	Args string
}

func testModel(modelName string) *ModelResult {
	start := time.Now()
	reqBody := buildRequest(modelName)
	resp, err := sendRequest(reqBody)
	duration := time.Since(start)

	result := &ModelResult{Model: modelName, Duration: duration}

	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		result.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))
		return result
	}

	// Parse streaming SSE response
	result.StreamData, result.Content, result.ToolCalls = parseStreamResponse(resp.Body)

	// Store raw SSE chunks as JSON array
	var rawJSON bytes.Buffer
	rawJSON.WriteString("[\n")
	for i, chunk := range result.StreamData {
		if i > 0 {
			rawJSON.WriteString(",\n")
		}
		data, _ := json.Marshal(chunk)
		rawJSON.Write(data)
	}
	rawJSON.WriteString("\n]")
	result.RawJSON = rawJSON.String()

	return result
}

func parseRawChunks(content string) []string {
	var chunks []string
	reader := strings.NewReader(content)
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunks = append(chunks, string(buf[:n]))
		}
		if err != nil {
			break
		}
	}
	return chunks
}

func parseStreamResponse(body io.Reader) ([]string, string, []ToolCall) {
	var streamData []string
	var fullContent strings.Builder
	var toolCalls []ToolCall
	scanner := bufio.NewScanner(body)

	// Increase buffer size for larger chunks
	const maxCapacity = 1024 * 1024 // 1MB
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	for scanner.Scan() {
		line := scanner.Text()
		streamData = append(streamData, line)

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if choices, ok := chunk["choices"].([]interface{}); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]interface{}); ok {
				// Extract delta content
				if delta, ok := choice["delta"].(map[string]interface{}); ok {
					// Content delta
					if content, ok := delta["content"].(string); ok {
						fullContent.WriteString(content)
					}
					// Tool calls in streaming format are in delta.tool_calls
					if tc, ok := delta["tool_calls"].([]interface{}); ok {
						for _, t := range tc {
							if tMap, ok := t.(map[string]interface{}); ok {
								if fn, ok := tMap["function"].(map[string]interface{}); ok {
									toolCalls = append(toolCalls, ToolCall{
										Name: fmt.Sprintf("%v", fn["name"]),
										Args: fmt.Sprintf("%v", fn["arguments"]),
									})
								}
							}
						}
					}
				}
			}
		}
	}
	return streamData, fullContent.String(), toolCalls
}

func buildRequest(model string) map[string]interface{} {
	return map[string]interface{}{
		"model": model,
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": "List the files and directories in the current working directory. Use the list_files_and_directories tool.",
			},
		},
		"tools": []map[string]interface{}{
			{
				"type": "function",
				"function": map[string]interface{}{
					"name":        "list_files_and_directories",
					"description": "Lists files and directories in the specified path",
					"parameters": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"path": map[string]interface{}{
								"type":        "string",
								"description": "The directory path to list (default: current directory)",
							},
						},
					},
				},
			},
		},
		"tool_choice": "auto",
		"stream":      true,
	}
}

func sendRequest(reqBody map[string]interface{}) (*http.Response, error) {
	data, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", proxyURL, bytes.NewBuffer(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-4de49a4237e09e98c5aa6ffae5f2cb299835b8c4670119f641888c70c63f21b4")
	if *ultimate {
		req.Header.Set("X-Force-Ultimate-Model", "true")
	}
	return (&http.Client{Timeout: timeout}).Do(req)
}

func saveResult(model string, r *ModelResult) {
	filename := filepath.Join(*outputDir, safeFilename(model)+".json")
	data, _ := json.MarshalIndent(r, "", "  ")
	os.WriteFile(filename, data, 0644)
}

func extractContent(result map[string]interface{}) string {
	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := msg["content"].(string); ok {
					return strings.TrimSpace(content)
				}
			}
		}
	}
	return ""
}

func extractToolCalls(result map[string]interface{}) []ToolCall {
	var calls []ToolCall
	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				if toolCalls, ok := msg["tool_calls"].([]interface{}); ok {
					for _, tc := range toolCalls {
						if tcMap, ok := tc.(map[string]interface{}); ok {
							if fn, ok := tcMap["function"].(map[string]interface{}); ok {
								calls = append(calls, ToolCall{
									Name: fmt.Sprintf("%v", fn["name"]),
									Args: fmt.Sprintf("%v", fn["arguments"]),
								})
							}
						}
					}
				}
			}
		}
	}
	return calls
}

func safeFilename(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, ":", "-")
	return name
}

func genComparison(models []string, results map[string]*ModelResult) {
	var b strings.Builder
	b.WriteString("# Model Comparison Report\n\n")
	b.WriteString(fmt.Sprintf("Generated: %s\n\n", time.Now().Format(time.RFC3339)))
	b.WriteString("| Model | Status | Duration | Tool Calls | Content |\n")
	b.WriteString("|-------|--------|----------|------------|--------|\n")

	for _, m := range models {
		r := results[m]
		status := "❌ " + r.Error
		if r.StatusCode == http.StatusOK {
			status = "✅ OK"
			if len(r.ToolCalls) == 0 {
				status = "⚠️  No tools"
			}
		}
		content := r.Content
		if len(content) > 50 {
			content = content[:50] + "..."
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %v | %d | %s |\n",
			m, status, r.Duration, len(r.ToolCalls), content))
	}

	report := filepath.Join(*outputDir, "comparison.md")
	os.WriteFile(report, []byte(b.String()), 0644)
	fmt.Println("Comparison:", report)
}
