/**
 * Local notifications — the managed-Expo equivalent of the legacy Android
 * client's MessagingService: while the app process is alive the shared
 * ChatSocket stays connected, and ChatStore.onNotify fires for messages that
 * need an OS notification (app backgrounded or a different conversation).
 * Tapping a notification routes to that conversation, like the legacy
 * EXTRA_OPEN_CHANNEL intent.
 */
import { Platform } from "react-native";
import * as Notifications from "expo-notifications";
import type { NotifyEvent } from "@alexmessages/shared";
import { CallRinger } from "../modules/call-ringer";

const CHANNEL_ID = "messages";
/** Incoming calls: a heads-up banner. Silent — the app rings and vibrates
 *  itself while it runs (src/call/callAudio). */
const CALLS_CHANNEL_ID = "calls";

/** Channel-open requests from notification taps — the chat screen subscribes
 *  and navigates. Kept here (not in session.ts) to avoid a require cycle. */
const openReqListeners = new Set<(channel: string) => void>();
/** A tap that came before any screen listened (the app was starting). */
let pendingOpen: { channel: string; at: number } | null = null;
const PENDING_OPEN_MS = 60000;
export function onOpenChannelRequest(fn: (channel: string) => void): () => void {
  openReqListeners.add(fn);
  const pending = pendingOpen;
  pendingOpen = null;
  if (pending && Date.now() - pending.at < PENDING_OPEN_MS) fn(pending.channel);
  return () => openReqListeners.delete(fn);
}
export function requestOpenChannel(channel: string): void {
  if (openReqListeners.size === 0) {
    pendingOpen = { channel, at: Date.now() };
    return;
  }
  for (const fn of openReqListeners) fn(channel);
}
let configured = false;

/** One-time setup: Android channel, foreground display handler, tap routing. */
export function configureNotifications(): void {
  if (configured) return;
  configured = true;

  Notifications.setNotificationHandler({
    handleNotification: async () => ({
      shouldPlaySound: true,
      shouldSetBadge: true,
      shouldShowBanner: true,
      shouldShowList: true,
    }),
  });

  if (Platform.OS === "android") {
    void Notifications.setNotificationChannelAsync(CHANNEL_ID, {
      name: "Messages",
      importance: Notifications.AndroidImportance.HIGH,
      vibrationPattern: [0, 250, 250, 250],
    });
    void Notifications.setNotificationChannelAsync(CALLS_CHANNEL_ID, {
      name: "Calls",
      importance: Notifications.AndroidImportance.MAX,
      sound: null,
      enableVibrate: false,
      lockscreenVisibility: Notifications.AndroidNotificationVisibility.PUBLIC,
    });
  }

  Notifications.addNotificationResponseReceivedListener((res) => {
    const data = res.notification.request.content.data as { channel?: string; kind?: string };
    // A call's banner just brings the app forward: its call screen is
    // already open, and opening the thread would cover it.
    if (data?.kind === "call") return;
    if (data?.channel) requestOpenChannel(data.channel);
  });
}

/** Ensures the OS permission is granted. Returns the granted flag. */
export async function ensureNotificationPermission(): Promise<boolean> {
  const cur = await Notifications.getPermissionsAsync();
  if (cur.granted) return true;
  const req = await Notifications.requestPermissionsAsync();
  return req.granted;
}

/** Posts a local notification for an incoming message. */
export async function presentMessageNotification(n: NotifyEvent): Promise<void> {
  try {
    if (!(await ensureNotificationPermission())) return;
    await Notifications.scheduleNotificationAsync({
      identifier: n.channel, // one active notification per conversation
      content: {
        title: n.title,
        body: n.body,
        data: { channel: n.channel },
      },
      // Immediate; on Android, posted to the Messages channel.
      trigger: Platform.OS === "android" ? { channelId: CHANNEL_ID } : null,
    });
  } catch {
    /* notification errors never break chat */
  }
}

const callNotificationId = (channel: string) => `call:${channel}`;

/** Posts the banner for a call ringing while the app is in the background. */
export async function presentCallNotification(n: NotifyEvent): Promise<void> {
  try {
    if (!(await ensureNotificationPermission())) return;
    await Notifications.scheduleNotificationAsync({
      identifier: callNotificationId(n.channel),
      content: {
        title: n.title,
        body: n.body,
        data: { channel: n.channel, kind: "call" },
        sound: true,
        priority: Notifications.AndroidNotificationPriority.MAX,
      },
      trigger: Platform.OS === "android" ? { channelId: CALLS_CHANNEL_ID } : null,
    });
  } catch {
    /* notification errors never break calls */
  }
}

/** Takes a call's banner down once it stops ringing here. */
export async function dismissCallNotification(channel: string): Promise<void> {
  try {
    await Notifications.dismissNotificationAsync(callNotificationId(channel));
  } catch {
    /* noop */
  }
}

/** Clears a conversation's posted notifications when it is opened (with
 *  the Android ring service's missed-call notification). */
export async function dismissForChannel(channel: string): Promise<void> {
  try {
    await Promise.all([Notifications.dismissNotificationAsync(channel), CallRinger.dismissMissed(channel)]);
  } catch {
    /* noop */
  }
}
