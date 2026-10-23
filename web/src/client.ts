/**
 * The singleton client glue: one ApiClient + one ChatStore for the whole app.
 *
 * The React tree never talks to fetch/WebSocket directly — it renders
 * ChatStore.state and calls store actions. Side-effect surfaces (toasts,
 * the reconnect banner, push notifications) are wired here via the store's
 * event hooks.
 */
import { ApiClient } from "@shared/api";
import { ChatStore, type ToastEvent, type NotifyEvent } from "@shared/store";
import { DebugReporter } from "@shared/debug";
import type { CallOutcome } from "@shared/call";
import { webCallPlatform } from "./call-platform";

export const api = new ApiClient({ baseUrl: "" });

export const Debug = new DebugReporter({
  api,
  sessionStorage: typeof sessionStorage !== "undefined" ? sessionStorage : null,
});

type Listener = (t: ToastEvent) => void;
const toastListeners = new Set<Listener>();
export function onToast(fn: Listener): () => void {
  toastListeners.add(fn);
  return () => toastListeners.delete(fn);
}
export function toast(text: string, isErr = false): void {
  for (const fn of toastListeners) fn({ text, isErr });
}

const notifyListeners = new Set<(n: NotifyEvent) => void>();
export function onNotify(fn: (n: NotifyEvent) => void): () => void {
  notifyListeners.add(fn);
  return () => notifyListeners.delete(fn);
}

function wsUrl(): string {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  return `${proto}://${location.host}/ws`;
}

export const store = new ChatStore({
  api,
  wsUrl,
  storage: typeof localStorage !== "undefined" ? localStorage : null,
  onToast: (t) => toast(t.text, t.isErr),
  onNotify: (n) => {
    for (const fn of notifyListeners) fn(n);
  },
  debug: (level, event, message, ctx) => {
    const lvl = level as "debug" | "info" | "warn" | "error";
    (Debug[lvl] || Debug.debug)(event, message, ctx);
  },
  onUnauthorized: () => {
    // The session is already gone, so this only ends the call on this page
    // (the socket closed with 4401 and sends nothing more); leaving the page
    // then asks nothing.
    store.calls.hangUp();
    window.location.href = "/login";
  },
  callPlatform: webCallPlatform,
});

// Foreground tracking drives the "actively reading ⇒ mark read" rule, and
// tells the server whether a call needs a push to reach this user.
store.setForeground(document.visibilityState === "visible");
document.addEventListener("visibilitychange", () => {
  store.setForeground(document.visibilityState === "visible");
});

// A call that rings while no screen of the callee shows it also rings as a
// Web Push notification that stays until clicked (tag "call-<id>", see
// callPushPayload in internal/webapp/calls.go). Close it once the call stops
// ringing here because it was answered or declined, on this device or
// another: the server's call_over push replaces it too, but only when the
// push service delivers it. Any other end is a missed call (db.CallMissed):
// the server's missed_call push replaces the notification under the same tag.
const ANSWERED_OR_DECLINED: ReadonlySet<CallOutcome> = new Set<CallOutcome>([
  "hung_up", // declined on this device
  "declined",
  "answered_elsewhere",
  "completed",
  "failed",
]);
let ringingCallId: string | null = null;
store.subscribe(() => {
  const call = store.state.call;
  const ringing = call?.phase === "incoming" ? call.id : null;
  if (ringingCallId && ringingCallId !== ringing && call?.id === ringingCallId) {
    // Still live after ringing: answered on this device.
    const answered = call.phase !== "ended";
    if (answered || (call.outcome && ANSWERED_OR_DECLINED.has(call.outcome))) void closeCallNotification(ringingCallId);
  }
  ringingCallId = ringing;
});

async function closeCallNotification(callId: string): Promise<void> {
  if (!("serviceWorker" in navigator)) return;
  try {
    const reg = await navigator.serviceWorker.getRegistration();
    for (const n of (await reg?.getNotifications({ tag: `call-${callId}` })) ?? []) n.close();
  } catch {
    /* no notification to close */
  }
}
