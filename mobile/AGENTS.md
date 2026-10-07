# Expo HAS CHANGED

Read the exact versioned docs at https://docs.expo.dev/versions/v57.0.0/ before writing any code.

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
  iOS 26 these are `@expo/ui/swift-ui` `ContextMenu`/`Menu`; Android and older
  iOS fall back to `src/components/ActionSheet.tsx` (bottom-sheet `Modal`). Do
  **not** use `Alert.alert` for menus — Android caps it at 3 buttons and drops
  Cancel (alerts are fine for confirmations).
- Views wrapped by `LongPressMenu`/`TapMenu` are hosted in SwiftUI
  (`RNHostView matchContents`), which measures them **without** the parent's
  width limit — give them an explicit `width`/`maxWidth` (bubbles get
  `maxWidth` from the list width; DM rows get the list width) or text stops
  wrapping and rows shrink to their content. Give the hosted view an opaque
  background so the lifted preview reads as a solid card.
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
  the native SwiftUI "+" for a UIKit `GlassView` twin while animating, so it
  can merge with the pill inside a `GlassContainer`. Keep the SwiftUI button
  mounted (hide it with opacity) — a freshly mounted SwiftUI host draws its
  first frame out of place — and mount the twin only while needed: a
  `GlassView` first laid out under opacity 0 never shows its glass.
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
