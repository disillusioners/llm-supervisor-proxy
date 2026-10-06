package imggen

import (
	"net/http"
	"time"
)

// sharedHTTPClient is a module-level HTTP client with connection
// pooling (BE-R4 / T1.4.3). Reusing a single client prevents
// accumulation of orphaned connection pools that occur when
// creating a new client per request.
//
// Copied from pkg/ultimatemodel/handler_external.go:28-35 with one
// CRITICAL difference (Architect Amendment 10): the Transport
// declares ResponseHeaderTimeout: 0 EXPLICITLY. The default is 0
// but a silent future "add a 30s header timeout" change would
// false-fire on 17-60s silent generations (no response headers
// for 17-60s; a 30s header timeout would close the connection
// before any byte arrived). The field is annotated inline so a
// future contributor sees the silent-generation trap.
//
// Timeout is 0 (context-driven; the handler enforces the
// ImageGenTimeout via context.WithTimeout). A client-level Timeout
// can't distinguish context-deadline from transport-failure and
// would double-fire.
var sharedHTTPClient = &http.Client{
	Timeout: 0, // context-driven; handler enforces ImageGenTimeout
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     300 * time.Second,
		// CRITICAL: 17-60s silent generation before first byte;
		// context-driven timeout via ImageGenTimeout. Default is 0
		// but a silent "add 30s header timeout" change would
		// false-fire on image-gen. Do not change without
		// re-reading phase1-plan.md T1.4.3.
		ResponseHeaderTimeout: 0,
	},
}
