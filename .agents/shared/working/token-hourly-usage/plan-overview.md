# Plan Overview: Token-level Hourly Request Counting

## Objective
Track and display the number of LLM API requests consumed per token, aggregated hourly. This enables token-level observability — knowing which tokens made how many requests during which hours.

## Scope Assessment
**LARGE** — Spans 3 modules (backend data extraction + storage, API, frontend UI). Requires extracting usage data from LLM responses (currently unextracted), creating a new database table, modifying the request flow to capture token IDs, new API endpoints, and new frontend components.

## Context
- **Project:** llm-supervisor-proxy
- **Working Directory:** /Users/nguyenminhkha/All/Code/opensource-projects/llm-supervisor-proxy
- **Stack:** Go backend (net/http) + Preact/TypeScript frontend (Vite + Tailwind)
- **Database:** SQLite (primary) + PostgreSQL (secondary), using `*sql.DB` directly with `QueryBuilder` dialect handling
- **Requested by:** Leader

## Current State Analysis

### What Exists
| Area | Current State |
|------|--------------|
| Token model | `auth_tokens` table: id, token_hash, name, expires_at, created_at, created_by |
| Authentication | `authenticate()` validates token but **discards** `*AuthToken` (returns only `bool`) |
| Request store | In-memory `RequestStore` with `RequestLog` — **no token_id field** |
| Usage data structures | `store.Usage` and `race_request.TokenUsage` exist but are **never populated** |
| Usage from upstream | `event.Response.Usage` available in internal streaming but **only injected into SSE, never stored** |
| Request finalization | **7 inline points** in `handler.go` — no shared finalize function |
| Database queries | `*sql.DB` directly with `QueryBuilder` for dialect differences (NOT sqlc) |
| UI server | Has `tokenStore` (→`*sql.DB`) but no direct `*database.Store` field |
| Frontend tokens tab | `TokenList.tsx` + `TokenForm.tsx` in Settings → Tokens tab |
| Frontend API | `useApi.ts` with `useTokens()` hook pattern |

### Key Gaps to Fill
1. **Usage extraction (CRITICAL):** Must extract `usage` from LLM responses — currently never done anywhere
2. `authenticate()` must return token identity (ID, name), not just bool
3. `RequestLog` needs a `TokenID` / `TokenName` field
4. New `token_hourly_usage` table for hourly aggregated data
5. Counter component that increments DB on request completion
6. Add `*database.Store` to UI Server for query access
7. New API endpoint: `GET /fe/api/usage`
8. New frontend Usage tab with hourly breakdown display

## Phase Index

| Phase | Name | Objective | Dependencies | Est. Tasks | Est. Time |
|-------|------|-----------|-------------|-----------|-----------|
| 1 | Usage Extraction & Backend Data Collection | Extract usage from LLM responses; modify auth flow to capture token ID; create DB table; hook counting into all 7 finalization points | None | 9 | 5-6h |
| 2 | Backend API Endpoints | Add `*database.Store` to UI server; add REST endpoints to query hourly usage data per token | Phase 1 | 5 | 2-3h |
| 3 | Frontend Visualization | New Usage tab with token selector, hourly breakdown table/chart, date range picker | Phase 2 | 7 | 3-4h |

## Risks & Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|-----------|------------|
| Usage extraction may differ across LLM providers (OpenAI vs Anthropic format) | High | Medium | Focus on OpenAI-compatible format first; Anthropic usage is structurally similar; test both |
| 7 finalization points = 7 places to add counting logic | Medium | Low | Extract a shared `finalizeRequest(rc)` helper to avoid 7 copy-paste blocks |
| SQLite write contention on UPSERT under high load | Medium | Low | SQLite WAL mode handles concurrent reads; use `ExecContext` with timeout |
| `authenticate()` signature change breaks existing callers | High | Low | Change to return `(*auth.AuthToken, bool)` — only 1 caller exists |
| Token usage data not populated (upstream doesn't return usage) | Low | Medium | Gracefully handle nil usage — still count requests, tokens = 0 |
| Migration compatibility (SQLite vs PostgreSQL) | Medium | Low | Follow existing dual-migration pattern with dialect-aware queries |

## Success Criteria
- [ ] Usage data (prompt_tokens, completion_tokens, total_tokens) extracted from both streaming and non-streaming LLM responses
- [ ] Every finalized request increments the correct token's hourly counter in the database
- [ ] All 7 finalization paths in handler.go include counting (no missed paths)
- [ ] Unauthenticated requests (when token store is disabled) are not counted
- [ ] API endpoint returns hourly usage for any token with configurable time range
- [ ] Frontend displays hourly usage in a clear, filterable table
- [ ] Existing functionality (proxy, token CRUD, settings) is unaffected
- [ ] Migrations and queries work for both SQLite and PostgreSQL

## Tracking
- **Created:** 2026-03-31
- **Last Updated:** 2026-03-31 (Revised — addressed 6 critical review issues)
- **Status:** draft
