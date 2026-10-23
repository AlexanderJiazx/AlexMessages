// iOS/web stub for @expo/ui/jetpack-compose: Compose exists only on Android,
// and every call site is gated behind Platform.OS === "android", so these are
// never rendered here — they exist only so the module resolves. Typed as the
// real components (a type-only import loads nothing) so call sites typecheck.
import type * as Compose from "@expo/ui/jetpack-compose";

const Stub = (() => null) as never;

export const DropdownMenu: typeof Compose.DropdownMenu = Stub;
export const DropdownMenuItem: typeof Compose.DropdownMenuItem = Stub;
export const Host: typeof Compose.Host = Stub;
export const Icon: typeof Compose.Icon = Stub;
export const RNHostView: typeof Compose.RNHostView = Stub;
export const Text: typeof Compose.Text = Stub;
