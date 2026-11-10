/**
 * Whole-stack navigation that expo-router's `router` can't express: it acts
 * on the focused navigator (inside the Settings sheet, its own nested
 * stack), so `router.replace("/login")` from there leaves the screens under
 * the sheet in place. These helpers reset the app's root Stack
 * (app/_layout.tsx) directly, which also takes every modal sheet down.
 */
import type { useNavigationContainerRef } from "expo-router";

export type NavRef = ReturnType<typeof useNavigationContainerRef>;

interface LooseRoute {
  key?: string;
  name: string;
  params?: object;
  state?: LooseState;
}
interface LooseState {
  key?: string;
  index?: number;
  routeNames?: string[];
  routes: LooseRoute[];
}

/** The root Stack's state: the navigator that owns `chat` and `login`. */
function appStack(ref: NavRef): LooseState | null {
  if (!ref.isReady()) return null;
  const find = (s: LooseState | undefined): LooseState | null => {
    if (!s) return null;
    if (s.routeNames?.includes("chat") && s.routeNames.includes("login")) return s;
    for (const r of s.routes) {
      const hit = find(r.state);
      if (hit) return hit;
    }
    return null;
  };
  return find(ref.getRootState() as unknown as LooseState);
}

/** The routes of the root Stack, bottom first ([] before it mounts). */
export function stackRoutes(ref: NavRef): LooseRoute[] {
  return appStack(ref)?.routes ?? [];
}

/**
 * Replaces the root Stack with `routes` (bottom first). A route passed with
 * the `key` of a mounted route stays mounted; every other route is created
 * or removed. Returns false when the Stack isn't mounted yet.
 */
export function resetStack(ref: NavRef, routes: LooseRoute[]): boolean {
  const stack = appStack(ref);
  if (!stack?.key) return false;
  ref.dispatch({
    type: "RESET",
    payload: { index: routes.length - 1, routes },
    target: stack.key,
  } as Parameters<NavRef["dispatch"]>[0]);
  return true;
}

/** Signed out: the login screen alone, every sheet and screen removed. */
export function resetToLogin(ref: NavRef): boolean {
  return resetStack(ref, [{ name: "login" }]);
}

/**
 * Shows `channel`'s conversation from anywhere (a notification tap): every
 * modal sheet and stacked conversation above the home screen is removed.
 * Narrow layouts push the conversation over the home screen; wide ones show
 * it in the home screen's right pane (`pushConversation` false).
 */
export function showConversation(ref: NavRef, channel: string, pushConversation: boolean): boolean {
  const routes = stackRoutes(ref);
  const home = routes.find((r) => r.name === "chat");
  if (!home) return false;
  // An open copy of that conversation stays mounted (its draft with it).
  const open = [...routes]
    .reverse()
    .find((r) => r.name === "conversation" && (r.params as { channel?: string } | undefined)?.channel === channel);
  const next: LooseRoute[] = [home];
  if (pushConversation) next.push(open ?? { name: "conversation", params: { channel } });
  if (next.length === routes.length && next.every((r, i) => r.key === routes[i].key)) return true;
  return resetStack(ref, next);
}
