# Configuration System Design Plan

## Overview

Implement a unified configuration system for `llm-supervisor-proxy` that:
1. Uses env/default values as the initial configuration
2. Auto-creates `config.json` if it doesn't exist
3. Prioritizes `config.json` over env variables when it exists
4. Keeps in-memory configuration synchronized with file changes

---

## Configuration Precedence (Priority: High → Low)

```
Environment Variables > config.json > Hardcoded Defaults
```

**Rationale** (12-Factor App methodology):
- **Env vars always win** - Required for Docker/K8s deployments where runtime overrides are needed
- **File provides persistence** - Desktop users can save preferences without shell profiles
- **Defaults ensure boot** - App always has valid config even with no file or env vars

**Why NOT file > env:**
- If config.json is baked into a Docker image, env vars become useless
- GitOps/Helm/K8s ConfigMaps rely on env var injection
- Immutable infrastructure patterns require runtime overrides

---

## File Location

Following XDG Base Directory Specification (consistent with existing `models.json`):

| Platform | Location |
|----------|----------|
| Linux/macOS | `~/.config/llm-supervisor-proxy/config.json` |
| Windows | `%APPDATA%\llm-supervisor-proxy\config.json` |
| Custom | `$XDG_CONFIG_HOME/llm-supervisor-proxy/config.json` |

**Implementation**: Use existing `os.UserConfigDir()` pattern from `pkg/models/config.go`

---

## Configuration Schema

```json
{
  "version": "1.0",
  "upstream_url": "http://localhost:4001",
  "port": 8089,
  "idle_timeout": "10s",
  "max_generation_time": "180s",
  "max_retries": 1,
  "updated_at": "2024-01-15T10:30:00Z"
}
```

### Field Mappings

| Config Field | Env Variable | Default | Type |
|--------------|--------------|---------|------|
| `upstream_url` | `UPSTREAM_URL` | `http://localhost:4001` | string |
| `port` | `PORT` | `8089` | int |
| `idle_timeout` | `IDLE_TIMEOUT` | `10s` | duration |
| `max_generation_time` | `MAX_GENERATION_TIME` | `180s` | duration |
| `max_retries` | `MAX_RETRIES` | `1` | int |

---

## Architecture

### Package Structure

```
pkg/
├── config/
│   ├── config.go       # Config struct, defaults, load/save
│   └── config_test.go  # Unit tests
```

### Core Components

#### 1. Config Struct (`pkg/config/config.go`)

```go
package config

import (
    "encoding/json"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "sync"
    "time"
)

const (
    AppName        = "llm-supervisor-proxy"
    ConfigFileName = "config.json"
    ConfigVersion  = "1.0"
)

// Duration is a custom type that serializes to human-readable format (e.g., "10s")
// instead of nanoseconds. Required because time.Duration marshals to int64.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
    return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
    var v interface{}
    if err := json.Unmarshal(data, &v); err != nil {
        return err
    }
    switch value := v.(type) {
    case string:
        parsed, err := time.ParseDuration(value)
        if err != nil {
            return err
        }
        *d = Duration(parsed)
    case float64:
        *d = Duration(time.Duration(value))
    default:
        return errors.New("invalid duration format")
    }
    return nil
}

func (d Duration) String() string {
    return time.Duration(d).String()
}

func (d Duration) Duration() time.Duration {
    return time.Duration(d)
}

// Config holds all application configuration
type Config struct {
    Version           string   `json:"version"`
    UpstreamURL       string   `json:"upstream_url"`
    Port              int      `json:"port"`
    IdleTimeout       Duration `json:"idle_timeout"`
    MaxGenerationTime Duration `json:"max_generation_time"`
    MaxRetries        int      `json:"max_retries"`
    UpdatedAt         string   `json:"updated_at"` // ISO8601 string for readability
}

// Defaults - used when env not set and file doesn't exist
var Defaults = Config{
    Version:           ConfigVersion,
    UpstreamURL:       "http://localhost:4001",
    Port:              8089,
    IdleTimeout:       Duration(10 * time.Second),
    MaxGenerationTime: Duration(180 * time.Second),
    MaxRetries:        1,
}

// Validate ensures config values are valid before saving
func (c *Config) Validate() error {
    if c.UpstreamURL == "" {
        return errors.New("upstream_url is required")
    }
    if !strings.HasPrefix(c.UpstreamURL, "http://") && !strings.HasPrefix(c.UpstreamURL, "https://") {
        return errors.New("upstream_url must start with http:// or https://")
    }
    if c.Port < 1 || c.Port > 65535 {
        return errors.New("port must be between 1 and 65535")
    }
    if c.IdleTimeout < Duration(time.Second) {
        return errors.New("idle_timeout must be at least 1s")
    }
    if c.MaxGenerationTime < Duration(time.Second) {
        return errors.New("max_generation_time must be at least 1s")
    }
    if c.MaxRetries < 0 {
        return errors.New("max_retries cannot be negative")
    }
    return nil
}

// Manager handles configuration lifecycle
type Manager struct {
    mu        sync.RWMutex
    config    Config
    filePath  string
    readOnly  bool     // true if file write fails (permission denied, etc.)
    eventBus  *events.Bus // optional: for publishing config updates
}

// SaveResult contains metadata about a save operation
type SaveResult struct {
    RestartRequired bool     `json:"restart_required"`
    ChangedFields   []string `json:"changed_fields,omitempty"`
}
```

#### 2. Load Logic (Precedence Chain: env > file > defaults)

```go
// NewManager creates a new configuration manager
func NewManager() (*Manager, error) {
    configDir, err := os.UserConfigDir()
    if err != nil {
        return nil, err
    }
    filePath := filepath.Join(configDir, AppName, ConfigFileName)
    
    m := &Manager{filePath: filePath}
    if err := m.Load(); err != nil {
        return nil, err
    }
    return m, nil
}

// Load initializes configuration with proper precedence: env > file > defaults
func (m *Manager) Load() error {
    m.mu.Lock()
    defer m.mu.Unlock()

    // Step 1: Start with defaults
    cfg := Defaults

    // Step 2: Load from file if exists (file > defaults)
    if data, err := os.ReadFile(m.filePath); err == nil {
        if err := json.Unmarshal(data, &cfg); err != nil {
            // Corrupted file - backup and use defaults
            m.backupCorruptedFile()
            cfg = Defaults
        }
    }

    // Step 3: Apply env overrides (env > file > defaults)
    cfg = m.applyEnvOverrides(cfg)

    // Step 4: If no file exists, create one for user convenience
    if _, err := os.Stat(m.filePath); os.IsNotExist(err) {
        if err := m.saveToFile(cfg); err != nil {
            // Can't write file - continue in read-only mode
            m.readOnly = true
        }
    }

    m.config = cfg
    return nil
}

// applyEnvOverrides applies env vars on top of config (env wins always)
func (m *Manager) applyEnvOverrides(cfg Config) Config {
    if v := os.Getenv("UPSTREAM_URL"); v != "" {
        cfg.UpstreamURL = v
    }
    if v := os.Getenv("PORT"); v != "" {
        if port, err := strconv.Atoi(v); err == nil {
            cfg.Port = port
        }
    }
    if v := os.Getenv("IDLE_TIMEOUT"); v != "" {
        if d, err := time.ParseDuration(v); err == nil {
            cfg.IdleTimeout = Duration(d)
        }
    }
    if v := os.Getenv("MAX_GENERATION_TIME"); v != "" {
        if d, err := time.ParseDuration(v); err == nil {
            cfg.MaxGenerationTime = Duration(d)
        }
    }
    if v := os.Getenv("MAX_RETRIES"); v != "" {
        if r, err := strconv.Atoi(v); err == nil {
            cfg.MaxRetries = r
        }
    }
    return cfg
}

// backupCorruptedFile renames corrupted config for recovery
func (m *Manager) backupCorruptedFile() {
    backupPath := m.filePath + ".corrupted." + time.Now().Format("20060102-150405")
    os.Rename(m.filePath, backupPath)
}
```

#### 3. Save Logic (with validation, backup, and in-memory sync)

```go
// Save persists configuration to file and updates in-memory state
func (m *Manager) Save(cfg Config) (*SaveResult, error) {
    // Validate before any changes
    if err := cfg.Validate(); err != nil {
        return nil, fmt.Errorf("validation failed: %w", err)
    }

    m.mu.Lock()
    defer m.mu.Unlock()

    if m.readOnly {
        return nil, errors.New("config file is read-only (permission denied)")
    }

    // Detect changes that require restart
    result := &SaveResult{}
    if m.config.Port != cfg.Port {
        result.RestartRequired = true
        result.ChangedFields = append(result.ChangedFields, "port")
    }

    // Set metadata
    cfg.Version = ConfigVersion
    cfg.UpdatedAt = time.Now().Format(time.RFC3339)

    // Backup existing file before overwrite
    if _, err := os.Stat(m.filePath); err == nil {
        backupPath := m.filePath + ".bak"
        if err := os.Rename(m.filePath, backupPath); err != nil {
            // Non-fatal: log warning but continue
        }
    }

    if err := m.saveToFile(cfg); err != nil {
        return nil, err
    }

    // Re-apply env overrides to in-memory config (env always wins)
    m.config = m.applyEnvOverrides(cfg)
    
    // Publish config update event if event bus is wired
    if m.eventBus != nil {
        m.eventBus.Publish(events.Event{
            Type: "config.updated",
            Data: m.config,
        })
    }
    
    return result, nil
}

// saveToFile writes config to disk atomically
func (m *Manager) saveToFile(cfg Config) error {
    // Ensure directory exists
    dir := filepath.Dir(m.filePath)
    if err := os.MkdirAll(dir, 0755); err != nil {
        return err
    }

    data, err := json.MarshalIndent(cfg, "", "  ")
    if err != nil {
        return err
    }

    // Atomic write using temp file (avoids partial writes)
    tmpFile, err := os.CreateTemp(dir, "config-*.tmp")
    if err != nil {
        return err
    }
    tmpPath := tmpFile.Name()

    // Write and sync to disk
    if _, err := tmpFile.Write(data); err != nil {
        tmpFile.Close()
        os.Remove(tmpPath)
        return err
    }
    if err := tmpFile.Sync(); err != nil {
        tmpFile.Close()
        os.Remove(tmpPath)
        return err
    }
    tmpFile.Close()

    // Atomic rename
    return os.Rename(tmpPath, m.filePath)
}
```

#### 4. Thread-Safe Accessors

```go
// Get returns current configuration (thread-safe)
func (m *Manager) Get() Config {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.config
}

// GetUpstreamURL returns the upstream URL
func (m *Manager) GetUpstreamURL() string {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.config.UpstreamURL
}

// ... similar getters for other fields
```

---

## Integration Points

### 1. `cmd/main.go` Changes

**Before:**
```go
upstreamURL := getEnv("UPSTREAM_URL", "http://localhost:4001")
port := getEnvInt("PORT", 8089)
// ... scattered env reads
```

**After:**
```go
// Initialize config manager
configMgr, err := config.NewManager()
if err != nil {
    log.Fatalf("Failed to load config: %v", err)
}

cfg := configMgr.Get()
handler := &proxy.Handler{
    UpstreamURL:       cfg.UpstreamURL,
    Port:              cfg.Port,
    IdleTimeout:       cfg.IdleTimeout,
    MaxGenerationTime: cfg.MaxGenerationTime,
    MaxRetries:        cfg.MaxRetries,
    ConfigManager:     configMgr, // Pass manager for hot updates
}
```

### 2. `pkg/proxy/handler.go` Changes

**Important**: Consolidate to single config source. Remove the existing `Config` struct in handler and use `*config.Manager` directly.

**Before (current state with dual config structs):**
```go
type Config struct {
    mu                sync.RWMutex  // ❌ Duplicate mutex
    UpstreamURL       string
    IdleTimeout       time.Duration
    ...
}
```

**After (single source of truth):**
```go
import "github.com/yourorg/llm-supervisor-proxy/pkg/config"

type Handler struct {
    configMgr *config.Manager  // Single source of truth
    // ... existing fields (store, events, models, etc.)
}

// GetCurrentConfig returns the latest config (for each request)
func (h *Handler) GetCurrentConfig() config.Config {
    return h.configMgr.Get()
}

// Convenience methods that delegate to manager
func (h *Handler) GetUpstreamURL() string {
    return h.configMgr.Get().UpstreamURL
}

func (h *Handler) GetIdleTimeout() time.Duration {
    return h.configMgr.Get().IdleTimeout.Duration()
}
```

**Migration Note**: Remove `Clone()`, `CopyFrom()`, and the internal mutex from handler's old Config struct. All config access goes through `config.Manager`.

### 3. Web UI API Endpoints (`pkg/ui/server.go`)

Add endpoints for config management:

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/api/config` | GET | Get current configuration |
| `/api/config` | PUT | Update configuration |

```go
// GET /api/config
func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
    cfg := s.configMgr.Get()
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(cfg)
}

// PUT /api/config
func (s *Server) updateConfig(w http.ResponseWriter, r *http.Request) {
    var cfg config.Config
    if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
        http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
        return
    }
    
    // Clear read-only fields (prevent client manipulation)
    cfg.Version = ""  // Will be set by Save()
    cfg.UpdatedAt = "" // Will be set by Save()
    
    result, err := s.configMgr.Save(cfg)
    if err != nil {
        if strings.Contains(err.Error(), "validation failed") {
            http.Error(w, err.Error(), http.StatusBadRequest)
        } else if strings.Contains(err.Error(), "read-only") {
            http.Error(w, "Config file is read-only", http.StatusForbidden)
        } else {
            http.Error(w, fmt.Sprintf("Failed to save: %v", err), http.StatusInternalServerError)
        }
        return
    }
    
    // Include restart hint in response
    response := struct {
        config.Config
        RestartRequired bool     `json:"restart_required"`
        ChangedFields   []string `json:"changed_fields,omitempty"`
    }{
        Config:          s.configMgr.Get(),
        RestartRequired: result.RestartRequired,
        ChangedFields:   result.ChangedFields,
    }
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(response)
}
```

**Security Note**: These endpoints should be protected in production. Consider adding authentication or restricting to localhost only.

---

## Implementation Order

### Phase 1: Core Config Package
1. Create `pkg/config/config.go` with:
   - Config struct with JSON tags
   - Default values
   - Manager struct with mutex
   - Load/Save methods
   - Thread-safe getters

2. Create `pkg/config/config_test.go` with:
   - Test default values
   - Test env override
   - Test file load/save
   - Test precedence chain
   - Test concurrent access

### Phase 2: Integration
3. Update `cmd/main.go`:
   - Initialize config manager
   - Replace env reads with config manager
   - Pass manager to handlers

4. Update `pkg/proxy/handler.go`:
   - Accept config manager
   - Use config for all settings

5. Update `pkg/ui/server.go`:
   - Add config API endpoints
   - Wire up config manager

### Phase 3: Frontend (Optional)
6. Update Web UI:
   - Add settings page
   - Implement config form
   - Add save functionality

---

## Migration Path

### For Existing Users

1. **No config.json exists**: App creates one from defaults, env vars still override
2. **Using env vars only**: Config file created, but env vars continue to work (env wins)
3. **Docker/K8s deployment**: Set env vars to override file config at runtime
4. **Desktop users**: Edit config.json directly, changes persist across restarts

### Backward Compatibility

- All existing env vars continue to work and **take precedence** over file
- No breaking changes to existing behavior
- Config file is auto-generated, no manual setup needed
- Env vars can always override file settings (12-factor app compliant)

---

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Config file corrupted | Backup as `.corrupted.<timestamp>`, use defaults + env |
| Permission denied | Set `readOnly=true`, continue in-memory only, API returns 403 on save |
| Invalid JSON | Treat as corrupted file, backup and use defaults |
| Invalid duration format | Use default value for that field, log warning |
| Validation failed | Reject save, return 400 with specific error message |
| Save fails mid-write | Temp file cleaned up, original file unchanged |

---

## Testing Strategy

### Unit Tests
- Config load with/without file
- Env variable precedence
- Save and reload consistency
- Thread-safety under concurrent access
- Invalid input handling
- **Empty env var handling**: `UPSTREAM_URL=""` should not override
- **Restart detection**: Port change returns `restart_required: true`
- **Event publishing**: Save publishes event when bus is wired

### Integration Tests
- Full app startup with config
- Hot config reload (if implemented)
- API endpoint functionality
- **Request isolation**: In-flight requests see original config after API update

---

## Key Design Decisions (Oracle Review)

### 🔴 Critical Fixes Applied

| Issue | Original | Fixed |
|-------|----------|-------|
| **Precedence** | `file > env > defaults` | `env > file > defaults` |
| **Duration JSON** | `time.Duration` (breaks) | Custom `Duration` type with Marshal/Unmarshal |
| **Dual Config** | `pkg/config.Config` + `pkg/proxy.Config` | Single `config.Manager` only |
| **No Validation** | Accept any values | `Validate() error` method |

### 🟠 High Priority Fixes

| Issue | Solution |
|-------|----------|
| No backup on overwrite | Keep `.bak` file before save |
| No read-only handling | `readOnly` flag, API returns 403 |
| No validation on save | `Validate() error` with specific messages |
| API no error details | Return validation errors in response |

---

## Edge Cases & Implementation Details

### 1. Port Changes Require Server Restart

**Problem**: HTTP server can't rebind to a new port at runtime. Updating `port` via API succeeds but has no effect until restart.

**Solution**: Track which fields require restart and communicate to UI.

```go
// In config.go
var RestartRequiredFields = []string{"port"}

func (m *Manager) Save(cfg Config) (*SaveResult, error) {
    // ... validation and save logic
    
    // Detect restart-requiring changes
    restartRequired := m.config.Port != cfg.Port
    
    // ... save to file
    
    return &SaveResult{RestartRequired: restartRequired}, nil
}

type SaveResult struct {
    RestartRequired bool     `json:"restart_required"`
    ChangedFields   []string `json:"changed_fields,omitempty"`
}
```

**API Response**:
```json
{
  "version": "1.0",
  "upstream_url": "http://localhost:4001",
  "port": 8090,
  "restart_required": true,
  "changed_fields": ["port"]
}
```

### 2. In-Flight Request Consistency

**Problem**: Config changes mid-request could cause inconsistent behavior (e.g., switching `upstream_url` mid-stream).

**Solution**: Capture config at request start, pass by value through request lifecycle.

```go
// In pkg/proxy/handler.go
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Snapshot config at request start (by value, not pointer)
    cfg := h.configMgr.Get()
    
    // Pass cfg through the entire request lifecycle
    h.handleRequest(w, r, cfg)
}

func (h *Handler) handleRequest(w http.ResponseWriter, r *http.Request, cfg config.Config) {
    // All downstream code uses the captured cfg, never reads from manager again
    // This guarantees consistency for the duration of this request
}
```

**Note**: This is already naturally afforded by Go's value semantics. No special handling needed if implemented correctly.

### 3. Config Update Events

**Problem**: Other subsystems (WebSocket clients, background workers) need to know when config changes.

**Solution**: Publish `ConfigUpdated` event via existing event bus.

```go
// In config.go
type ConfigUpdatedEvent struct {
    Config      Config   `json:"config"`
    ChangedBy   string   `json:"changed_by"` // "api", "file", "env"
}

// Wire into Save()
func (m *Manager) Save(cfg Config) (*SaveResult, error) {
    // ... save logic
    
    // Publish event if eventBus is wired
    if m.eventBus != nil {
        m.eventBus.Publish(events.Event{
            Type: "config.updated",
            Data: ConfigUpdatedEvent{
                Config:    m.config,
                ChangedBy: "api",
            },
        })
    }
    
    return &SaveResult{...}, nil
}
```

**UI Integration**: WebSocket clients can subscribe to `config.updated` events for real-time updates.

### 4. Empty Env Vars Should Not Override

**Problem**: `UPSTREAM_URL=""` returns empty string, which would override valid file/default values.

**Solution**: Check for non-empty strings before applying.

```go
func (m *Manager) applyEnvOverrides(cfg Config) Config {
    // Only apply if env var exists AND is non-empty
    if v := os.Getenv("UPSTREAM_URL"); v != "" {
        cfg.UpstreamURL = v
    }
    if v := os.Getenv("PORT"); v != "" {
        if port, err := strconv.Atoi(v); err == nil && port > 0 {
            cfg.Port = port
        }
    }
    if v := os.Getenv("IDLE_TIMEOUT"); v != "" {
        if d, err := time.ParseDuration(v); err == nil && d > 0 {
            cfg.IdleTimeout = Duration(d)
        }
    }
    if v := os.Getenv("MAX_GENERATION_TIME"); v != "" {
        if d, err := time.ParseDuration(v); err == nil && d > 0 {
            cfg.MaxGenerationTime = Duration(d)
        }
    }
    if v := os.Getenv("MAX_RETRIES"); v != "" {
        if r, err := strconv.Atoi(v); err == nil && r >= 0 {
            cfg.MaxRetries = r
        }
    }
    return cfg
}
```

**Alternative**: Use `os.LookupEnv()` to distinguish between "not set" and "set to empty":

```go
if v, exists := os.LookupEnv("UPSTREAM_URL"); exists && v != "" {
    cfg.UpstreamURL = v
}
```

---

## Future Considerations

### Potential Enhancements
1. **Hot Reload**: Watch config file for changes
2. **Validation**: Schema validation before save
3. **Sensitive Values**: Mask secrets in API responses
4. **Config Profiles**: Multiple config files (dev/prod)
5. **Export/Import**: Config backup and restore

### Not in Scope (V1)
- Database-backed config
- Remote config server
- Encrypted config values
- Config versioning/history

---

## File Checklist

| File | Action | Description |
|------|--------|-------------|
| `pkg/config/config.go` | CREATE | Core configuration package |
| `pkg/config/config_test.go` | CREATE | Unit tests (incl. edge cases) |
| `cmd/main.go` | MODIFY | Use config manager |
| `pkg/proxy/handler.go` | MODIFY | Accept config manager, snapshot config at request start |
| `pkg/ui/server.go` | MODIFY | Add config API endpoints with restart hints |
| `pkg/events/bus.go` | MODIFY (optional) | Add `config.updated` event type |

---

## Summary

This design provides:
- ✅ **12-Factor App compliant**: Env vars always override file config
- ✅ **Custom Duration type**: Proper JSON serialization (`"10s"` not nanoseconds)
- ✅ **Single source of truth**: Consolidated config.Manager, no dual structs
- ✅ **Validation on save**: Invalid values rejected before persisting
- ✅ **Backup before overwrite**: `.bak` file preserved
- ✅ **Graceful degradation**: Works in read-only mode if permission denied
- ✅ **Thread-safe**: sync.RWMutex for concurrent access
- ✅ **Atomic writes**: Temp file + rename pattern
- ✅ **API for runtime config**: GET/PUT endpoints with validation
- ✅ **Backward compatible**: Existing env vars continue to work
- ✅ **Restart hints**: API returns `restart_required` flag for port changes
- ✅ **Request consistency**: Config snapshot at request start prevents mid-stream changes
- ✅ **Event publishing**: Config updates published to event bus for subscribers
- ✅ **Empty env handling**: Empty env vars don't override valid values
