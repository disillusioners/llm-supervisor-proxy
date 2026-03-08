# Routing & Auth Refactor Plan

## Overview

Refactor the LLM Supervisor Proxy routing structure to:
1. Move UI to `/ui` subpath
2. Move frontend APIs to `/fe/api/*` with OAuth2-Proxy + Casdoor authentication
3. Keep LLM proxy endpoint unchanged (auth handled by litellm api_key)

---

## Current State

| Path | Handler | Auth |
|------|---------|------|
| `/` | UI static files | None |
| `/api/config` | Frontend API | None |
| `/api/models` | Frontend API | None |
| `/api/models/{id}` | Frontend API | None |
| `/api/models/validate` | Frontend API | None |
| `/api/events` | Frontend API (SSE) | None |
| `/api/requests` | Frontend API | None |
| `/api/requests/{id}` | Frontend API | None |
| `/v1/chat/completions` | LLM Proxy | None (api_key in request) |

**Problem**: All endpoints are publicly accessible. Dashboard allows config/model changes without authentication.

---

## Target State

| Path | Handler | Auth |
|------|---------|------|
| `/ui/*` | UI static files | OAuth2-Proxy + Casdoor |
| `/fe/api/config` | Frontend API | OAuth2-Proxy + Casdoor |
| `/fe/api/models` | Frontend API | OAuth2-Proxy + Casdoor |
| `/fe/api/events` | Frontend API (SSE) | OAuth2-Proxy + Casdoor |
| `/fe/api/requests` | Frontend API | OAuth2-Proxy + Casdoor |
| `/v1/chat/completions` | LLM Proxy | None (litellm api_key) |

---

## Implementation Plan

### Phase 1: Infrastructure - OAuth2-Proxy Setup

Deploy oauth2-proxy in the cluster, configured to use Casdoor as OIDC provider.

**New resources needed:**
```yaml
# k8s/secrets/oauth2-proxy-secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: oauth2-proxy-secret
  namespace: llmproxy
type: Opaque
stringData:
  client-id: <casdoor-client-id>
  client-secret: <casdoor-client-secret>
  cookie-secret: <random-32-char-string>
```

```yaml
# k8s/templates/oauth2-proxy-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: oauth2-proxy
  namespace: llmproxy
spec:
  replicas: 1
  selector:
    matchLabels:
      app: oauth2-proxy
  template:
    metadata:
      labels:
        app: oauth2-proxy
    spec:
      containers:
      - name: oauth2-proxy
        image: quay.io/oauth2-proxy/oauth2-proxy:v7.6.0
        args:
        - --provider=oidc
        - --oidc-issuer-url=https://door.daoduc.org
        - --client-id=$(CLIENT_ID)
        - --client-secret=$(CLIENT_SECRET)
        - --cookie-secret=$(COOKIE_SECRET)
        - --upstream=file:///dev/null
        - --email-domain=daoduc.org
        - --cookie-expire=24h
        - --skip-provider-button=true
        envFrom:
        - secretRef:
            name: oauth2-proxy-secret
        ports:
        - containerPort: 4180
---
apiVersion: v1
kind: Service
metadata:
  name: oauth2-proxy
  namespace: llmproxy
spec:
  ports:
  - port: 4180
    targetPort: 4180
  selector:
    app: oauth2-proxy
```

**Casdoor configuration needed:**
1. Create a new application in Casdoor for `llm.daoduc.org`
2. Set redirect URI: `https://llm.daoduc.org/oauth2/callback`
3. Note client-id and client-secret

---

### Phase 2: Backend Route Changes

**File: `pkg/ui/server.go`**

> **Note**: This project uses Go standard library `*http.ServeMux`, not chi router.

Current:
```go
// UI at root
mux.HandleFunc("/", s.serveIndex)
mux.Handle("/static/", http.StripPrefix("/static/", fileServer))

// API at /api
mux.HandleFunc("/api/config", s.handleConfig)
mux.HandleFunc("/api/models", s.handleModels)
// ...
```

Change to:
```go
// UI at /ui
mux.HandleFunc("/ui/", func(w http.ResponseWriter, r *http.Request) {
    path := strings.TrimPrefix(r.URL.Path, "/ui/")
    if path == "" || path == "index.html" {
        r.URL.Path = "/"
        fileServer.ServeHTTP(w, r)
        return
    }
    // Try static file first
    f, err := staticFS.Open(path)
    if err == nil {
        f.Close()
        http.StripPrefix("/ui/", fileServer).ServeHTTP(w, r)
        return
    }
    // Fallback to index.html for SPA client-side routing
    r.URL.Path = "/"
    fileServer.ServeHTTP(w, r)
})

// API at /fe/api
mux.HandleFunc("/fe/api/config", s.handleConfig)
mux.HandleFunc("/fe/api/models", s.handleModels)
mux.HandleFunc("/fe/api/models/", s.handleModelDetail)
mux.HandleFunc("/fe/api/models/validate", s.handleValidateModel)
mux.HandleFunc("/fe/api/events", s.handleEvents)
mux.HandleFunc("/fe/api/requests", s.handleRequests)
mux.HandleFunc("/fe/api/requests/", s.handleRequestDetail)
```

**File: `cmd/main.go`**

Ensure routes are mounted correctly and `/v1/chat/completions` remains at root level.

---

### Phase 3: Frontend Changes

**File: `pkg/ui/frontend/src/hooks/useApi.ts`**

Change:
```typescript
const API_BASE = '/api';
```

To:
```typescript
const API_BASE = '/fe/api';
```

**File: `pkg/ui/frontend/src/hooks/useEvents.ts`**

Change:
```typescript
const eventSource = new EventSource('/api/events');
```

To:
```typescript
const eventSource = new EventSource('/fe/api/events');
```

**File: `pkg/ui/frontend/vite.config.ts`**

Two changes needed:

1. Set base path for production builds:
```typescript
export default defineConfig({
  base: '/ui/',
  // ...
})
```

2. Update dev server proxy for local development:
```typescript
export default defineConfig({
  base: '/ui/',
  server: {
    proxy: {
      '/fe/api': 'http://localhost:4321',  // Changed from '/api'
    },
  },
  // ...
})
```

---

### Phase 4: Ingress Configuration

> **Critical**: NGINX Ingress annotations apply to the *entire* Ingress resource. We must split into **two separate Ingress resources** to prevent auth from being applied to the LLM proxy endpoint.

**File: `k8s/templates/ingress-protected.yaml`** (Protected routes with OAuth)

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: llm-supervisor-proxy-protected
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
    # OAuth2-Proxy auth annotations
    nginx.ingress.kubernetes.io/auth-url: "http://oauth2-proxy.llmproxy.svc.cluster.local:4180/oauth2/auth"
    nginx.ingress.kubernetes.io/auth-signin: "https://$host/oauth2/start?rd=$escaped_request_uri"
    nginx.ingress.kubernetes.io/auth-response-headers: "X-Auth-Request-User,X-Auth-Request-Email"
spec:
  tls:
    - secretName: llm-supervisor-proxy-tls
      hosts:
        - llm.daoduc.org
  rules:
    - host: llm.daoduc.org
      http:
        paths:
          # UI - protected
          - path: /ui
            pathType: Prefix
            backend:
              service:
                name: llm-supervisor-proxy
                port:
                  number: 4321
          
          # Frontend API - protected
          - path: /fe/api
            pathType: Prefix
            backend:
              service:
                name: llm-supervisor-proxy
                port:
                  number: 4321
```

**File: `k8s/templates/ingress-public.yaml`** (Public routes without auth)

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: llm-supervisor-proxy-public
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
    # NO auth annotations here - these paths are public
spec:
  tls:
    - secretName: llm-supervisor-proxy-tls
      hosts:
        - llm.daoduc.org
  rules:
    - host: llm.daoduc.org
      http:
        paths:
          # OAuth2 callback - must be public for auth flow
          - path: /oauth2
            pathType: Prefix
            backend:
              service:
                name: oauth2-proxy
                port:
                  number: 4180
          
          # LLM Proxy - NO AUTH (uses api_key validated by litellm)
          - path: /v1
            pathType: Prefix
            backend:
              service:
                name: llm-supervisor-proxy
                port:
                  number: 4321
```

**Why two Ingress resources?**

NGINX Ingress annotations are per-Ingress, not per-path. If we put all paths in one Ingress with auth annotations, the `/v1/chat/completions` endpoint would require OAuth authentication, breaking LLM clients that only send API keys.

---

## Migration Strategy

### Option A: Direct Cutover (Recommended)
1. Deploy oauth2-proxy
2. Update backend routes
3. Update frontend
4. Update ingress
5. Deploy in one release

**Pros**: Clean, no backward compatibility needed
**Cons**: Brief downtime during deployment

### Option B: Dual Path (Zero Downtime)
1. Deploy new routes alongside old routes
2. Update ingress to route new paths
3. Remove old routes after migration

**Pros**: Zero downtime, rollback easy
**Cons**: More complex, dual maintenance

---

## Files Changed Summary

| File | Change Type |
|------|-------------|
| `k8s/templates/oauth2-proxy-deployment.yaml` | New |
| `k8s/secrets/oauth2-proxy-secret.yaml` | New |
| `k8s/templates/ingress-protected.yaml` | New (split from ingress.yaml) |
| `k8s/templates/ingress-public.yaml` | New (split from ingress.yaml) |
| `k8s/templates/ingress.yaml` | Deleted (replaced by two separate ingresses) |
| `pkg/ui/server.go` | Modified |
| `pkg/ui/frontend/src/hooks/useApi.ts` | Modified |
| `pkg/ui/frontend/src/hooks/useEvents.ts` | Modified |
| `pkg/ui/frontend/vite.config.ts` | Modified (base path + proxy) |

---

## Testing Checklist

- [ ] OAuth2-Proxy deployed: `kubectl get pods -n llmproxy -l app=oauth2-proxy`
- [ ] OAuth2-Proxy logs healthy: `kubectl logs -n llmproxy -l app=oauth2-proxy`
- [ ] Access `https://llm.daoduc.org/ui` redirects to Casdoor login
- [ ] After login, UI loads correctly with static assets (CSS, JS)
- [ ] Frontend API calls work: `GET https://llm.daoduc.org/fe/api/models`
- [ ] SSE events work: `GET https://llm.daoduc.org/fe/api/events` (with auth cookie)
- [ ] LLM proxy works without auth: `POST https://llm.daoduc.org/v1/chat/completions` with `Authorization: Bearer <api_key>`
- [ ] Logout works correctly: `GET https://llm.daoduc.org/oauth2/sign_out`
- [ ] Both Ingress resources created: `kubectl get ingress -n llmproxy`

---

## Open Questions

1. **Session duration**: 
   - Default: `168h` (7 days)
   - Recommendation: `24h` for stricter security, requiring periodic re-authentication
   - Configured via `--cookie-expire` flag in oauth2-proxy

2. **Allowed domains**:
   - Option A: `--email-domain=*` (allow all) - Only use if Casdoor application-level restrictions are in place (explicitly invited users only)
   - Option B: `--email-domain=daoduc.org` - Restrict to specific organization domain (safer)
   - Recommendation: Start with specific domain restriction, open up only if needed

3. **Role-based access**: Need different permission levels (read-only vs admin)?
   - Not covered in this plan - would require additional code changes
   - Could leverage Casdoor roles passed via headers if needed later

---

## References

- [OAuth2-Proxy Documentation](https://oauth2-proxy.github.io/oauth2-proxy/)
- [Casdoor OIDC Configuration](https://casdoor.org/docs/oidc/oidc-overview)
- [nginx-ingress External Auth](https://kubernetes.github.io/ingress-nginx/examples/auth/external-auth/)
