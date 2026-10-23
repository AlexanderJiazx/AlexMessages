# Expo HAS CHANGED

Read the exact versioned docs at https://docs.expo.dev/versions/v57.0.0/ before writing any code.

## Communicating with the user (mandatory)

**THE MOST IMPORTANT RULE: NO AMBIGUITY AT ALL.** Every word and every
sentence must have exactly one possible meaning. If a sentence could be read
two ways, rewrite it before sending.

- **Lead with the direct answer to "does it work right now?"** Say "X works now"
  or "X does NOT work yet" before any explanation.
- **Never call something done, ready, or working when it still needs a step.**
  Example of what NOT to write: "the workflow can deploy" when deploys are
  switched off. Write: "Pushing does NOT deploy yet. It only runs tests."
- **Separate what is done from what is not done**, under explicit labels.
- **List every remaining step as a numbered action** that says who does it (you
  or the user), where exactly, and the exact names and values to use.
- **No vague words** ("set up", "wired", "should work", "ready", "in place")
  without saying exactly what happens and what does not.
- **Never contradict an earlier sentence.** Reread the reply before sending it.

## Project notes

- Shared logic lives in `@alexmessages/shared` (`../shared`, a `file:` dep) —
  wire types, `ApiClient`, `ChatSocket`, `ChatStore`, formatting. Keep
  platform code here thin; fix bugs in the shared store, not the view.
- `src/session.tsx` owns the singleton `ApiClient` + `ChatStore`. Auth is a
  bearer token in `expo-secure-store`; the server URL comes from
  `EXPO_PUBLIC_AM_SERVER` or `app.json → extra.defaultServerUrl`. Android dev
  builds remap `localhost` → `10.0.2.2`.
- Platform styling is deliberate: liquid glass on iOS 26+
  (`expo-glass-effect`), flat Material on Android. Same layout, same sage
  palette (`src/theme.ts`) — don't flatten the distinction.
- Wide layouts (iPad / `npm run web`) switch `app/chat.tsx` to a two-pane
  rail + conversation above the `720` breakpoint.
- Modal screens **must** wrap their root in `SafeAreaView edges={["top","bottom"]}`
  — without it the header renders under the Android status bar (edge-to-edge)
  and the close control is unreachable to users and UiAutomator. The one
  exception is the call screen, which pads with `useSafeAreaInsets()`
  instead (see **Calls** below).
- Menus go through `src/components/NativeMenu.tsx`: `LongPressMenu` (the
  iMessage-style system context menu — the view lifts out beside its menu),
  `GlassMenuButton` (a native glass button whose menu morphs out of it, e.g.
  the composer "+") and `TapMenu` (any RN view as a pull-down trigger). On
  iOS the first two are UIKit views from the local Expo module
  `modules/native-menu` (`UIContextMenuInteraction` attached straight to the
  RN child; a `UIButton` with `.glass()` and `showsMenuAsPrimaryAction`);
  `TapMenu` is an `@expo/ui/swift-ui` `Menu` on iOS 26. Android and older iOS
  fall back to `src/components/ActionSheet.tsx` (bottom-sheet `Modal`). Do
  **not** use `Alert.alert` for menus — Android caps it at 3 buttons and drops
  Cancel (alerts are fine for confirmations).
- A top-bar menu on Android is a Material dropdown anchored to its button,
  not the bottom sheet: `GlassMenuButton` with `dropdown` (used by the DM
  header's call button; the composer "+" keeps the sheet). It is the Jetpack
  Compose `DropdownMenu` from `@expo/ui/jetpack-compose` with the React
  Native button hosted as its trigger (`RNHostView`). Import Compose
  primitives from `src/components/JetpackCompose.tsx` (the Android file
  re-exports `@expo/ui/jetpack-compose`; the iOS/web file is a typed stub),
  mirroring `SwiftUI.tsx` on iOS. A dropdown item's leading icon
  (`MenuAction.androidIcon`) is an Android vector drawable required from
  `assets/icons/*.xml`: Expo UI's `Icon` renders only filled `<path>`s (no
  strokes, no groups), and those two files are glyphs taken from the
  bundled Ionicons font, so they match the app's other icons.
- The iOS image viewer is `ImagePreview`
  (`modules/native-menu/ios/ImagePreviewModule.swift`, called through
  `previewImage` in `modules/native-menu/index.ios.ts`): a UIKit view
  controller (`ImageViewerController`) in the local module, not QuickLook.
  QuickLook was tried first: it hides the tapped view while it loads its own
  copy of the file, then zooms out of a snapshot that misses the React Native
  image — a blank box faded into the picture, and a ghost on close. The
  picture starts as the tapped bubble's own decoded image and flies out of
  the bubble at once (or after the download, if the bubble has no image yet);
  the full-resolution download then swaps in. The bubble box is hidden while
  the viewer is up. The bubble's frame, at fly-in and fly-out, comes from its
  presentation layers, so a bubble still moving with the keyboard is tracked. A
  UIScrollView gives pinch zoom (up to the image's pixel size, at least 3x) and
  double-tap zoom. A vertical drag at zoom 1, up or down, moves the picture with
  the finger; past 100pt, or a flick over 800pt/s, it flies back into the
  bubble, otherwise it springs back. Close flies it back the same way. A tap
  toggles the chrome (Close, Share, title) and the backdrop: `systemBackground`,
  or black while the chrome is hidden — and the status bar hides with the
  chrome (immersive black mode). That works because Info.plist sets
  `UIViewControllerBasedStatusBarAppearance` (in `app.json → ios.infoPlist`)
  and the presentation sets `modalPresentationCapturesStatusBarAppearance`;
  the controller's `prefersStatusBarHidden` then answers `immersive`. Do not
  revert the plist key — without it `prefersStatusBarHidden` is ignored and the
  status bar stays over the black backdrop. Share opens the share sheet with the
  downloaded file, enabled once the download lands. `ImageAttachment`
  (`src/components/attachments.tsx`) passes its box's React tag, from
  `findNodeHandle`, through `ImagePressHandler`; `ConversationView` calls
  `previewImage` on iOS. Android and web keep `src/components/ImageViewer.tsx`.
  Adding a Swift file to the local module needs `pod install`, because the Pods
  project lists the module's source files. New module classes are picked up
  from `expo-module.config.json`.
- **Never wrap list rows or bubbles in SwiftUI hosts** (`Host`/`RNHostView`).
  Every SwiftUI hosting view re-renders on each scroll frame (it observes
  ancestor geometry) and sizes itself asynchronously — one per bubble made
  fast scrolling lag and mounted rows at the wrong height. That is why
  `LongPressMenu` is UIKit.
- `LongPressMenu` lifts its child as-is: give transparent children a
  `previewBackground` (or the preview's shadow shows through as a grey card),
  and don't dim pressed states of wrapped views (the native interaction
  cancels the RN touch once the menu takes over). A lifted cell's shadow
  paints over later siblings only if they sit below it — the DM list header
  carries `zIndex: 1` so the pinned grid's previews aren't cut off by the
  first row.
- Native menus have no Cancel row: Maestro flows close them through
  `e2e/subflows/dismiss-menu.yaml` (Cancel on Android, outside tap on iOS).
- `MessageList` keys its `useMemo` on `store.version` (not the history array)
  — the store mutates history in place, so identity-keyed memos go stale and
  new messages don't render. Keep `version` in the deps.
- **`MessageList` is an inverted FlatList** (the standard chat list). Rows
  are built oldest → newest, then reversed, so row 0 (the newest message)
  sits at the bottom: a thread opens on its newest message with nothing to
  scroll, and older history pages in via `onEndReached` at the top edge.
  `maintainVisibleContentPosition` keeps the view still while history is read
  and auto-scrolls when within 80pt of the bottom; sending scrolls to offset 0.
  Because the list is flipped, top and bottom paddings and insets are swapped
  (`paddingTop` / `contentInset.top` sit at the screen bottom). The iOS 26 blur
  that once ruled out an inverted list had a real cause: UIKit's scroll-edge
  effect. react-native-screens applies a route's `scrollEdgeEffects` only to
  the first scroll view it finds when the screen mounts, so the message list
  kept the automatic edge effect, and on the flipped list its "top" edge sits
  at the bottom of the screen, over the newest messages. `NoScrollEdgeEffects`
  (`src/components/ScrollEdge.tsx`) wraps a scroll view in react-native-screens'
  experimental `ScrollViewMarker` with every edge hidden (iOS only,
  `pointerEvents="box-none"`). Both the message list and the composer's
  drag-area scroll view are wrapped.
- The `chat`/`conversation` routes set `scrollEdgeEffects` to hidden — we draw
  our own top treatment: `EdgeBlur` (`modules/native-menu/ios/EdgeBlurModule.swift`,
  rendered via `EdgeBlur` in `modules/native-menu/index.ios.ts`) on iOS 26+,
  `EdgeFade` (`src/components/Glass.tsx`, a native `experimental_backgroundImage`
  gradient) everywhere else. `EdgeBlur` is a `CABackdropLayer` with the private
  `CAFilter` type `variableBlur` plus a gradient alpha mask — strongest at the
  edge, fading to sharp by ~140pt, no tint. **It is private API** (installed by
  name through `CAFilter.filter(withName:)`); if App Review ever objects, the
  fallback is `EdgeFade`, which tints.
- Glass stays **light** — that is the system behavior, not a bug we patch.
  iOS 26/27 `UIGlassEffect` does not darken over dark content (Apple's own
  Photos keeps light glass over a near-black photo); "adaptive" in Apple's
  docs means light/dark *mode* adaptation, not backdrop sampling. Do NOT
  hand-roll tone detection — a probe view sampling the backdrop was tried and
  ripped out: it re-coloured content wrongly and extra views inside a
  `GlassView`/`GlassContainer` break the material's touch response. Inside a
  `GlassContainer` (`UIGlassContainerEffect`) member glasses render as one
  union — per-member `overrideUserInterfaceStyle`/`tintColor` overrides are
  ignored, so never rely on them. Keep glass children to real content only
  (no invisible helper views), and let the material do its own legibility.
- Composer geometry: `ConversationView` computes
  `edgeInset = max(bottomSafeArea - 6, 12)` (6pt into the home-indicator inset,
  min 12pt) and `dockPad = edgeInset - COMPOSER_PAD`; `Composer` takes
  `sideInset`. That keeps the dock off the screen edge but lets it ride into
  the safe area, which is what makes the 28pt bottom margin. While the
  keyboard lifts the dock, the side margins narrow to
  `kbUpInset = edgeInset - keyboardOffset` — the same gap the glass keeps
  above the keyboard — so the composer has one visual margin on every side
  (Messages does the same widening). `useKeyboardLift` drives a `kbWide`
  shared value (0→1 on show, →0 on hide/dismiss) from the keyboard handler's
  final-height events — it animates on the UI thread in step with the
  keyboard instead of trailing a React state flip — and `Composer`
  interpolates `paddingHorizontal` between the two insets in `wrapStyle`.
- Uploads: Expo SDK 57's global `fetch` is `expo/fetch`, which rejects React
  Native `{ uri }` FormData parts. Always go through `ApiClient` (built with
  `readFile` in `session.tsx`), never a hand-rolled `fetch` + FormData.
- The voice-message morph (`Composer` + `DictationBar`'s `VoicePanel`) swaps
  the native "+" for an `expo-glass-effect` `GlassView` twin while animating,
  so it can merge with the pill inside a `GlassContainer`. Keep the native
  button mounted (hide it with opacity), and mount the twin only while
  needed: a `GlassView` first laid out under opacity 0 never shows its glass.
- The composer input has a fixed `lineHeight` of 21 (a whole number of
  points): with the font's natural ~20.3pt line UIKit rounds the content
  height up while layout rounds the view to a third of a point, so a single
  line became scrollable. It grows to six lines (`maxHeight`), then scrolls.
- iOS keyboard handling is `react-native-keyboard-controller`. The
  `KeyboardProvider` in `app/_layout.tsx` is iOS-only (on Android it takes over
  window insets even when disabled). In `ConversationView`, the custom hook
  `useKeyboardLift` (built on `useGenericKeyboardHandler`) replaces
  `KeyboardStickyView` and `useReanimatedKeyboardAnimation`. Show and hide set
  the final keyboard height once, in `onStart`, which runs inside UIKit's
  keyboard animation block, so Core Animation moves the composer and the list
  along the keyboard's own curve. A drag follows the finger through
  `onInteractive`. Two library quirks are worked around: (1) at drag release,
  one `onInteractive` position equals the `KeyboardGestureArea`'s invisible
  accessory height; it is ignored, because applying it caused a 34pt drop below
  rest. (2) `onMove` is not used: on iOS 26, after a drag is released, its
  values are off by the accessory height and follow a model curve rather than
  the real keyboard, which dropped the composer behind the keyboard.
  `KeyboardGestureArea` runs with `offset={0}` — a nonzero offset extends the
  keyboard's grab area above it via an invisible `inputAccessoryView`, and a
  drag STARTING inside that zone makes UIKit snap the keyboard/accessory
  assembly's top edge under the finger (~53pt in one frame). With 0 the zone
  is just the keyboard frame, and composer-originated drags dismiss through
  `ComposerDragArea`'s interactive-dismiss scroll view (overscroll) — the
  keyboard follows the finger's delta from the first frame. The 0-height
  accessory still attaches, which the deferred-resign timing below relies on.
  The list is translated, never
  resized; the part lifted off the top stays reachable via `topSlack`, applied
  as `contentInset.bottom` on the flipped list (the screen top). Android keeps
  `KeyboardAvoidingView`.
  Back (`app/conversation.tsx`), iOS only: with the keyboard up, a Back tap
  holds the pop (`beforeRemove` + `preventDefault`) until the keyboard has
  started to hide, then re-dispatches it. The hold goes through
  `afterKeyboardCloses` in `src/keyboard.ts` (`keyboardWillHide`, with a 250ms
  fallback), which the image viewer also waits on before it presents (a
  full-screen presentation in the frame the keyboard-controller defers the
  resign drops the keyboard without its animation). The library defers the real
  `resignFirstResponder` by one frame when the `KeyboardGestureArea` accessory
  is attached, while a JS-driven pop unmounts the composer at once
  (react-native-screens animates a snapshot on Fabric). The deferred resign then
  ran on an input no longer in a window, and the keyboard stayed up over the DM
  list. The swipe-back gesture needs nothing: UIKit resigns the input as the
  swipe starts. Android calls `Keyboard.dismiss()` on `beforeRemove`.
- iOS screenshots omit secure-text contents and the secure keyboard — an
  "empty" password field in a Maestro capture is not a bug.
- The status bar belongs to the screens: every Stack screen sets
  `statusBarStyle` (`app/_layout.tsx`: `"dark"` by default, `"light"` on
  `/call`). Don't add expo-status-bar's `<StatusBar>` — with
  `UIViewControllerBasedStatusBarAppearance` on (the image viewer needs it),
  React Native's StatusBar module throws (a red box on iOS). Set the style on
  every screen: once one screen sets it, Android gives the light style to
  screens that don't.
- `@expo/ui` must stay autolinked on Android: expo-router's own Android code
  imports it, and excluding it (`expo.autolinking.android.exclude` in
  `package.json`) crashed the app at launch with "Cannot find native module
  'ExpoUI'".
- **Calls** (`src/call/`, `app/call.tsx`; the root CLAUDE.md/AGENTS.md has
  the whole design under **DM calls**). The shared `CallManager`
  (`store.calls`) owns all call logic; this app only adapts and draws it.
  - `src/call/platform.native.ts` adapts react-native-webrtc. Its
    `MediaStreamTrack.stop()` only disables a track; `release()` frees the
    native capturer. The adapter releases every stream it creates
    (`releaseStream`) before the tracks in it: on Android,
    `MediaStream.dispose()` also disposes the tracks it still lists and
    throws on one that was already disposed. Don't release call tracks or
    streams anywhere else.
  - Video is `CallVideo` (`RTCView`). On Android an `RTCView` renders into a
    `SurfaceView`, which ignores opacity and rounded-corner clipping: hide a
    video by drawing a view over it (as `CallScreen`'s backdrop and the
    floating tile's cover do), and give a video drawn over another video
    `zOrder={1}`.
  - All call sounds and audio routing go through `callAudio`
    (react-native-incall-manager). While a call is live, nothing else may
    touch the audio session: expo-audio's pause deactivates the iOS session
    100 ms later, `setAudioModeAsync` sets the `.playback` category, and
    expo-video sets `.playback` on every play. `src/media.ts` enforces it —
    register any new player or recorder with `registerMedia`, and check
    `isCallLive()` before starting one (`enablePlayback()` already returns
    early during a call).
  - The sound files must keep their names
    (`assets/sounds/incallmanager_ringtone.mp3`,
    `incallmanager_ringback.mp3`): InCallManager's `"_BUNDLE_"` looks them up
    in the iOS bundle root and Android `res/raw`, where the `expo-asset`
    plugin in `app.json` copies them at prebuild.
  - `/call` is a `fullScreenModal` with `gestureEnabled: false` (a call ends
    with a button, never a swipe). `CallHost` pushes it once per call and the
    route closes itself when `useCallView().call` is null; the chevron closes
    it mid-call and the floating tile reopens it. Both the push and the close
    wait while the app is in the background (`useAppVisible`): on iOS a
    modal dismissed in the background never finishes its transition, and
    react-native-screens' snapshot of the closing screen comes out blank, so
    the app came back to a black screen.
  - The floating tile (`MiniCall`) on iOS: while a modal sheet (`settings`,
    `new-chat`, `profile`: the `SHEETS` routes in `CallHost`) is open,
    `OverSheet` renders it in a react-native-screens `FullWindowOverlay`
    (with `unstable_accessibilityContainerViewIsModal={false}`, so VoiceOver
    still reaches the app), because iOS presents the sheets above the root
    view. On every other route it stays in the root view: react-native-screens
    brings the overlay window's container to the front whenever UIKit adds a
    subview to the window, so in the overlay the tile would draw above
    context menus, the image viewer, and the closing call screen. An `Alert`
    has its own window and always covers the tile. Each move between the
    overlay and the root view (a sheet opens or closes) remounts `MiniCall`,
    which shows the peer's avatar until the video's first frame arrives
    again. Android needs no overlay: the sheets are views in the root view,
    and the tile, drawn after the Stack, is above them. NOT verified on a
    device: with a sheet open, the tile also draws above anything presented
    over the sheet (the Settings → Profile photo menu and the photo picker),
    and after a minimize onto a sheet it shows at once over the call screen
    while that screen slides away.
  - `CallScreen` pads its controls with `useSafeAreaInsets()` — never a
    `SafeAreaView`, and no extra `SafeAreaProvider` in the modal. A
    `SafeAreaView` has no insets on its first frames (Fabric applies them
    natively afterwards) and in an iOS full-screen modal gets none at all, so
    the chevron sat under the status bar while the screen opened. Measured on
    Android: one frame with the root provider; about 0.75 s with a provider
    inside the modal, which reports no top inset until the modal's transition
    ends.
  - The incoming-call notification is `presentCallNotification` in
    `src/notify.ts` (identifier `call:<channel>`, Android channel `calls`);
    `CallHost` removes it when the call stops ringing. On Android a
    notification's channel is set by `trigger: { channelId }` — a
    `channelId` inside `content` is ignored. It is the fallback: on iOS the
    system call UI rings instead, and on Android the ring service does
    while it runs (both below).
  - The phone's own call handling is `src/call/system.ios.ts` (iOS),
    `system.android.ts` (Android) and `system.ts` (Expo web). Keep their
    seven exports identical: `session.tsx`, `CallHost` and `app/call.tsx`
    import `./system`, and Metro picks the platform file.
  - iOS: `modules/system-calls` reports every call to CallKit or, in
    mainland China, LiveCommunicationKit (`SystemCalls.kind`, from the
    region setting). While the system holds a call it owns the audio
    session: `CallAudio.swift` puts WebRTC's `RTCAudioSession` in manual
    audio and enables it only between the provider's activate and
    deactivate callbacks, and does `callAudio`'s (InCallManager's) job for
    that call. Never start InCallManager's session for a call the system
    holds — `useCallRoute` in `system.ios.ts` picks the sounds for each
    call. Adding a Swift file to the module needs `pod install`. The iOS
    Simulator never uses the system call UI (`kind` is `none` there: iOS
    ends every CallKit / LiveCommunicationKit call in the Simulator), so
    test changes to this module on a real iPhone.
  - Android: `modules/call-ringer` is the ring service that rings calls
    while the app is closed (the root CLAUDE.md/AGENTS.md describes it
    under **DM calls**). Its rules:
    - Never `stopService` it: stopping a service started with
      `startForegroundService` before it reached `startForeground` crashes
      the app. `RingService.stop` clears the stored session, and the
      service stops itself.
    - The app and the service agree on "on screen" through `AppVisibility`
      (an activity is resumed: the same signal as React Native's
      `AppState`). The app claims each call it rings (`CallRinger.claim`)
      and releases it when the call stops ringing or the app leaves the
      screen; the service posts its notification only for an unclaimed
      call while the app is off screen.
    - The missed-call notification takes the conversation's slot (tag =
      the channel, id 0). That is where expo-notifications posts the
      conversation's message notification (identifier = the channel, id
      `ExpoPresentationDelegate.ANDROID_NOTIFICATION_ID`), so either one
      replaces the other, and `dismissForChannel` clears both.
    - The service runs only while notifications are allowed
      (`CallRinger.notificationsAllowed()`), and `session.tsx` re-syncs it
      every time the app comes back to the foreground. Every path to the
      login screen stops it (`syncRingService(false)`): logout, and an app
      start with no stored token or a failed startup `GET /api/me`. The
      service keeps its own copy of the session and would otherwise ring
      calls that the signed-out app can't answer. expo-notifications
      reports "denied" rather than "undetermined" once the app has asked
      before, so ask through `ensureNotificationPermission()`, which asks
      whenever Android still shows the prompt. After two denials
      (`canAskAgain` false) Android shows no prompt; send the user to
      `CallRinger.openNotificationSettings()`.
    - A native change needs a rebuild (`npm run android`): Fast Refresh
      pushes the new JS into the old native build, which then fails with
      "undefined is not a function".

## Test

- `npx tsc --noEmit` — typecheck.
- `npx maestro test e2e/` — 8 flows on a booted simulator/emulator
  (`--udid <id>` to pick a device). Set `JAVA_HOME` to a real JDK first
  (Android Studio's JBR works).
- Emulator quirks: keep animations ON (Reanimated's reduced-motion dev toast
  covers the composer); `adb shell settings put secure show_ime_with_hard_keyboard 1`
  on AVDs that hide the soft IME; on Android `hideKeyboard` ≈ a back press
  when no keyboard is open — gate it behind `visible: "return"` in flows.
