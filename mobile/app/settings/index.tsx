import React from "react";
import { Alert, Pressable, Text, View } from "react-native";
import { Stack, useRouter, type Href } from "expo-router";
import { useChatState, useSession } from "../../src/session";
import { colors } from "../../src/theme";
import { Avatar } from "../../src/components/Avatar";
import { Icon } from "../../src/components/Icon";
import { Group, Row, SettingsPage, styles } from "../../src/components/SettingsList";

/**
 * Settings root — an iOS Settings-style inset-grouped list: the profile
 * card, the sections (each a pushed native stack screen), and Log Out.
 */
export default function SettingsScreen() {
  const s = useChatState();
  const { serverUrl, logout } = useSession();
  const router = useRouter();
  const me = s.me;
  const open = (href: Href) => router.push(href);

  const confirmLogout = () => {
    Alert.alert(
      "Log Out",
      "Log out from all of your devices? This ends every active session, including this one.",
      [
        { text: "Cancel", style: "cancel" },
        { text: "Log Out", style: "destructive", onPress: () => void logout() },
      ]
    );
  };

  return (
    <>
      <Stack.Toolbar placement="right">
        <Stack.Toolbar.Button variant="done" onPress={() => router.back()} accessibilityLabel="Done">
          Done
        </Stack.Toolbar.Button>
      </Stack.Toolbar>
      <SettingsPage>
        {me && (
          <>
            <Group>
              <Pressable
                style={({ pressed }) => [styles.meCard, pressed && styles.pressed]}
                onPress={() => open("/settings/profile")}
                accessibilityRole="button"
                accessibilityLabel="Profile"
              >
                <Avatar user={me} size={60} />
                <View style={styles.flex}>
                  <Text style={styles.meName} numberOfLines={1}>
                    {me.display_name || me.username}
                  </Text>
                  <Text style={styles.meSub} numberOfLines={1}>
                    @{me.username} · Photo, name & bio
                  </Text>
                </View>
                <Icon name="chevron-forward" size={16} color={colors.faint} />
              </Pressable>
            </Group>

            <Group>
              <Row
                icon="shield-checkmark"
                iconBg={colors.sage}
                title="Account"
                onPress={() => open("/settings/account")}
              />
              <Row
                icon="notifications"
                iconBg="#D6694A"
                title="Notifications"
                onPress={() => open("/settings/notifications")}
              />
              <Row
                icon="server"
                iconBg="#5B7FA6"
                title="Data"
                onPress={() => open("/settings/data")}
                last={!s.isAdmin}
              />
              {s.isAdmin && (
                <Row
                  icon="key"
                  iconBg={colors.ink2}
                  title="Admin"
                  onPress={() => open("/settings/admin")}
                  last
                />
              )}
            </Group>

            <Group>
              <Row title="Log Out" destructive center onPress={confirmLogout} last />
            </Group>

            <Text style={styles.colophon}>
              Alex Messages · {serverUrl.replace(/^https?:\/\//, "").replace(/\/+$/, "")}
            </Text>
          </>
        )}
      </SettingsPage>
    </>
  );
}
