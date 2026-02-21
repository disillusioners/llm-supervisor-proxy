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
config.json (if exists) > Environment Variables > Hardcoded Defaults
```

**Rationale**: 
- File-based config allows persistent user changes
- Env vars are for container/deployment scenarios
- Defaults ensure the app always has valid config

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
    "sync"
    "time"
)

const (
    AppName          = "llm-supervisor-proxy"
    ConfigFileName   = "config.json"
    ConfigVersion    = "1.0"
)

// Config holds all application configuration
type Config struct {
    Version           string        `json:"version"`
    UpstreamURL       string        `json:"upstream_url"`
    Port              int           `json:"port"`
    IdleTimeout       time.Duration `json:"idle_timeout"`
    MaxGenerationTime time.Duration `json:"max_generation_time"`
    MaxRetries        int           `json:"max_retries"`
    UpdatedAt         time.Time     `json:"updated_at"`
}

// Defaults - used when env not set and file doesn't exist
var Defaults = Config{
    Version:           ConfigVersion,
    UpstreamURL:       "http://localhost:4001",
    Port:              8089,
    IdleTimeout:       10 * time.Second,
    MaxGenerationTime: 180 * time.Second,
    MaxRetries:        1,
}

// Manager handles configuration lifecycle
type Manager struct {
    mu       sync.RWMutex
    config   Config
    filePath string
}
```

#### 2. Load Logic (Precedence Chain)

```go
// Load initializes configuration with proper precedence
func (m *Manager) Load() error {
    m.mu.Lock()
    defer m.mu.Unlock()

    // Step 1: Start with defaults
    cfg := Defaults

    // Step 2: Check if config file exists
    if _, err := os.Stat(m.filePath); err == nil {
        // File exists - load from file (ignore env)
        data, err := os.ReadFile(m.filePath)
        if err != nil {
            return err
        }
        if err := json.Unmarshal(data, &cfg); err != nil {
            return err
        }
    } else if os.IsNotExist(err) {
        // File doesn't exist - use env vars, then create file
        cfg = m.applyEnvOverrides(cfg)
        cfg.UpdatedAt = time.Now()
        if err := m.saveToFile(cfg); err != nil {
            return err
        }
    } else {
        return err
    }

    m.config = cfg
    return nil
}

// applyEnvOverrides applies env vars on top of defaults
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
            cfg.IdleTimeout = d
        }
    }
    if v := os.Getenv("MAX_GENERATION_TIME"); v != "" {
        if d, err := time.ParseDuration(v); err == nil {
            cfg.MaxGenerationTime = d
        }
    }
    if v := os.Getenv("MAX_RETRIES"); v != "" {
        if r, err := strconv.Atoi(v); err == nil {
            cfg.MaxRetries = r
        }
    }
    return cfg
}
```

#### 3. Save Logic (with in-memory sync)

```go
// Save persists configuration to file and updates in-memory state
func (m *Manager) Save(cfg Config) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    cfg.Version = ConfigVersion
    cfg.UpdatedAt = time.Now()

    if err := m.saveToFile(cfg); err != nil {
        return err
    }

    m.config = cfg
    return nil
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

    // Atomic write: write to temp file, then rename
    tmpPath := m.filePath + ".tmp"
    if err := os.WriteFile(tmpPath, data, 0644); err != nil {
        return err
    }

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

```go
type Config struct {
    UpstreamURL       string
    Port              int
    IdleTimeout       time.Duration
    MaxGenerationTime time.Duration
    MaxRetries        int
}

type Handler struct {
    config       Config
    configMgr    *config.Manager  // NEW: for dynamic updates
    // ... existing fields
}

// GetCurrentConfig returns the latest config (for hot reload)
func (h *Handler) GetCurrentConfig() config.Config {
    return h.configMgr.Get()
}
```

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
    json.NewEncoder(w).Encode(cfg)
}

// PUT /api/config
func (s *Server) updateConfig(w http.ResponseWriter, r *http.Request) {
    var cfg config.Config
    if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    if err := s.configMgr.Save(cfg); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    json.NewEncoder(w).Encode(cfg)
}
```

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

1. **No config.json exists**: App creates one from env/defaults
2. **Using env vars only**: First run creates config.json, subsequent runs use file
3. **Want to switch back to env**: Delete config.json

### Backward Compatibility

- All existing env vars continue to work on first run
- No breaking changes to existing behavior
- Config file is auto-generated, no manual setup needed

---

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Config file corrupted | Log error, use defaults, rewrite file |
| Permission denied | Log error, use env/defaults, continue in-memory only |
| Invalid JSON | Log error with details, use defaults |
| Invalid duration format | Use default value for that field |

---

## Testing Strategy

### Unit Tests
- Config load with/without file
- Env variable precedence
- Save and reload consistency
- Thread-safety under concurrent access
- Invalid input handling

### Integration Tests
- Full app startup with config
- Hot config reload (if implemented)
- API endpoint functionality

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
| `pkg/config/config_test.go` | CREATE | Unit tests |
| `cmd/main.go` | MODIFY | Use config manager |
| `pkg/proxy/handler.go` | MODIFY | Accept config manager |
| `pkg/ui/server.go` | MODIFY | Add config API endpoints |

---

## Summary

This design provides:
- ✅ Single source of truth (config.json when exists)
- ✅ Graceful migration from env-only setup
- ✅ Thread-safe in-memory updates
- ✅ Atomic file writes
- ✅ Clear precedence chain
- ✅ API for runtime configuration
- ✅ Backward compatible
