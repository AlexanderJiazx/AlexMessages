# mobile — Alex Messages Expo client

The native iOS + Android client for Alex Messages: an Expo + React Native app
(Expo Router, file-based routes) that shares its wire types, API/WS plumbing,
state machine, and formatting with the React web app via
[`@alexmessages/shared`](../shared/README.md) (a `file:../shared` dependency).

## Layout

```
app/                    Expo Router routes
  _layout.tsx           root stack + ActionSheet host + theme
  index.tsx             splash/bootstrap → login or chat
  login.tsx             sign in / register / server-URL picker
  chat.tsx              DM list — home tab; two-pane rail+conversation when wide
  conversation.tsx      a single DM thread (pushed route on phones)
  settings.tsx          tabbed settings modal (Profile/Account/Notifications/Data/Admin)
  new-chat.tsx          start-a-DM modal
  profile.tsx           read-only peer profile modal
src/
  session.tsx           Session context — one ApiClient + one ChatStore for the
                        app; bearer token in expo-secure-store (AsyncStorage on
                        web); AppState → store.setForeground; Android dev
                        remaps localhost → 10.0.2.2
  notify.ts             local notifications (expo-notifications); taps route
                        back to the conversation — the managed-Expo analogue of
                        the legacy Android MessagingService
  audio.ts              useVoiceRecorder (expo-audio) for dictation + voice notes
  theme.ts              shared "paper + sage" tokens, the 720 wide breakpoint,
                        glassSupported (iOS ≥ 26)
  components/
    DMList.tsx          pinned/recent conversation rows + swipe/long-press actions
    ConversationView.tsx
    MessageList.tsx     inverted FlatList; keys on store.version so live
                        updates always re-render
    Bubble.tsx          bubble: reply refs, (edited) tag, media, long-press menu
    Composer.tsx        pill composer: text, attachments, dictate button
    DictationBar.tsx    ChatGPT-style dictation bar (waveform/timer/stop/send-audio/send)
    ActionSheet.tsx     iOS ActionSheetIOS / Android bottom-sheet Modal (any
                        option count — Alert.alert caps at 3 on Android)
    Sheet.tsx           shared bottom-sheet card pattern
    attachments.tsx     image/video/audio/GIF/file rendering
    ImageViewer.tsx     fullscreen image viewer
    Avatar.tsx, Icon.tsx, Toasts.tsx
e2e/                    Maestro YAML flows + subflows/login.yaml
```

## Platform styling

Deliberately different per platform while sharing layout and palette:

- **iOS** — liquid-glass treatment via `expo-glass-effect` `GlassView` where
  supported (iOS 26+), with a graceful blur/tint fallback below.
- **Android** — flat, modern Material surfaces; bottom-sheet `Modal`s replace
  the platform `Alert` for action menus.
- **Wide layouts** (iPad, tablets, `expo start --web`) — `chat` switches to a
  two-pane rail + conversation view above the `720` breakpoint.

## Server URL

Resolved in order: `EXPO_PUBLIC_AM_SERVER` → `app.json → extra.defaultServerUrl`
→ `https://messages.alexanderjia.com`. For a local backend on Android, point
at `localhost` — the session layer remaps it to `10.0.2.2` (the emulator's
host alias) automatically. Auth is the `session_token` from `/api/login`, sent
as `Authorization: Bearer` on REST calls and `?token=` on the `/ws` upgrade.

## Run

```bash
npm install
npm start            # Metro / Expo dev server
npm run ios          # build + launch on the iOS simulator
npm run android      # build + launch on an Android emulator
npm run web          # react-native-web build in the browser
```

For a device build against a local backend, start the Go server on `:8765`
first (`ADMIN_PASSWORD=… PORT=8765 go run ./cmd/server` from the repo root).

## Test

```bash
npx tsc --noEmit     # typecheck
npx maestro test e2e/                # all flows on a booted device
npx maestro test e2e/chat.yaml       # one flow
npx maestro test --udid emulator-5556 e2e/   # pick a specific device
```

Maestro flows: `chat`, `dictation`, `attach`, `new-chat`, `settings`,
`dm-actions`, `reply-edit`, `tour` — 8 total, green on both iOS and Android.
`e2e/subflows/login.yaml` signs in (alice / password123) against the dev
server; it accepts either `localhost:8765` or `10.0.2.2:8765`.

### Emulator gotchas (see also the root AGENTS.md)

- Set `JAVA_HOME` to a real JDK (Android Studio's JBR) before running Maestro.
- Keep **animations enabled** — "Reduce animations" triggers a Reanimated dev
  LogBox toast that covers the composer and eats taps.
- AVDs ship `hw.keyboard=yes` + `show_ime_with_hard_keyboard=0`, which hides
  the soft IME — `adb shell settings put secure show_ime_with_hard_keyboard 1`.
- `hideKeyboard` on Android ≈ a back press when no keyboard is open — gate it
  behind `visible: "return"` in flows.
- Modal screens must use `SafeAreaView edges={["top","bottom"]}` — without it
  the header renders under the Android status bar.
