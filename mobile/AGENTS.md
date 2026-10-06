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
- Action menus use `src/components/ActionSheet.tsx` (iOS `ActionSheetIOS` /
  Android bottom-sheet `Modal`). Do **not** use `Alert.alert` — Android caps
  it at 3 buttons and drops Cancel.
- `MessageList` keys its `useMemo` on `store.version` (not the history array)
  — the store mutates history in place, so identity-keyed memos go stale and
  new messages don't render. Keep `version` in the deps.

## Test

- `npx tsc --noEmit` — typecheck.
- `npx maestro test e2e/` — 8 flows on a booted simulator/emulator
  (`--udid <id>` to pick a device). Set `JAVA_HOME` to a real JDK first
  (Android Studio's JBR works).
- Emulator quirks: keep animations ON (Reanimated's reduced-motion dev toast
  covers the composer); `adb shell settings put secure show_ime_with_hard_keyboard 1`
  on AVDs that hide the soft IME; on Android `hideKeyboard` ≈ a back press
  when no keyboard is open — gate it behind `visible: "return"` in flows.
