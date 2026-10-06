# web — Alex Messages React client

The React web client for Alex Messages: a Vite + React 18 + TypeScript SPA
that is a full rewrite of the legacy `static/` vanilla-JS frontend. It shares
its wire types, API/WS plumbing, state machine, and formatting with the React
Native app via [`@alexmessages/shared`](../shared/README.md).

## Layout

```
src/
  client.ts            app singletons: ApiClient + ChatStore + toast; visibility →
                       store.setForeground; 401 → /login
  push.ts              Web Push lifecycle — SW registers eagerly, subscribe() is
                       gated behind a user gesture (prompt modal or Settings toggle)
  audio/recorder.ts    WebAudio PCM capture → shared encodeWav (dictation)
  screens/             LoginScreen, ChatScreen
  components/
    Rail.tsx           DM list (pinned/recent) + bottom-left account trigger
    Stream.tsx         message list (inverted scroll, lazy history paging)
    Topbar.tsx         peer name + online-status chip
    Composer.tsx       pill composer: text, attachments, dictate button
    DictationBar.tsx   ChatGPT-style dictation bar (waveform/timer/stop/send-audio/send)
    MessageBubble.tsx  bubble: reply refs, (edited) tag, link previews, attachments
    SettingsModal.tsx  tabbed settings (Profile/Account/Notifications/Data/Admin)
    Modals.tsx         sheets/dialogs (peer profile, new chat, image viewer…)
    Overlays.tsx       toasts, reconnect banner, push-permission prompt
    Avatar.tsx, icons.tsx
  hooks.ts             useSyncExternalStore bindings to the shared store
  format-ui.tsx        React-side formatting glue
e2e/                   Playwright suite + start-server.sh + seed/ (Go seeder)
```

`App.tsx` routes between the login and chat screens; `main.tsx` mounts the
root and registers the service worker.

## Develop

Requires the Go backend running on `:8765` — `vite.config.ts` proxies `/api`,
`/ws`, `/uploads`, `/avatars`, `/static`, and `/fonts` to `localhost:8765`.

```bash
npm install
npm run dev        # vite on :5173, proxied to the Go server
```

## Build

```bash
npm run build      # tsc --noEmit && vite build → web/dist
```

When `web/dist/index.html` exists, the Go server serves this SPA for `/` and
`/login` (and mounts `web/dist/assets` at `/assets`); otherwise it falls back
to the legacy `static/` shells — so a fresh clone still runs without Node.

## Test

```bash
npm test           # vitest (component/store-adjacent unit tests)
npm run test:e2e   # playwright — 41 tests across 7 specs
```

The Playwright suite is hermetic: `e2e/start-server.sh` builds a dedicated
`cmd/server` on `:8795` against a **scratch SQLite database** seeded by
`e2e/seed` (a small Go program), so it never touches dev data. Chromium is
launched with fake-media flags so the dictation specs exercise the real
WebAudio capture path rather than a stub.

Specs: `auth` (login/register/logout), `chat` (send/reply/edit/read receipts),
`dm-list` (pin/unread/new-chat), `settings` (tabs, password change, export),
`media` (image/video/audio/file attachments + viewer), `dictation` (bar +
transcribe paths), `responsive` (phone vs desktop viewports).
