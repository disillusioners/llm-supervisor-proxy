# Phase 4: Tests & Integration

## Objective
Write comprehensive tests to verify the secondary upstream model feature works correctly in the race coordinator/executor flow, and that existing behavior is unchanged when the field is not configured.

## Coupling
- **Depends on**: Phase 2 (retry logic changes)
- **Coupling type**: tight — tests the code from Phase 2
- **Shared files with other phases**: Tests use the same race_coordinator, race_executor, and models infrastructure
- **Shared APIs/interfaces**: None (tests are consumers, not providers)

## Context
Phases 1-3 delivered the complete feature. Now we need tests to ensure:
1. Secondary model is used for `modelTypeSecond` spawns
2. Primary model is still used for `modelTypeMain`
3. Fallback model is still used for `modelTypeFallback`
4. When secondary is empty, current behavior (same model) is preserved
5. Non-internal models are unaffected
6. External upstream models are unaffected

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Unit test: ResolveInternalConfig with secondary | Test that when secondary is set, the executor uses it for second-type requests | `pkg/proxy/race_executor_test.go` (new or extend) |
| 2 | Unit test: upstreamRequest flag | Test SetUseSecondaryUpstream/UseSecondaryUpstream methods | `pkg/proxy/race_request_test.go` |
| 3 | Integration test: Race with secondary model | Create mock model with SecondaryUpstreamModel, run race coordinator, verify second request uses secondary | `pkg/proxy/race_retry_test.go` (extend) |
| 4 | Integration test: Race without secondary model | Ensure existing race behavior is unchanged when SecondaryUpstreamModel is empty | `pkg/proxy/race_retry_test.go` |
| 5 | Unit test: Model validation | Test validation of secondary_upstream_model (valid when internal=true, invalid when internal=false) | `pkg/models/config_test.go` (extend) |
| 6 | Unit test: Store CRUD with secondary | Test AddModel, UpdateModel, GetModel with secondary_upstream_model field | `pkg/store/database/` test files |
| 7 | Unit test: API handler round-trip | Test GET/POST/PUT handlers in server.go for `secondary_upstream_model` field serialization, deserialization, and validation | `pkg/ui/server.go` test files |
| 8 | Build verification | Ensure all Go tests pass, frontend builds | Root |

## Key Files
- `pkg/proxy/race_executor_test.go` — Executor tests
- `pkg/proxy/race_request_test.go` — Request state tests
- `pkg/proxy/race_retry_test.go` — Retry integration tests
- `pkg/models/config_test.go` — Model config validation tests

## Detailed Implementation Notes

### Task 1: Executor test
```go
func TestExecuteInternalRequest_WithSecondaryUpstreamModel(t *testing.T) {
    // Setup mock model config with:
    //   InternalModel: "glm-5.0"
    //   SecondaryUpstreamModel: "glm-4.7-flash"
    //   Internal: true
    
    // Create upstreamRequest with useSecondaryUpstream = true
    // Execute request
    // Verify that the mock provider received "glm-4.7-flash" as model name
}

func TestExecuteInternalRequest_WithoutSecondaryUpstreamModel(t *testing.T) {
    // Setup mock model config with:
    //   InternalModel: "glm-5.0"
    //   SecondaryUpstreamModel: ""  (empty)
    //   Internal: true
    
    // Create upstreamRequest with useSecondaryUpstream = true
    // Execute request
    // Verify that the mock provider received "glm-5.0" (fallback to primary)
}
```

### Task 2: Flag test
```go
func TestUpstreamRequest_SecondaryUpstreamFlag(t *testing.T) {
    req := newUpstreamRequest(0, modelTypeMain, "test-model", 1024)
    
    // Default is false
    assert.False(t, req.UseSecondaryUpstream())
    
    // Set to true
    req.SetUseSecondaryUpstream(true)
    assert.True(t, req.UseSecondaryUpstream())
    
    // Set back to false
    req.SetUseSecondaryUpstream(false)
    assert.False(t, req.UseSecondaryUpstream())
}
```

### Task 3: Race coordinator integration test
Create a mock that tracks which model names were actually sent to the provider:
```go
func TestRaceCoordinator_SecondaryUpstreamOnIdle(t *testing.T) {
    // Setup:
    //   - Model "test-model" with InternalModel="glm-5.0", SecondaryUpstreamModel="glm-4.7-flash"
    //   - Very short idle timeout
    //   - Mock provider that delays response
    
    // Execute:
    //   - Send request
    //   - Wait for coordinator to spawn second request
    
    // Verify:
    //   - Main request sent "glm-5.0"
    //   - Second request sent "glm-4.7-flash"
    //   - Fallback (if any) sent its own model
}
```

### Task 5: Validation tests
```go
func TestValidate_SecondaryUpstreamModel(t *testing.T) {
    // Valid: internal=true, secondary set
    model := ModelConfig{
        ID: "test", Name: "Test", Internal: true,
        CredentialID: "cred1", InternalModel: "glm-5.0",
        SecondaryUpstreamModel: "glm-4.7-flash",
    }
    // Should pass validation
    
    // Invalid: internal=false, secondary set
    model.Internal = false
    // Should fail: "secondary_upstream_model requires internal to be true"
    
    // Valid: internal=true, secondary empty
    model.Internal = true
    model.SecondaryUpstreamModel = ""
    // Should pass (field is optional)
}
```

## Test Matrix

| Scenario | Internal | SecondaryUpstream | Expected Behavior |
|----------|----------|-------------------|-------------------|
| Primary model only | true | "" | Retry uses same model (unchanged) |
| With secondary | true | "glm-4.7-flash" | Retry uses secondary |
| Non-internal model | false | "" | No retry change (unchanged) |
| Non-internal with secondary | false | "glm-4.7-flash" | Invalid config (validation fails) |
| External upstream | N/A | "" | Not applicable (external path) |
| Fallback model | true | "" | Fallback uses its own config (unchanged) |
| Fallback model with secondary | true | "backup-flash" | Fallback uses its own config (secondary only applies to second-type, not fallback) |
| Peak hour active + secondary | true | "glm-4-flash" | Main uses peak hour model, retry uses secondary (unchanged by peak hour per Decision #4) |

### Task 7: API handler round-trip test
```go
func TestModelAPI_SecondaryUpstreamModel(t *testing.T) {
    // Test GET returns secondary_upstream_model field
    // Test POST creates model with secondary_upstream_model
    // Test PUT updates secondary_upstream_model
    // Test POST/PUT rejects secondary_upstream_model when internal=false
    // Test POST/PUT accepts secondary_upstream_model when internal=true
    // Test POST/PUT accepts empty secondary_upstream_model (optional field)
}
```

## Constraints
- All existing tests must continue to pass
- New tests must use existing mock infrastructure (test/mock_llm_*.go)
- Tests must cover both SQLite and PostgreSQL paths (or use dialect-agnostic patterns)

## Deliverables
- [ ] Unit tests for upstreamRequest flag
- [ ] Unit tests for executor with secondary model
- [ ] Integration test for race coordinator with secondary model
- [ ] Validation tests for ModelConfig
- [ ] Store CRUD tests with new field
- [ ] API handler round-trip tests for GET/POST/PUT with secondary_upstream_model
- [ ] All tests pass (go test ./... and frontend build)
