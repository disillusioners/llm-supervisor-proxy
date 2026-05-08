package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Initialization
// ─────────────────────────────────────────────────────────────────────────────

// initRequestContext parses the incoming request, creates the request log,
// resolves the fallback chain, and returns a fully populated requestContext.
func (h *Handler) initRequestContext(r *http.Request) (*requestContext, error) {
	conf := h.config.Clone()
	targetURL, err := url.JoinPath(conf.UpstreamURL, "/v1/chat/completions")
	if err != nil {
		return nil, fmt.Errorf("invalid_upstream_url")
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read_body_failed")
	}
	r.Body.Close()

	var requestBody map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestBody); err != nil {
		return nil, fmt.Errorf("invalid_json")
	}

	reqID := uuid.New().String()
	startTime := time.Now()

	storeMessages := parseMessages(requestBody)
	model, _ := requestBody["model"].(string)
	originalModel := model

	// Deep-copy original messages for retry reconstruction
	var originalMessages []interface{}
	if msgs, ok := requestBody["messages"].([]interface{}); ok {
		originalMessages = make([]interface{}, len(msgs))
		copy(originalMessages, msgs)
	}

	isStream := false
	if s, ok := requestBody["stream"].(bool); ok && s {
		isStream = true
	}

	// Extract parameters (exclude standard fields that are shown separately)
	parameters := extractParameters(requestBody)

	// Extract app tag from x-proxy-app header for request grouping
	appTag := r.Header.Get("x-proxy-app")

	reqLog := &store.RequestLog{
		ID:            reqID,
		Status:        "running",
		Model:         model,
		OriginalModel: originalModel,
		StartTime:     startTime,
		Messages:      storeMessages,
		Retries:       0,
		FallbackUsed:  []string{},
		IsStream:      isStream,
		Parameters:    parameters,
		AppTag:        appTag,
	}
	h.store.Add(reqLog)

	// Resolve model ID to config at the boundary
	var resolvedModel *models.ModelConfig
	if conf.ModelsConfig != nil {
		resolvedModel = conf.ModelsConfig.GetModel(originalModel)
	}

	// Build model list using resolved config
	var modelList []string
	if resolvedModel != nil {
		// Known model — use ID and fallback chain from config
		modelList = make([]string, 0, 1+len(resolvedModel.FallbackChain))
		modelList = append(modelList, resolvedModel.ID)
		modelList = append(modelList, resolvedModel.FallbackChain...)
	} else {
		// External/unknown model — no fallback chain, just use raw name
		modelList = []string{originalModel}
	}

	// Set ModelID to the resolved ID (or raw name for external models)
	if resolvedModel != nil {
		conf.ModelID = resolvedModel.ID
	} else {
		conf.ModelID = originalModel
	}

	// Extract proxy-only flags from headers (these are stripped before forwarding upstream)
	bypassInternal := strings.EqualFold(r.Header.Get("x-llmproxy-bypass-internal"), "true")

	return &requestContext{
		conf:             conf,
		targetURL:        targetURL,
		reqID:            reqID,
		startTime:        startTime,
		reqLog:           reqLog,
		modelList:        modelList,
		resolvedModel:    resolvedModel,
		requestBody:      requestBody,
		rawBody:          bodyBytes, // Save original body bytes
		isStream:         isStream,
		originalHeaders:  r.Header,
		method:           r.Method,
		baseCtx:          r.Context(),
		originalMessages: originalMessages,
		bypassInternal:   bypassInternal,
	}, nil
}
