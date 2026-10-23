import React, { useEffect } from "react";
import { Platform, StyleSheet } from "react-native";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import { KeyboardProvider } from "react-native-keyboard-controller";
import { SafeAreaProvider } from "react-native-safe-area-context";
import { Stack } from "expo-router";
import { SessionProvider } from "../src/session";
import { configureNotifications } from "../src/notify";
import { ActionSheetHost } from "../src/components/ActionSheet";
import { CallHost } from "../src/call/CallHost";
import { CALL_BG } from "../src/call/CallScreen";
import { colors } from "../src/theme";

/**
 * Root layout: providers + the Stack. Routes: index (redirect), login,
 * chat (adaptive home), conversation (narrow pushed chat), the
 * settings/new-chat/profile modal sheets, and the full-screen call screen
 * (opened and closed by CallHost).
 */
export default function RootLayout() {
  useEffect(() => {
    configureNotifications();
  }, []);

  return (
    <GestureHandlerRootView style={styles.root}>
      <KeyboardTracking>
        <SafeAreaProvider>
          <SessionProvider>
            <CallHost>
              {/* The status bar belongs to the screens: Info.plist turns on
                  view-controller-based status bar appearance (the image
                  viewer hides it), which React Native's StatusBar module
                  refuses. Every screen names its style, because once one
                  does (the call screen's light one), Android gives the
                  light style to every screen that doesn't. */}
              <Stack
                screenOptions={{
                  headerShown: false,
                  contentStyle: { backgroundColor: colors.bg },
                  statusBarStyle: "dark",
                }}
              >
                <Stack.Screen name="index" />
                <Stack.Screen name="login" />
                {/* We draw our own scroll-edge fades; UIKit's automatic effect
                    misplaces itself on the inverted message list. */}
                <Stack.Screen name="chat" options={{ scrollEdgeEffects: NO_EDGE_EFFECTS }} />
                <Stack.Screen name="conversation" options={{ scrollEdgeEffects: NO_EDGE_EFFECTS }} />
                <Stack.Screen
                  name="settings"
                  options={{ presentation: "modal" }}
                />
                <Stack.Screen
                  name="new-chat"
                  options={{ presentation: "modal" }}
                />
                <Stack.Screen
                  name="profile"
                  options={{ presentation: "modal" }}
                />
                {/* Over everything, modals included; a call ends with a
                    button, never a swipe. */}
                <Stack.Screen
                  name="call"
                  options={{
                    presentation: "fullScreenModal",
                    gestureEnabled: false,
                    statusBarStyle: "light",
                    contentStyle: { backgroundColor: CALL_BG },
                  }}
                />
              </Stack>
            </CallHost>
            <ActionSheetHost />
          </SessionProvider>
        </SafeAreaProvider>
      </KeyboardTracking>
    </GestureHandlerRootView>
  );
}

/**
 * Frame-by-frame keyboard tracking for the conversation's composer and list
 * (iOS only). On Android the provider takes over the window's insets even
 * when disabled, so Android keeps the stock keyboard handling.
 */
function KeyboardTracking({ children }: { children: React.ReactNode }) {
  if (Platform.OS !== "ios") return <>{children}</>;
  return <KeyboardProvider>{children}</KeyboardProvider>;
}

const NO_EDGE_EFFECTS = {
  top: "hidden",
  bottom: "hidden",
  left: "hidden",
  right: "hidden",
} as const;

const styles = StyleSheet.create({
  root: { flex: 1 },
});
