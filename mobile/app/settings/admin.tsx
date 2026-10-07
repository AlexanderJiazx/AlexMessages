import React from "react";
import * as WebBrowser from "expo-web-browser";
import { useSession } from "../../src/session";
import { colors } from "../../src/theme";
import { Icon } from "../../src/components/Icon";
import { Group, Row, SettingsPage } from "../../src/components/SettingsList";

export default function AdminPage() {
  const { serverUrl } = useSession();
  const url = serverUrl.replace(/:(\d+)$/, ":8001");
  return (
    <SettingsPage>
      <Group footer="You have administrator access. The control panel opens in the browser.">
        <Row
          icon="key"
          iconBg={colors.ink2}
          title="Open Admin Panel"
          onPress={() => void WebBrowser.openBrowserAsync(url)}
          accessory={<Icon name="open-outline" size={17} color={colors.faint} />}
          chevron={false}
          last
        />
      </Group>
    </SettingsPage>
  );
}
