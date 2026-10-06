# @alexmessages/shared

Framework-agnostic TypeScript core for the Alex Messages clients. One package
supplies the wire types, REST/WebSocket plumbing, the chat state machine, and
display formatting so the **React web app** (`web/`) and the **React Native
app** (`mobile/`) behave identically by construction — logic lives here, not
in either UI.

There is no React dependency: `ChatStore` exposes a `version` counter that
bumps on every state change, which React binds with `useSyncExternalStore`
(both clients do this). Platform side effects — toasts, the reconnect banner,
local notifications — surface through `onToast` / `onNotify` hooks the host
wires up, never through the DOM.

## Modules

| File | Contents |
|------|----------|
| `types.ts` | Wire models mirroring the Go JSON **exactly** (snake_case): `PublicUser`, `Attachment`, `HistoryMessage`, `BroadcastMessage`, `DmStatePayload`/`DmState`, `InitPayload`, `ServerEvent`, `ClientMessage`, `MeResponse`, `LoginResponse`, `HistoryResponse`, `LinkPreview`. |
| `dm.ts` | `dm:<lo>:<hi>` helpers — `isDM`, `parseDMChannel`, `dmPeerOf`, `dmChannelFor`. Mirrors `db.DMChannelID`; keep byte-compatible. |
| `format.ts` | Display formatting ported from `static/app.js`: `PEER_COLORS` + `hash`/`colorFor`/`shade` (peer avatar colors — must match the web palette), `nameFor`/`handleFor`/`initialsFor`, `fmtTime`/`fmtSize`/`formatDate`, `attachmentKind`/`attachmentLabel`, `lastMessagePreviewFor`, `bodySegments`, `firstUrl`. |
| `api.ts` | `ApiClient` — REST client. Web auth = HttpOnly `am_session` cookie; native auth = `token` sent as `Authorization: Bearer` (from `/api/login`'s `session_token`). Non-2xx → `ApiError(status, detail)`. |
| `socket.ts` | `ChatSocket` — the `/ws` control plane. Sends are queued while down and flushed on reconnect; fixed 1.5 s retry with a delayed down notice; `?token=` auth for native; keepalive ping; injectable `WebSocketImpl` for tests. |
| `store.ts` | `ChatStore` — the state machine. Owns all chat state, reduces every `ServerEvent`, exposes the actions the UI calls (send/edit/reply/pin/read/contacts…). Drives optimistic send (Sending… → Delivered → Read) and the 10 s failed-send/Retry path. |
| `debug.ts` | `DebugReporter` — batches `Debug.*` events to `POST /api/debug/report` (same contract as the legacy clients). |
| `wav.ts` | `encodeWav` — PCM → 16-bit WAV so dictation clips need no server-side transcoding (Voxtral accepts wav/mp3). |

## Protocol contract

The wire shapes and DM-channel format mirror the Go backend
(`internal/webapp/`, `internal/db/`). Adding a field server-side is safe (the
clients ignore unknown keys); **renaming or retyping a field breaks the
protocol** — change `types.ts`, `api.ts`, and the server together. Peer avatar
colors and display formatting must match `static/app.js` exactly so a user
looks the same on web and mobile — change both in sync.

## Develop / test

```bash
npm install
npm run typecheck   # tsc --noEmit
npm test            # vitest run
npm run test:watch  # vitest (watch)
```

Tests (`shared/tests/`) cover the store reducers (optimistic send →
delivered → read, edits, replies, unread state), the socket's reconnect/queue
behavior, the API client's error mapping, the format helpers, and the WAV
encoder — the logic both UIs depend on.
