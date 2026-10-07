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
  and the close control is unreachable to users and UiAutomator.
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
- **Don't make `MessageList` `inverted`.** On iOS 26 an inverted (flipped)
  FlatList that runs under floating chrome renders its first batch of cells
  blurred. The list is a normal FlatList that pins itself to the bottom by
  explicit `scrollToOffset` from measured content/viewport heights (not
  `scrollToEnd`, whose cached metrics lag keyboard resizes), and only enables
  `maintainVisibleContentPosition` while scrolled up (at the bottom it fights
  the pinning).
- The `chat`/`conversation` routes set `scrollEdgeEffects` to hidden — we draw
  our own `EdgeFade`s; UIKit's automatic iOS 26 edge effect would double up.
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
- iOS keyboard handling is `react-native-keyboard-controller` (the
  `KeyboardProvider` in `app/_layout.tsx` is iOS-only — on Android it takes
  over window insets even when disabled). `ConversationView` moves the
  composer (`KeyboardStickyView`) and the message list (a transform from
  `useReanimatedKeyboardAnimation`) with the keyboard frame by frame, and
  `KeyboardGestureArea` + an interactive-dismiss scroll view under the
  composer let a drag pull the keyboard down. The list is translated, never
  resized; the part lifted off the top stays reachable via `topSlack`
  (`contentInset.top`). Android keeps `KeyboardAvoidingView`.
- `MessageList` loads older history only after the first pin to the bottom
  (`ready`): `onStartReached` fires at mount while the offset is still 0, and
  prepending a page then left the conversation opened at the top.
- iOS screenshots omit secure-text contents and the secure keyboard — an
  "empty" password field in a Maestro capture is not a bug.

## Test

- `npx tsc --noEmit` — typecheck.
- `npx maestro test e2e/` — 8 flows on a booted simulator/emulator
  (`--udid <id>` to pick a device). Set `JAVA_HOME` to a real JDK first
  (Android Studio's JBR works).
- Emulator quirks: keep animations ON (Reanimated's reduced-motion dev toast
  covers the composer); `adb shell settings put secure show_ime_with_hard_keyboard 1`
  on AVDs that hide the soft IME; on Android `hideKeyboard` ≈ a back press
  when no keyboard is open — gate it behind `visible: "return"` in flows.
