# web — Alex Messages React client

The React web client for Alex Messages: a Vite + React 18 + TypeScript SPA
that is a full rewrite of the legacy `static/` vanilla-JS frontend. It shares
its wire types, API/WS plumbing, state machine, and formatting with the React
Native app via [`@alexmessages/shared`](../shared/README.md).

## Layout

```
src/
  client.ts            app singletons: ApiClient + ChatStore + toast; visibility →
                       store.setForeground; 401 → /login; closes a ringing call's
                       Web Push notification once the call is answered or declined
  push.ts              Web Push lifecycle — SW registers eagerly, subscribe() is
                       gated behind a user gesture (prompt modal or Settings toggle)
  audio/recorder.ts    WebAudio PCM capture → shared encodeWav (dictation)
  call-platform.ts     the browser's WebRTC behind the shared CallPlatform; picks
                       the camera (front/back by facing mode on phones, the next
                       webcam by deviceId on desktops)
  screens/             LoginScreen, ChatScreen
  components/
    Rail.tsx           DM list (pinned/recent) + bottom-left account trigger
    Stream.tsx         message list (inverted scroll, lazy history paging)
    Topbar.tsx         peer name + online-status chip + voice/video call buttons
    CallView.tsx       incoming-call card, call view (peer video, self view,
                       mute/camera/switch/end), floating mini window (below any
                       open dialog), ringtone
    CallEntry.tsx      finished-call card in the thread ("Call again")
    Composer.tsx       pill composer: text, attachments, dictate button
    DictationBar.tsx   ChatGPT-style dictation bar (waveform/timer/stop/send-audio/send)
    MessageBubble.tsx  bubble: reply refs, (edited) tag, link previews, attachments
    SettingsModal.tsx  tabbed settings (Profile/Account/Notifications/Data/Admin)
    Modals.tsx         sheets/dialogs (peer profile with message/call/contact
                       actions, new chat, image viewer…)
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
npm run test:e2e   # playwright — 49 tests across 8 specs
```

The web client has no Vitest unit tests of its own (the shared logic is
unit-tested in `shared/`). `npm test` runs `vitest run`, which finds no unit
tests and fails on the Playwright specs in `e2e/`; do not use it.

The Playwright suite runs against its own server: `e2e/start-server.sh`
builds a dedicated `cmd/server` on `:8795` against its own SQLite database in
`$E2E_RUN_DIR` (default `/tmp/am-e2e`), seeded by `e2e/seed` (a small Go
program), so it never touches dev data. That database is NOT recreated for
each run: the seed adds each e2e user only when missing, adds the two
`e2e_alice`–`e2e_bob` seed messages only when that thread is empty, and
resets the seeded threads' pin and read state, so messages from earlier runs
remain. Delete the run dir's `data/` for an empty database. Chromium is
launched with fake-media flags so the dictation specs exercise the real
WebAudio capture path rather than a stub. Two `calls` tests launch a browser of their own:
one with two fake cameras, and one on full Chromium (channel `chromium`),
because the default headless shell denies notifications. Both browsers come
from `npx playwright install chromium`.

Specs: `auth` (login/register/logout), `chat` (send/reply/edit/read receipts),
`dm-list` (pin/unread/new-chat), `settings` (tabs, password change, export),
`media` (image/video/audio/file attachments + viewer), `dictation` (bar +
transcribe paths), `responsive` (phone vs desktop viewports), `calls` (a
video call between two browser contexts with Chromium's fake camera and
microphone: ring, answer, media both ways, mute/camera, minimize, hang up,
thread entries; a declined call; a cancelled call that shows as missed; a
call to someone with no device online; calls placed from the peer profile
sheet, whose call buttons are disabled while a call is live; Switch camera
moving between two webcams that report no facing mode; logging out during a
call, which ends it at once with no leave-page prompt; a ringing call's
notification closing when the call is answered here, declined here, or
answered on another device, and staying for a missed call).
