import React from "react";
import { Platform } from "react-native";
import { Stack } from "expo-router";
import { colors } from "../../src/theme";

/**
 * Settings is its own native stack inside the modal sheet, so every section
 * pushes with the platform transition (and iOS's edge-swipe back), and the
 * native header owns the title, back button, and toolbar items.
 */
export default function SettingsLayout() {
  return (
    <Stack
      screenOptions={{
        headerTintColor: colors.sageDeep,
        headerTitleStyle: { color: colors.ink },
        headerShadowVisible: false,
        // iOS 26: content scrolls under the glass bar; elsewhere a flat bar.
        headerTransparent: Platform.OS === "ios",
        headerStyle: Platform.OS === "ios" ? undefined : { backgroundColor: colors.grouped },
        contentStyle: { backgroundColor: colors.grouped },
      }}
    >
      <Stack.Screen name="index" options={{ title: "Settings" }} />
      <Stack.Screen name="profile" options={{ title: "Profile" }} />
      <Stack.Screen name="account" options={{ title: "Account" }} />
      <Stack.Screen name="notifications" options={{ title: "Notifications" }} />
      <Stack.Screen name="data" options={{ title: "Data" }} />
      <Stack.Screen name="admin" options={{ title: "Admin" }} />
    </Stack>
  );
}
