import React, { useState } from "react";
import { ActivityIndicator } from "react-native";
import * as FileSystem from "expo-file-system";
import * as Sharing from "expo-sharing";
import { toast, useSession } from "../../src/session";
import { colors } from "../../src/theme";
import { Group, Row, SettingsPage } from "../../src/components/SettingsList";

export default function DataPage() {
  const { api } = useSession();
  const [busy, setBusy] = useState(false);

  const doExport = async () => {
    setBusy(true);
    try {
      const data = await api.exportData();
      const path = `${FileSystem.Paths.cache}alex-messages-export.json`;
      const file = new FileSystem.File(path);
      file.write(JSON.stringify(data, null, 2));
      if (await Sharing.isAvailableAsync()) {
        await Sharing.shareAsync(file.uri, { mimeType: "application/json", dialogTitle: "Export my data" });
      } else {
        toast(`Saved to ${file.uri}`);
      }
    } catch {
      toast("Export failed", true);
    } finally {
      setBusy(false);
    }
  };

  return (
    <SettingsPage>
      <Group footer="A copy of your account, contacts, and full message history as a JSON file.">
        <Row
          icon="download"
          iconBg="#5B7FA6"
          title="Export my data"
          onPress={busy ? undefined : () => void doExport()}
          accessory={busy ? <ActivityIndicator size="small" color={colors.muted} /> : undefined}
          chevron={!busy}
          last
        />
      </Group>
    </SettingsPage>
  );
}
