# Future Roadmap: Features & Frontend Improvements

> Document for review - prioritized improvements for LLM Supervisor Proxy

---

## Project Snapshot

**LLM Supervisor Proxy** is a Go sidecar proxy that prevents LLM "zombie" requests (streams that hang mid-generation). It features:
- Multi-strategy auto-retry (heartbeat monitoring, upstream recovery, generation guard)
- Model fallback chains with smart resume
- Advanced loop detection (exact match, SimHash, oscillation patterns)
- Preact-based real-time monitoring dashboard

---

## Priority Matrix

### 🔥 P0: High Impact, Quick Wins

| Feature | Effort | Impact | Description |
|---------|--------|--------|-------------|
| **Dark Mode** | 1-2h | High | Toggle dark/light themes. Devs run this locally - eye strain matters. |
| **Request Search/Filter** | 2-3h | High | Filter requests by status, model, or text content. 100 request limit fills fast during debugging. |
| **Export Request (JSON)** | 30m | Medium | Download individual request as JSON for sharing/bug reports. |

### 🚀 P1: High Impact, Medium Effort

| Feature | Effort | Impact | Description |
|---------|--------|--------|-------------|
| **Retry Chain Visualization** | 4-6h | High | Visual tree/timeline of retry attempts. Current text-based retry tracking is hard to parse at a glance. |
| **Token/Cost Dashboard** | 4-6h | High | Aggregate token usage + estimated costs per model. Users proxy LLM calls = they care about spending. |
| **Prometheus Metrics Endpoint** | 3-4h | High | Export metrics: retry rates, latency histograms, model success rates. Essential for production monitoring. |
| **Request Replay** | 3-4h | High | Re-send a failed request with modified parameters. Invaluable for debugging why retries failed. |

### 📊 P2: Medium Impact

| Feature | Effort | Impact | Description |
|---------|--------|--------|-------------|
| **Alert Webhooks** | 2-3h | Medium | POST to Slack/Discord/email when retry chains exceed threshold. Critical for production awareness. |
| **Model Health Dashboard** | 3-4h | Medium | Show success rates, avg latency, error types per model. Helps identify problematic upstreams. |
| **Request Diff View** | 2-3h | Medium | Compare original vs retried request payloads side-by-side. |
| **Keyboard Shortcuts** | 1-2h | Low | Power-user navigation (j/k for list, r for refresh, etc.) |
| **PWA Support** | 1-2h | Low | Install dashboard as standalone app with offline request cache. |

### 🔮 P3: Future Considerations

| Feature | Effort | Impact | Description |
|---------|--------|--------|-------------|
| **Persistence Layer** | 2-3 days | High | SQLite/Postgres for long-term request history beyond 100 in-memory limit. |
| **Authentication** | 1-2 days | High | Basic auth / API keys for production deployments. |
| **Multi-tenancy** | 3-5 days | Medium | Org/project separation for team deployments. |
| **Distributed Tracing** | 2-3 days | Medium | OpenTelemetry integration for request tracing across services. |
| **Custom Rules Engine** | 3-5 days | Medium | User-defined retry rules based on response patterns (regex matching, etc.) |

---

## Frontend-Specific Improvements

### UI/UX Enhancements

1. **Responsive Design Audit**
   - Dashboard may have layout issues on smaller screens
   - Consider collapsible sidebar for mobile

2. **Loading States**
   - Add skeleton loaders for async content
   - Better error boundaries with retry buttons

3. **Status Indicators**
   - More distinct visual states for: pending, streaming, success, failed, retrying
   - Animated indicators for active streams

4. **Accessibility (a11y)**
   - ARIA labels for interactive elements
   - Keyboard navigation support
   - Screen reader friendly status announcements

5. **Performance**
   - Virtual scrolling for request list (100 items is fine, but future-proof)
   - Debounced search inputs
   - Optimistic UI updates

---

## Suggested Implementation Order

### Sprint 1: Quick Wins
1. Dark mode toggle
2. Request search/filter
3. Export request JSON

### Sprint 2: Observability
1. Prometheus metrics endpoint
2. Token/cost dashboard
3. Model health status

### Sprint 3: Debugging Power Tools
1. Retry chain visualization
2. Request replay
3. Request diff view

### Sprint 4: Production Readiness
1. Alert webhooks
2. Authentication (if needed)
3. Persistence layer (if needed)

---

## Questions for Review

1. **Target users**: Is this primarily for local dev or production deployments? (affects auth/persistence priority)
2. **Cost tracking**: Do we want actual dollar estimates? Would need configurable pricing per model.
3. **Persistence**: Is the 100 request limit problematic? Should we add SQLite?
4. **Webhooks**: Which platforms matter most? (Slack, Discord, PagerDuty, custom)
5. **Metrics**: Prometheus only, or also support OpenTelemetry/StatsD?

---

## Technical Notes

### Dark Mode Implementation
```typescript
// Tailwind config - add darkMode: 'class'
// Add toggle component to store preference in localStorage
// Apply 'dark' class to <html> element
```

### Request Search Implementation
```typescript
// Add search state to RequestList component
// Filter by: status (success/failed/retrying), model name, request text
// Consider debounced search for text matching
```

### Prometheus Metrics
```
# Potential metrics to export
llm_proxy_requests_total{model, status}
llm_proxy_retries_total{model, reason}
llm_proxy_request_duration_seconds{model}
llm_proxy_tokens_total{model, type} // prompt vs completion
```

---

*Last updated: 2026-02-22*
