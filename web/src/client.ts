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
    window.location.href = "/login";
  },
});

// Foreground tracking drives the "actively reading ⇒ mark read" rule.
document.addEventListener("visibilitychange", () => {
  store.setForeground(document.visibilityState === "visible");
});
