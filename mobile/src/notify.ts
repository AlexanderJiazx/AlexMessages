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

const CHANNEL_ID = "messages";

/** Channel-open requests from notification taps — the chat screen subscribes
 *  and navigates. Kept here (not in session.ts) to avoid a require cycle. */
const openReqListeners = new Set<(channel: string) => void>();
export function onOpenChannelRequest(fn: (channel: string) => void): () => void {
  openReqListeners.add(fn);
  return () => openReqListeners.delete(fn);
}
export function requestOpenChannel(channel: string): void {
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
  }

  Notifications.addNotificationResponseReceivedListener((res) => {
    const channel = (res.notification.request.content.data as { channel?: string })
      ?.channel;
    if (channel) requestOpenChannel(channel);
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
        ...(Platform.OS === "android" ? { channelId: CHANNEL_ID } : {}),
      },
      trigger: null, // immediate
    });
  } catch {
    /* notification errors never break chat */
  }
}

/** Clears a conversation's posted notification when it is opened. */
export async function dismissForChannel(channel: string): Promise<void> {
  try {
    await Notifications.dismissNotificationAsync(channel);
  } catch {
    /* noop */
  }
}
