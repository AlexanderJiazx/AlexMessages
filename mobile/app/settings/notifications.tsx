import React, { useState } from "react";
import { Alert, Switch } from "react-native";
import * as Notifications from "expo-notifications";
import { toast } from "../../src/session";
import { colors } from "../../src/theme";
import { Group, Row, SettingsPage } from "../../src/components/SettingsList";

export default function NotificationsPage() {
  const [perm, setPerm] = useState<Notifications.PermissionStatus | null>(null);

  React.useEffect(() => {
    void Notifications.getPermissionsAsync().then((p) => setPerm(p.status));
  }, []);

  const enabled = perm === "granted";
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
    setPerm(res.status);
    if (res.granted) toast("Notifications enabled");
  };

  let desc =
    "Get a notification for new direct messages while Alex Messages is in the background.";
  if (perm === "denied") {
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
    </SettingsPage>
  );
}
