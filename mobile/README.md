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
  profile.tsx           read-only peer profile modal (Message, Voice Call,
                        Video Call, Add Contact/Remove)
  call.tsx              the call screen — a full-screen modal that CallHost
                        opens when a call starts
src/
  session.tsx           Session context — one ApiClient + one ChatStore for the
                        app; bearer token in expo-secure-store (AsyncStorage on
                        web); AppState → store.setForeground; Android dev
                        remaps localhost → 10.0.2.2
  updates.ts            over-the-air updates (expo-updates): applies a
                        downloaded update when the app returns after 15
                        minutes away; the Settings footer's code label
  notify.ts             local notifications (expo-notifications); taps route
                        back to the conversation — the managed-Expo analogue of
                        the legacy Android MessagingService
  audio.ts              useVoiceRecorder (expo-audio) for dictation + voice notes
  media.ts              in-app media vs. calls: a call stops, then blocks, voice
                        messages, videos, and recording until it ends
  call/
    CallHost.tsx        calls layer around the root Stack: opens /call, plays
                        the call sounds, floats a return-to-call tile (on iOS
                        in a FullWindowOverlay while a modal sheet is open, so
                        it stays above the sheet)
    CallScreen.tsx      ringing (Decline / Accept) and in-call controls
                        (speaker, camera, mute, end, camera flip)
    platform.native.ts  react-native-webrtc behind the shared CallPlatform
                        (platform.ts: Expo-web stub, no calls)
    CallVideo.native.tsx  RTCView for one call stream
    callAudio.native.ts react-native-incall-manager: ringtone, ringback,
                        in-call audio routing, speaker toggle
    system.ios.ts       iOS: calls in the system call UI (modules/system-calls)
    system.android.ts   Android: the ring service (modules/call-ringer)
                        (system.ts: Expo-web stub)
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
    NativeMenu.tsx      menus: UIKit context menus + glass menu button on iOS;
                        ActionSheet, or a Material dropdown (the call button),
                        on Android
    SwiftUI.tsx, JetpackCompose.tsx  platform-split @expo/ui primitives
                        (real on iOS / Android respectively, stubs elsewhere)
    Sheet.tsx           shared bottom-sheet card pattern
    attachments.tsx     image/video/audio/GIF/file rendering
    ImageViewer.tsx     fullscreen image viewer
    CallEntry.tsx       finished-call card in the thread ("Call again")
    Avatar.tsx, Icon.tsx, Toasts.tsx
assets/icons/           Android vector drawables (the call dropdown's icons)
modules/                local Expo modules
  native-menu/          iOS (Swift): UIKit context menus, glass menu button,
                        image viewer, edge blur
  system-calls/         iOS (Swift): CallKit, or LiveCommunicationKit in
                        mainland China
  call-ringer/          Android (Kotlin): the ring service, so calls ring
                        while the app is closed
e2e/                    Maestro YAML flows + subflows/login.yaml
```

## Platform styling

Deliberately different per platform while sharing layout and palette:

- **iOS** — liquid-glass treatment via `expo-glass-effect` `GlassView` where
  supported (iOS 26+), with a graceful blur/tint fallback below.
- **Android** — flat, modern Material surfaces; bottom-sheet `Modal`s replace
  the platform `Alert` for action menus, and the DM header's call button
  opens a Material dropdown anchored to it.
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

## Updates and new app builds

Release builds download new JavaScript from the server by themselves
(expo-updates; root CLAUDE.md → **OTA updates**, DEPLOY.md → **Mobile app
updates**). Debug builds (`npm run ios`, `npm run android`) load it from
Metro and never use updates.

```bash
scripts/publish-update.sh        # the current JavaScript → every phone on this app version
scripts/publish-app.sh android   # release APK → /app (raise the version in app.json first)
scripts/publish-app.sh ios       # unsigned IPA → /app (raise the version in app.json first)
```

To try updates against a local server in the iOS Simulator (an Android
release build refuses plain-http URLs), set both
`AM_UPDATES_URL=http://localhost:8765/api/ota/manifest` and
`EXPO_PUBLIC_AM_SERVER=http://localhost:8765`, then run
`npx expo prebuild --platform ios --clean` and
`npx expo run:ios --configuration Release`, and publish with
`OTA_LOCAL=<repo root>/data scripts/publish-update.sh` (same two variables
set). Run `npx expo prebuild --platform ios --clean` again without them
afterwards: the update URL is written into `ios/` at prebuild.

## Calls

Voice and video calls in a DM run on iOS and Android; `npm run web` has no
calls. They use two native modules, `react-native-webrtc` and
`react-native-incall-manager`, plus native config from `app.json` (camera and
microphone permission text, and the two call sounds in `assets/sounds/`
copied into the app by the `expo-asset` plugin). After changing any of that,
regenerate the native projects and rebuild:

```bash
npx expo prebuild    # ios/ and android/ are generated (gitignored)
npm run ios          # or: npm run android
```

If `pod install` fails inside prebuild with a Unicode encoding error, run it
again with a UTF-8 locale: `cd ios && LANG=en_US.UTF-8 pod install`.

On iOS, calls ring and run in the system call UI (`modules/system-calls`):
CallKit, or LiveCommunicationKit (iOS 17.4+) when the iPhone's region is
mainland China, where the App Store doesn't allow CallKit. There is no VoIP
push (it needs a paid Apple Developer account), so an iPhone hears of a call
only while the app runs: in practice, while the app is open.

On Android, calls also ring while the app is closed (`modules/call-ringer`).
A foreground service keeps a small socket to the server (`/ws?mode=calls`)
and rings with a call notification (Decline and Answer, and a full-screen
call screen over the lock screen). While it runs, a silent "Ready for calls"
notification stays in the notification shade. Settings → Notifications →
**Ring when the app is closed** (default on) turns it off. Signing out stops
the service, and so does an app start that ends on the login screen (no
stored token, or the startup `GET /api/me` fails; any failure of that request,
a network error included, signs the user out). The service needs
notifications to be allowed; when notifications, full-screen calls, or the
battery-optimization exemption are off, the same page shows a button that
opens the matching system setting.

Both are local Expo modules: after pulling a change to either one, run
`npx expo prebuild` and rebuild the app.

Testing calls: calls in the iOS Simulator ring in the app's own call screen,
never in CallKit or LiveCommunicationKit (the Simulator has no system call
UI), so test those on a real iPhone; launch the app with the argument
`-AppleLocale en_CN` to get LiveCommunicationKit outside China. The iOS
Simulator has no camera (its self view stays black),
it sends silence instead of microphone sound (the peer receives the audio
packets, but every sample is zero), and a VPN app on the Mac that takes the
default route stops the Simulator's call media (calls end as "Couldn't
connect"). With the VPN off, the Simulator receives the peer's video and
audio; check an iPhone's own voice on a real iPhone. The Android emulator's
virtual camera works, and its calls connect.

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
