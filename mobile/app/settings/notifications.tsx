import React, { useCallback, useEffect, useState } from "react";
import { Alert, AppState, Switch } from "react-native";
import * as Notifications from "expo-notifications";
import { CallRinger, hasRingService } from "../../modules/call-ringer";
import { toast, useSession } from "../../src/session";
import { colors } from "../../src/theme";
import { Group, Row, SettingsPage } from "../../src/components/SettingsList";

type Permission = Notifications.NotificationPermissionsStatus;

/** The notification permission, read again when the user comes back from the system settings. */
function useNotificationPermission(): [Permission | null, (p: Permission) => void] {
  const [perm, setPerm] = useState<Permission | null>(null);
  useEffect(() => {
    const read = () => void Notifications.getPermissionsAsync().then(setPerm);
    read();
    const sub = AppState.addEventListener("change", (s) => {
      if (s === "active") read();
    });
    return () => sub.remove();
  }, []);
  return [perm, setPerm];
}

export default function NotificationsPage() {
  const [perm, setPerm] = useNotificationPermission();

  const enabled = perm?.granted === true;
  const toggle = async () => {
    if (enabled) {
      Alert.alert(
        "Notifications",
        "Notification permission is managed by the system. Open the Settings app to turn it off.",
        [{ text: "OK" }]
      );
      return;
    }
    const res = await Notifications.requestPermissionsAsync();
    setPerm(res);
    if (res.granted) toast("Notifications enabled");
  };

  let desc =
    "Get a notification for new direct messages while Alex Messages is in the background.";
  // Denied once is not blocked: Android shows the prompt again until the
  // second denial (and expo-notifications reports "denied" after any ask).
  if (perm?.status === "denied" && !perm.canAskAgain) {
    desc = "Blocked by the system. Enable notifications for Alex Messages in the Settings app.";
  } else if (enabled) {
    desc = "You'll get a notification when a direct message arrives in the background.";
  }

  return (
    <SettingsPage>
      <Group footer={desc}>
        <Row
          icon="chatbubble"
          iconBg={colors.sage}
          title="Direct message alerts"
          accessory={
            <Switch
              value={enabled}
              onValueChange={() => void toggle()}
              trackColor={{ true: colors.sage, false: colors.line }}
              accessibilityLabel="Toggle notifications"
            />
          }
          last
        />
      </Group>
      {hasRingService && <RingSettings perm={perm} onPerm={setPerm} />}
    </SettingsPage>
  );
}

/**
 * Android: the ring service (modules/call-ringer) keeps a connection open
 * so calls ring while the app is closed. Three system settings decide
 * whether it works: notifications (a call rings as one; without them the
 * service doesn't run), full-screen calls over the lock screen (Android 14+
 * lets the user deny them), and battery optimization (Android restarts a
 * killed service by itself, but a phone maker's battery saver may stop it
 * for good, and Android 12+ lets the app restart it from the background
 * only when exempt).
 */
function RingSettings({ perm, onPerm }: { perm: Permission | null; onPerm: (p: Permission) => void }) {
  const { ringWhenClosed, setRingWhenClosed } = useSession();
  // Re-read the system settings when the user comes back from them.
  const [, setTick] = useState(0);
  useEffect(() => {
    const sub = AppState.addEventListener("change", (s) => {
      if (s === "active") setTick((n) => n + 1);
    });
    return () => sub.remove();
  }, []);
  const allowed = CallRinger.notificationsAllowed();
  const fullScreen = CallRinger.canUseFullScreenIntent();
  const unrestricted = CallRinger.isIgnoringBatteryOptimizations();

  const allowNotifications = useCallback(async () => {
    if (perm && !perm.granted && perm.canAskAgain) {
      const res = await Notifications.requestPermissionsAsync();
      onPerm(res);
      if (!res.granted) return;
      if (CallRinger.notificationsAllowed()) {
        // Starts the ring service.
        setRingWhenClosed(true);
        return;
      }
    }
    // Blocked for good, or the "Incoming calls" category is off.
    await CallRinger.openNotificationSettings();
  }, [perm, onPerm, setRingWhenClosed]);

  let footer =
    "Off: calls can stop ringing once you leave Alex Messages, because Android pauses apps in the background.";
  if (ringWhenClosed) {
    footer = allowed
      ? "Calls ring even when Alex Messages is closed. A silent “Ready for calls” notification stays in the notification shade while this is on."
      : "Notifications are off, so calls can't ring while Alex Messages is closed.";
  }

  return (
    <>
      <Group header="Calls" footer={footer}>
        <Row
          icon="call"
          iconBg={colors.sage}
          title="Ring when the app is closed"
          accessory={
            <Switch
              value={ringWhenClosed}
              onValueChange={setRingWhenClosed}
              trackColor={{ true: colors.sage, false: colors.line }}
              accessibilityLabel="Ring when the app is closed"
            />
          }
          last
        />
      </Group>
      {ringWhenClosed && (!allowed || !fullScreen || !unrestricted) && (
        <Group footer={ringFixFooter(fullScreen, unrestricted)} footerColor={colors.warn}>
          {!allowed && (
            <Row
              icon="notifications"
              iconBg={colors.warn}
              title="Allow notifications"
              onPress={() => void allowNotifications()}
              last={fullScreen && unrestricted}
            />
          )}
          {!fullScreen && (
            <Row
              icon="phone-portrait"
              iconBg={colors.warn}
              title="Allow full-screen calls"
              onPress={() => void CallRinger.openFullScreenIntentSettings()}
              last={unrestricted}
            />
          )}
          {!unrestricted && (
            <Row
              icon="battery-half"
              iconBg={colors.warn}
              title="Allow background use"
              onPress={() => void CallRinger.requestIgnoreBatteryOptimizations()}
              last
            />
          )}
        </Group>
      )}
    </>
  );
}

function ringFixFooter(fullScreen: boolean, unrestricted: boolean): string | undefined {
  const parts: string[] = [];
  if (!fullScreen) {
    parts.push("Full-screen calls are off: a call that rings on the lock screen shows only as a notification.");
  }
  if (!unrestricted) {
    parts.push(
      "Battery optimization is on, so Android or the phone maker's battery saver may shut down the connection that rings calls. Calls then ring only after you open Alex Messages again."
    );
  }
  return parts.length ? parts.join(" ") : undefined;
}
