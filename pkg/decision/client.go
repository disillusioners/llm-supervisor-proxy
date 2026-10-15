package decision

import (
	"net/http"
	"time"
)

// sharedHTTPClient is a module-level HTTP client with connection
// pooling (Decision Models commission / §3.2 — mirrors
// pkg/imggen/client.go verbatim). Reusing a single client
// prevents accumulation of orphaned connection pools that occur
// when creating a new client per request.
//
// Decisions are synchronous, vendor-claimed 10× faster than the
// OpenAI Responses API (UNVERIFIED marketing per amendment 8;
// tune post-deploy on live data). The transport declares
// ResponseHeaderTimeout: 0 EXPLICITLY — the default is 0 but a
// silent future "add a 30s header timeout" change would false-fire
// on slow upstreams where the first byte hasn't arrived yet. The
// field is annotated inline so a future contributor sees the trap.
//
// Timeout is 0 (context-driven; the handler enforces the
// DecisionTimeout via context.WithTimeout). A client-level Timeout
// can't distinguish context-deadline from transport-failure and
// would double-fire.
var sharedHTTPClient = &http.Client{
	Timeout: 0, // context-driven; handler enforces DecisionTimeout
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     300 * time.Second,
		// CRITICAL: openai-decision / typesafe may return only
		// after a slow first byte (vendor-claimed sync but
		// unverified). Context-driven timeout via DecisionTimeout.
		// Default is 0 but a silent "add 30s header timeout"
		// change would false-fire on decision. Do not change
		// without re-reading plan §3.2 / §3.7.
		ResponseHeaderTimeout: 0,
	},
}
