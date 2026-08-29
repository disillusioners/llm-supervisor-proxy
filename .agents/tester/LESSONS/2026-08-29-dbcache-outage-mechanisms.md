# 2026-08-29 — DB-cache-layer E2E harness lessons (inducing REAL SQLite outages on a live process)

## Which outage mechanisms actually work (modernc.org/sqlite v1.46.1, WAL, macOS)

| Mechanism | Live-process effect | Verdict |
|---|---|---|
| M1: rm db (+wal/shm) + mkdir db | Pool keeps serving reads from deleted inodes indefinitely; only NEW connections fail | ❌ Not an outage for reads — useless for read-resilience testing |
| M2: dd same-size garbage over db (conv=notrunc) + rm -wal/-shm | Real errors on live queries: `file is not a database (26)` / `database disk image is malformed (11)`; writers see `unable to open database file: out of memory (14)` | ✅ **Chosen** — the only mechanism that makes the live process's reads genuinely fail |
| M3: truncate -s 0 (db or -shm) | **SIGBUS process crash** — modernc mmaps the -shm; zeroing it faults the mapping | ❌ Destroys the process under test |
| M4: chmod 000 dir | Already-open fds unaffected; reads never fail | ❌ No effect |

## Recovery caveat (F7)

After M2 corruption, restoring a byte-identical backup does NOT heal the live process — it degrades to perpetual `malformed (11)` (mmap/pool state). Pack A restarts the proxy after restore and verifies reconciler recovery (59s ≤ 120s) post-restart. PG-class outages reconnect without restart, so criterion H's no-restart reading holds only for network dialects.

## Other harness facts

- macOS: Go ignores `XDG_CONFIG_HOME` on darwin — the SQLite DB lands under `$HOME/Library/Application Support/llm-supervisor-proxy/config.db`; discover via `find "$HOME/Library/Application Support" -name config.db`.
- External models route to the GLOBAL `UPSTREAM_URL` default (never model credentials) — so a deliberate external model in an outage test will legitimately hit whatever the global default is. Misroute detection must be per-hit model attribution (sentinel logs the requested model), not raw hit counts.
- Pack watchdogs: emit the final RESULT + exit code BEFORE slow teardown, and kill children by tracked PID with `kill -9` (no `lsof` sweeps in cleanup) — lsof-per-port sweeps in cleanup cost 1-3s each and can push a 219s-logic pack past its 280s watchdog, losing the exit code (fixed in a753698).
- Log-scrape strings that worked: `development default http://localhost:4001` (boot tripwire), `[WARN] Race attempt` (credential-miss analog of the incident WARN), `[WARN] [cache] reconciler: strict read failed` (health detection).
