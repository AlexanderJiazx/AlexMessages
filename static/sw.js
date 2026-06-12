// Alex Messages service worker.
//
// Three responsibilities, in order of importance:
//
//   1. Receive Web Push events and call `showNotification` for every one.
//      The Push API contract for `userVisibleOnly: true` subscriptions
//      REQUIRES this — if we ever skip it, the browser will eventually
//      degrade or revoke the subscription, and Chrome will surface a
//      generic "site updated in the background" notification instead.
//
//   2. Handle notification clicks by focusing an existing tab (and asking
//      it to open the relevant DM) or by opening a new one.
//
//   3. Handle `pushsubscriptionchange` by resubscribing with the current
//      VAPID key and posting the new subscription to the server.
//
// Kept deliberately small and stateless: every `push` event re-derives
// what it needs from the payload, so a fresh SW invocation after the
// browser unloaded us is just as correct as a long-lived one.

const ICON_FALLBACK = "/static/icons/favicon-192.png";
const BADGE_FALLBACK = "/static/icons/favicon-32.png";

self.addEventListener("install", () => {
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("message", (event) => {
  const data = event.data;
  if (data && data.type === "SKIP_WAITING") {
    self.skipWaiting();
  }
});

function safeParsePush(event) {
  if (!event.data) return {};
  try { return event.data.json(); } catch (_) {}
  try { return { body: event.data.text() }; } catch (_) {}
  return {};
}

function absolutize(maybePath) {
  if (!maybePath) return null;
  if (/^https?:/i.test(maybePath)) return maybePath;
  return self.location.origin + (maybePath.startsWith("/") ? maybePath : "/" + maybePath);
}

self.addEventListener("push", (event) => {
  const data = safeParsePush(event);
  const title = data.title || "Alex Messages";
  const body = data.body || "New message";
  const icon = absolutize(data.icon) || self.location.origin + ICON_FALLBACK;
  const badge = absolutize(data.badge) || self.location.origin + BADGE_FALLBACK;
  const url = data.url || "/";
  const channel = data.channel || "";

  // Stash the channel id on the notification so notificationclick can route
  // straight into the right DM. Unique tag per push so macOS Chrome reliably
  // shows a fresh banner instead of silently coalescing.
  const options = {
    body,
    icon,
    badge,
    tag: `am-${Date.now()}`,
    renotify: true,
    data: { url, channel },
    timestamp: data.created_at ? data.created_at * 1000 : Date.now(),
  };

  console.log("[sw] push:", title, "—", body, channel ? `(channel ${channel})` : "");

  // Tell any open tabs that a push for this channel arrived. They use it to
  // refresh their unread state without waiting for the WebSocket round trip.
  const notifyClients = self.clients.matchAll({ type: "window", includeUncontrolled: true })
    .then((clients) => {
      for (const client of clients) {
        try {
          client.postMessage({ type: "PUSH_RECEIVED", channel, url });
        } catch (_) { /* tab gone */ }
      }
    })
    .catch(() => undefined);

  event.waitUntil(Promise.all([
    notifyClients,
    self.registration.showNotification(title, options),
  ]));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const target = event.notification.data || {};
  const url = target.url || "/";
  const channel = target.channel || "";

  event.waitUntil((async () => {
    const all = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
    for (const client of all) {
      try {
        const sameOrigin = new URL(client.url).origin === self.location.origin;
        if (!sameOrigin) continue;
        if (channel) {
          client.postMessage({ type: "OPEN_CHANNEL", channel });
        }
        return client.focus();
      } catch (_) { /* try next */ }
    }
    if (self.clients.openWindow) {
      return self.clients.openWindow(url);
    }
  })());
});

// When the push service rotates a subscription's keys/endpoint, resubscribe
// and post the new credentials to the server. Without this, dead-on-arrival
// pushes will pile up after a key rotation.
self.addEventListener("pushsubscriptionchange", (event) => {
  event.waitUntil((async () => {
    try {
      const keyRes = await fetch("/api/push/public-key", { credentials: "same-origin" });
      if (!keyRes.ok) throw new Error("public-key fetch failed: " + keyRes.status);
      const { public_key: vapidKey } = await keyRes.json();

      const sub = await self.registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(vapidKey),
      });
      await fetch("/api/push/subscribe", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ ...sub.toJSON(), user_agent: self.navigator.userAgent }),
      });
      console.log("[sw] resubscribed after pushsubscriptionchange");
    } catch (err) {
      console.error("[sw] pushsubscriptionchange handler failed", err);
    }
  })());
});

function urlBase64ToUint8Array(base64) {
  const pad = "=".repeat((4 - (base64.length % 4)) % 4);
  const raw = (base64 + pad).replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(raw);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}
