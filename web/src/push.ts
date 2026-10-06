/**
 * Web Push lifecycle — a faithful port of the legacy app.js flow.
 *
 * The service worker registers eagerly on boot (so pending pushes still
 * arrive), but `pushManager.subscribe()` — which triggers the browser
 * permission prompt — is gated behind a real user gesture, either the
 * first-visit "Stay in the loop" modal or the Settings toggle.
 */
import { api, store, toast } from "./client";

export const PUSH_PROMPTED_KEY = "am_push_prompted"; // popup shown at least once
export const PUSH_DECLINED_KEY = "am_push_declined"; // dismissed; don't re-ask

let swReg: ServiceWorkerRegistration | null = null;

export function pushSupported(): boolean {
  return (
    "serviceWorker" in navigator && "PushManager" in window && "Notification" in window
  );
}

function urlBase64ToUint8Array(base64: string): Uint8Array {
  const pad = "=".repeat((4 - (base64.length % 4)) % 4);
  const raw = (base64 + pad).replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(raw);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

async function registerAndReady(): Promise<ServiceWorkerRegistration> {
  const reg = await navigator.serviceWorker.register("/sw.js", { updateViaCache: "none" });
  await reg.update();
  await navigator.serviceWorker.ready;
  swReg = reg;
  return reg;
}

/** Early SW registration so push events land even before the user opts in. */
export function registerServiceWorker(): void {
  if (!("serviceWorker" in navigator)) return;
  navigator.serviceWorker
    .register("/sw.js", { updateViaCache: "none" })
    .then((reg) => {
      swReg = reg;
    })
    .catch((err) => console.warn("[push] sw register failed", err));
  navigator.serviceWorker.addEventListener("message", onServiceWorkerMessage);
}

function onServiceWorkerMessage(event: MessageEvent): void {
  const data = event.data || {};
  if (data.type === "OPEN_CHANNEL" && data.channel) {
    store.openChannelExternal(data.channel);
  } else if (data.type === "PUSH_RECEIVED") {
    store.handlePushReceived(data.channel);
  }
}

function sameBuffer(a: Uint8Array, b: Uint8Array): boolean {
  if (!a || !b || a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

async function getOrCreateSubscription(reg: ServiceWorkerRegistration, vapidKey: string) {
  let sub = await reg.pushManager.getSubscription();
  if (sub) {
    // If the VAPID key rotated, re-subscribe so pushes don't silently 403.
    const existing = sub.options?.applicationServerKey;
    const want = urlBase64ToUint8Array(vapidKey);
    if (existing && !sameBuffer(new Uint8Array(existing), want)) {
      try {
        await sub.unsubscribe();
      } catch {
        /* best effort */
      }
      sub = null;
    }
  }
  if (!sub) {
    sub = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlBase64ToUint8Array(vapidKey) as BufferSource,
    });
  }
  return sub;
}

async function postSubscriptionToServer(sub: PushSubscription): Promise<void> {
  await api.pushSubscribe(sub.toJSON() as Record<string, unknown>, navigator.userAgent);
}

/** Enable push — must be called from a user-gesture handler. */
export async function enablePushFlow(): Promise<boolean> {
  if (!pushSupported()) {
    toast("Notifications aren't supported in this browser", true);
    return false;
  }
  try {
    const reg = await registerAndReady();
    const permission = await Notification.requestPermission();
    if (permission !== "granted") {
      try {
        localStorage.setItem(PUSH_DECLINED_KEY, "1");
      } catch {
        /* private mode */
      }
      toast(
        permission === "denied"
          ? "Notifications are blocked — enable them in your browser settings"
          : "Notifications were declined",
      );
      store.state.pushSubscribed = false;
      return false;
    }
    const { public_key: vapidKey } = await api.pushPublicKey();
    const sub = await getOrCreateSubscription(reg, vapidKey);
    await postSubscriptionToServer(sub);
    try {
      localStorage.removeItem(PUSH_DECLINED_KEY);
    } catch {
      /* private mode */
    }
    store.state.pushSubscribed = true;
    toast("Notifications enabled");
    return true;
  } catch (err) {
    console.error("[push] enable flow failed", err);
    toast("Couldn't enable notifications", true);
    return false;
  }
}

export async function disablePushFlow(): Promise<void> {
  try {
    const reg = swReg || (await navigator.serviceWorker.getRegistration());
    if (!reg) return;
    const sub = await reg.pushManager.getSubscription();
    if (sub) {
      try {
        await api.pushUnsubscribe(sub.endpoint);
      } catch {
        /* best effort */
      }
      try {
        await sub.unsubscribe();
      } catch {
        /* best effort */
      }
    }
    store.state.pushSubscribed = false;
    try {
      localStorage.setItem(PUSH_DECLINED_KEY, "1");
    } catch {
      /* private mode */
    }
    toast("Notifications disabled");
  } catch (err) {
    console.warn("[push] disable flow failed", err);
  }
}

export function pushAlreadyPrompted(): boolean {
  try {
    return !!localStorage.getItem(PUSH_PROMPTED_KEY);
  } catch {
    return false;
  }
}
export function pushPreviouslyDeclined(): boolean {
  try {
    return !!localStorage.getItem(PUSH_DECLINED_KEY);
  } catch {
    return false;
  }
}
export function markPushPrompted(): void {
  try {
    localStorage.setItem(PUSH_PROMPTED_KEY, "1");
  } catch {
    /* private mode */
  }
}

/**
 * Idempotent push-state sync after init: if permission was already granted,
 * silently ensure the server holds a current subscription. Otherwise surface
 * the opt-in modal exactly once (returns true when the modal should show).
 * Never prompts by itself — that requires the modal's Enable click.
 */
export async function reconcilePushOnLoad(): Promise<"prompt" | "ok" | "unsupported"> {
  if (!pushSupported()) return "unsupported";
  if (Notification.permission !== "granted") {
    if (Notification.permission === "default" && !pushAlreadyPrompted() && !pushPreviouslyDeclined()) {
      markPushPrompted();
      return "prompt";
    }
    return "ok";
  }
  try {
    const reg = await registerAndReady();
    const { public_key: vapidKey } = await api.pushPublicKey();
    const sub = await getOrCreateSubscription(reg, vapidKey);
    await postSubscriptionToServer(sub);
    store.state.pushSubscribed = true;
  } catch (err) {
    console.warn("[push] reconcile failed", err);
  }
  return "ok";
}
