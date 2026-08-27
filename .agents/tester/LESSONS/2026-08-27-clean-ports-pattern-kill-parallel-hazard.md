# LESSON: test_mock_clean_ports.sh pattern-kills — mock scripts are NEVER parallel-safe

**Date:** 2026-08-27 (discovered during §8 merge-gate recon for feature/ultimate-model-trigger-schedule)

## Root cause
`test/test_mock_clean_ports.sh` (sourced by test_mock_ultimate_model.sh, test_mock_minimax_reasoning.sh, and other mock scripts) implements `clean_ports` as:
1. `lsof -ti :<port> | xargs kill -9` for its two fixed ports, AND
2. `kill_go_processes`: `pgrep -f` + `kill -9` against the **process-name patterns** `mock_llm.go`, `mock_llm_race.go`, `mock_llm_loop.go`, `mock_llm_openai.go`, and **`cmd/main.go`** — unconditionally.

So any concurrently running proxy (`go run cmd/main.go`) or mock — regardless of port — gets SIGKILLed when any mock script starts or cleans up. Distinct port assignments do NOT make these scripts parallel-safe.

## Impact if ignored
Two mock scripts launched in parallel: the second script's `clean_ports` kills the first script's proxy mid-test → false FAILs / flakes that look like product bugs.

## Rule (adopted)
- Mock e2e scripts (`test/test_mock_*.sh` that source test_mock_clean_ports.sh) run **strictly sequentially** — one at a time per machine.
- Hand-built harnesses (boundary drives, UI validation) must NOT source test_mock_clean_ports.sh; clean up **by PID only** (this worked cleanly in the 2026-08-27 boundary+UI harness).
- For ad-hoc harnesses, prefer prebuilt binaries (`go build -o /tmp/... ./cmd/main.go`) over `go run` so pgrep-by-source-pattern can't match them.
- Never kill by pattern (`pgrep -f cmd/main.go`) — port 8088 ensemble-safety rule applies to any kill logic.

## Verified
Recon 2026-08-27: sequential waves (ultimate script → boundary/UI harness → minimax script) had zero cross-kills; all runs first-attempt green.
