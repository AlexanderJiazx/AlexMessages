# Investigation — problems & threats (FreshRefactor)

A read-through of the whole codebase ahead of the refactor. Items are grouped by
severity. Each is marked **Fixed** (addressed on this branch), **Recommended**
(left as a deliberate follow-up, usually because it needs a production decision),
or **Noted** (intentional, documented so it isn't mistaken for a bug).

## Security

1. **WebSocket origin is not enforced** — *Recommended.*
   Both `/ws` upgraders use `CheckOrigin: return true`
   (`internal/webapp/ws.go`, `internal/meet/ws.go`). A malicious page could open
   an authenticated socket with the visitor's cookie (cross-site WebSocket
   hijacking). `SameSite=Lax` blunts it, but production should pin an allowlist
   of expected origins. Left unchanged to preserve the documented Starlette
   parity; flagged here so it's a conscious choice.

2. **Session cookies are not `Secure`** — *Recommended.*
   `httpx.SetSessionCookie` sets `secure=false` so localhost dev over HTTP works.
   In production (always HTTPS) the flag should be set. Best fixed with an env
   gate (e.g. `COOKIE_SECURE=1`) rather than hardcoding, since the dev story
   needs HTTP.

3. **VolcEngine RTC credentials are baked into source** — *Noted / Recommended.*
   `volcAppID`/`volcAppKey` in `internal/meet/routes.go` ship real defaults.
   They are overridable via `VOLC_RTC_APP_ID`/`VOLC_RTC_APP_KEY`, but the
   in-repo key is effectively public. Rotate it and require the env vars in
   production.

4. **Admin login has no rate limiting** — *Recommended.*
   Brute force is slowed by scrypt and by the admin panel binding to
   `127.0.0.1` (reached only through the reverse proxy), but a proxy-level
   throttle / fail2ban is worth adding.

5. **Debug-report tunnel could be abused** — *Fixed.*
   The new `/api/debug/report` ingestion is defended by a per-IP token bucket,
   a 64 KB body cap, a 50-event batch cap, per-field length limits, and a level
   whitelist; the actor identity is resolved server-side from the session
   cookie (never trusted from the body). See `internal/debuglog`.

6. **Link-preview SSRF** — *Noted (already mitigated).*
   `internal/webapp/linkpreview.go` uses an SSRF-guarded dialer and a TTL cache.
   Kept as-is; it remains a sensitive surface, so the guard must stay.

## Robustness / stability

7. **Servers had no timeouts or graceful shutdown** — *Fixed.*
   All three binaries used `engine.Run()` (bare `ListenAndServe`, no timeouts).
   They now go through `httpx.Serve`, which adds `ReadHeaderTimeout` and
   `IdleTimeout` (deliberately *no* `WriteTimeout`, which would kill the WS
   control plane and the SSE debug stream) and drains in-flight requests on
   SIGINT/SIGTERM.

8. **Expired sessions were only pruned lazily** — *Fixed.*
   Sessions were deleted only when next accessed. A background sweep
   (`db.StartBackgroundMaintenance`) now prunes expired sessions and caps the
   debug ring buffer every 10 minutes.

9. **No shared, centralized observability** — *Fixed.*
   The only debug console was meet-local and in-memory. It is replaced by a
   DB-backed console in the admin panel that both client apps and the servers
   feed (`debug_events` table + `internal/debuglog` + admin `/api/debug/*`).

## Dead / legacy code

10. **`calls` table helpers are unused** — *Noted.*
    `db.InsertCall`, `db.ListRecentCalls`, and the `db.Call` type have no
    callers — leftovers from the retired 1:1 call feature. The table itself is
    intentionally retained (CLAUDE.md) for data continuity, so the helpers are
    left in place but should be considered removable.

11. **`db.SortedKeys` is unused** — *Noted.* Safe to delete in a later pass.

12. **Historical spec docs lived at the repo root** — *Fixed.*
    `AlexMeetRefine.md`, `BugReportFromHumanTesting.md` (both already
    implemented) and the now-superseded `meet_debug.html` were moved to
    `Legacy/`.

## Naming / clarity

13. **Legacy "voicecall" / "AlexMessage" naming** — *Fixed.*
    `cmd/voicecall` → `cmd/meet`, `internal/voicecall` → `internal/meet`
    (package `meet`), the `vcServer`/`vcUser` types → `meetServer`/`meetUser`,
    log prefixes → `[alex-messages]` / `[alex-meet]` / `[alex-admin]`, and
    user-facing "AlexMessage" comments → "Alex Messages". The Go module name
    (`alexmessage`) and the SQLite filename (`alexmessage.db`) are intentionally
    unchanged to avoid breaking imports and production data continuity.
