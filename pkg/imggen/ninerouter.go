package imggen

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// NineRouterProviderName is the provider identity for the 9router
// image-generation upstream (an OpenAI-images-compatible gateway).
// Registered in the package-level registry at init time.
const NineRouterProviderName = "9router"

// NewNineRouterProvider returns the 9router UpstreamProvider.
func NewNineRouterProvider() UpstreamProvider {
	return &nineRouterProvider{}
}

type nineRouterProvider struct{}

// BuildUpstream constructs the upstream URL + body for the 9router
// gateway:
//   - upstreamURL = TrimSuffix(cred.BaseURL, "/") + "/images/generations"
//     (the credential's BaseURL already carries "/v1", matching the
//     minimax convention — see pkg/providers/factory.go:48).
//   - upstreamBody = shallow-copy of the client body with the "model"
//     field rewritten to cred.InternalModel. The two MiniMax-native
//     request keys that the OpenAI-images dialect does not carry
//     (aspect_ratio, response_format) are translated: aspect_ratio is
//     dropped and response_format is rewritten to "b64_json", so the
//     upstream returns the b64 payload the conversion hook converts
//     into the existing IO format. Every other body key passes
//     through verbatim.
func (p *nineRouterProvider) BuildUpstream(cred models.ResolvedCredential, clientBody []byte) (string, []byte, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(clientBody, &body); err != nil {
		return "", nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	if body == nil {
		body = make(map[string]interface{})
	}

	if cred.InternalModel != "" {
		body["model"] = cred.InternalModel
	}
	// MiniMax-native keys: aspect_ratio is not part of the
	// OpenAI-images dialect. response_format must UNCONDITIONALLY
	// request "b64_json" so the conversion hook can shape the result into
	// the proxy's IO format (data[].image_urls). If omitted by the
	// client, OpenAI's Images API defaults to "url", which would yield
	// URL-shaped output that fails conversion after burning upstream quota.
	// An explicit "url" requested by the client is also overridden.
	delete(body, "aspect_ratio")
	body["response_format"] = "b64_json"

	upstreamBody, err := json.Marshal(body)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal upstream body: %w", err)
	}

	upstreamURL := strings.TrimSuffix(cred.BaseURL, "/") + "/images/generations"
	return upstreamURL, upstreamBody, nil
}

// InspectResponse classifies the 9router upstream response into a
// relay decision:
//   - HTTP >= 400 ⇒ PromoteToError with RelayStatus = the upstream
//     status (so a 429 stays a 429, not a 502). Code = CodeCooldown
//     on 429, CodeUpstreamHTTPError otherwise. Outcome.Message is the
//     human-readable message extracted (and unwrapped) from the
//     upstream error body — it never carries the raw nested JSON
//     blob.
//   - empty body / unparseable JSON / missing or empty data ⇒
//     PromoteToError 502, CodeUpstreamMalformed (missing/empty data
//     carries CodeEmptyImage).
//   - HTTP 2xx with a non-empty data array ⇒ PassAsIs (RelayStatus 0).
func (p *nineRouterProvider) InspectResponse(upstreamStatus int, body []byte) Outcome {
	if upstreamStatus >= 400 {
		code := CodeUpstreamHTTPError
		if upstreamStatus == 429 {
			code = CodeCooldown
		}
		return Outcome{
			RelayStatus: upstreamStatus,
			Reason:      fmt.Sprintf("upstream HTTP %d", upstreamStatus),
			Code:        code,
			Message:     nineRouterErrorMessage(body),
		}
	}

	if len(body) == 0 {
		return Outcome{RelayStatus: 502, Reason: "empty body", Code: CodeUpstreamMalformed}
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Outcome{RelayStatus: 502, Reason: "unparseable JSON", Code: CodeUpstreamMalformed}
	}

	data, ok := resp["data"]
	if !ok || data == nil {
		return Outcome{RelayStatus: 502, Reason: "missing data key", Code: CodeUpstreamMalformed}
	}
	items, ok := data.([]interface{})
	if !ok {
		return Outcome{RelayStatus: 502, Reason: "unexpected data shape", Code: CodeUpstreamMalformed}
	}
	if len(items) == 0 {
		return Outcome{RelayStatus: 502, Reason: "empty data array", Code: CodeEmptyImage}
	}
	// Validate data items: check if all items are null or empty objects (e.g. data:[null] or data:[{}]).
	// If all items are null or empty, fail early as CodeEmptyImage with 502.
	allNullOrEmpty := true
	for _, item := range items {
		if item == nil {
			continue
		}
		m, ok := item.(map[string]interface{})
		if !ok || len(m) == 0 {
			continue
		}
		allNullOrEmpty = false
		break
	}
	if allNullOrEmpty {
		return Outcome{RelayStatus: 502, Reason: "empty data array", Code: CodeEmptyImage}
	}
	return Outcome{RelayStatus: 0, Reason: "", Code: CodeOK}
}

// BillableImages returns the number of billable images. If called on the converted
// body, it reads metadata.success_count. If called on an unconverted upstream body,
// it counts only valid convertible items so billing never exceeds delivered images.
// In the normal fully-convertible case, BillableImages equals len(data).
func (p *nineRouterProvider) BillableImages(body []byte) (int, error) {
	if len(body) == 0 {
		return 0, nil
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, nil
	}
	if md, ok := resp["metadata"].(map[string]interface{}); ok {
		if sc, ok := md["success_count"]; ok {
			if n, ok := coerceCount(sc); ok {
				return n, nil
			}
		}
	}
	data, ok := resp["data"].([]interface{})
	if !ok {
		return 0, nil
	}
	count := 0
	for _, item := range data {
		m, ok := item.(map[string]interface{})
		if !ok || m == nil {
			continue
		}
		if urls, ok := m["image_urls"].([]interface{}); ok && len(urls) > 0 {
			count++
			continue
		}
		if b64, ok := m["b64_json"].(string); ok && strings.TrimSpace(b64) != "" {
			count++
			continue
		}
	}
	return count, nil
}

// RewriteResponse converts the 9router OpenAI-images response
// (data[].b64_json) into the existing MiniMax img-gen IO format
// (data[].image_urls with data-URIs, metadata, base_resp). It implements
// the ResponseRewriter seam.
//
// Notes on dialect translation:
//   - MiniMax shape: returns data[].image_urls with data-URIs, metadata
//     (success_count, failed_count), and base_resp (status_code 0). This is
//     NOT full OpenAI URL compatibility; clients expecting URLs will have
//     their response_format overridden to b64_json in BuildUpstream and
//     receive data-URIs here.
//   - revised_prompt: deliberately omitted. OpenAI DALL-E returns
//     revised_prompt in data[], but the proxy's unified img-gen IO format
//     does not carry it; only image_urls are delivered to clients.
//   - created: passed through conditionally when present in the upstream
//     response; omitted when absent.
//   - Partial conversion: success_count counts converted items, while
//     failed_count equals the number of unconvertible items. Billing via
//     BillableImages never exceeds delivered images.
//   - Conversion errors never leak raw upstream response bodies.
func (p *nineRouterProvider) RewriteResponse(body []byte) ([]byte, error) {
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unparseable JSON: %w", err)
	}
	rawItems, ok := resp["data"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("missing data array")
	}

	out := make([]map[string]interface{}, 0, len(rawItems))
	for i, item := range rawItems {
		m, ok := item.(map[string]interface{})
		if !ok || m == nil {
			continue
		}
		// Upstream already speaking the existing IO format — pass
		// through verbatim.
		if urls, ok := m["image_urls"]; ok {
			out = append(out, map[string]interface{}{"image_urls": urls})
			continue
		}
		b64, ok := m["b64_json"].(string)
		if !ok || strings.TrimSpace(b64) == "" {
			continue // unusable item — skipped
		}
		uri, err := imageDataURIFromBase64(b64)
		if err != nil {
			return nil, fmt.Errorf("data[%d]: %w", i, err)
		}
		out = append(out, map[string]interface{}{"image_urls": []string{uri}})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no convertible image in data")
	}

	failedCount := len(rawItems) - len(out)
	converted := map[string]interface{}{
		"data": out,
		"metadata": map[string]interface{}{
			"success_count": fmt.Sprintf("%d", len(out)),
			"failed_count":  fmt.Sprintf("%d", failedCount),
		},
		"base_resp": map[string]interface{}{
			"status_code": 0,
			"status_msg":  "success",
		},
	}
	if created, ok := resp["created"]; ok {
		converted["created"] = created
	}
	return json.Marshal(converted)
}

// NormalizeErrorBody flattens the 9router nested-JSON error message
// into a client-friendly OpenAI-style error body. It implements the
// ErrorBodyNormalizer seam.
func (p *nineRouterProvider) NormalizeErrorBody(body []byte) []byte {
	msg := nineRouterErrorMessage(body)
	if msg == "" {
		return body
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return body
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		return body
	}
	errObj["message"] = msg
	out, err := json.Marshal(resp)
	if err != nil {
		return body
	}
	return out
}

// nineRouterErrorMessage extracts the human-readable message from a
// 9router error body, unwrapping the nested JSON document embedded
// inside error.message (the live 429 shape:
//
//	{"error":{"message":"[antigravity/gemini-3.8-flash] [429]: {\"error\":{\"code\":429,\"message\":\"You have exhausted your capacity on this model. Your quota will reset after 4h39m9s.\",\"status\":\"RESOURCE_EXHAUSTED\",\"details\":[...]}}","type":"..."}}
//
// The extraction unwraps the embedded document and returns the
// innermost human-readable message. A parse failure at any level
// degrades to the next-best text (the outer message), never to "".
func nineRouterErrorMessage(body []byte) string {
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return ""
	}
	return extractNineRouterMessage(resp, 0)
}

const maxNineRouterUnwrapDepth = 4

func extractNineRouterMessage(resp map[string]interface{}, depth int) string {
	if resp == nil || depth > maxNineRouterUnwrapDepth {
		return ""
	}
	msg := ""
	if errObj, ok := resp["error"].(map[string]interface{}); ok {
		if s, ok := errObj["message"].(string); ok {
			msg = s
		}
	}
	if msg == "" {
		if s, ok := resp["message"].(string); ok {
			msg = s
		}
	}
	if msg == "" {
		return ""
	}
	// Unwrap an embedded JSON document, if present.
	if inner := embeddedJSONObject(msg); inner != nil {
		if s := extractNineRouterMessage(inner, depth+1); s != "" {
			return s
		}
	}
	return strings.TrimSpace(msg)
}

// embeddedJSONObject finds and parses the first or innermost JSON object embedded
// in s. It tries the widest { ... } span first, and if unmarshal fails, searches
// for nested or innermost parseable JSON objects. Returns nil when no JSON object
// can be parsed, preserving graceful degradation.
func embeddedJSONObject(s string) map[string]interface{} {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(s[start:end+1]), &out); err == nil {
		return out
	}
	// If the outermost span failed (e.g. malformed prefix/suffix or multiple sibling objects),
	// search backwards from last '}' or forwards from '{' to find the innermost valid JSON object.
	for i := strings.LastIndexByte(s, '{'); i >= 0; i = strings.LastIndexByte(s[:i], '{') {
		for j := strings.LastIndexByte(s, '}'); j > i; j = strings.LastIndexByte(s[:j], '}') {
			if err := json.Unmarshal([]byte(s[i:j+1]), &out); err == nil {
				return out
			}
		}
	}
	return nil
}

// init registers the 9router provider in the package-level registry.
func init() {
	registry[NineRouterProviderName] = NewNineRouterProvider()
}
