import React, { useCallback } from "react";
import { StyleSheet, View } from "react-native";
import { Redirect, useLocalSearchParams, useRouter } from "expo-router";
import { isDM } from "@alexmessages/shared";
import { useSession } from "../src/session";
import { colors } from "../src/theme";
import { ConversationView } from "../src/components/ConversationView";
import { Toasts } from "../src/components/Toasts";

/** Pushed conversation screen — the narrow-layout chat surface. */
export default function ConversationScreen() {
  const { phase } = useSession();
  const router = useRouter();
  const { channel } = useLocalSearchParams<{ channel: string }>();

  const onOpenProfile = useCallback(
    (uid: number) => router.push({ pathname: "/profile", params: { uid: String(uid) } }),
    [router]
  );
  const onBack = useCallback(() => router.back(), [router]);

  if (phase === "login") return <Redirect href="/login" />;
  if (!channel || !isDM(channel)) return <Redirect href="/chat" />;

  return (
    <View style={styles.root}>
      <ConversationView
        channel={channel}
        showBack
        fullBleed
        onBack={onBack}
        onOpenProfile={onOpenProfile}
      />
      <Toasts />
    </View>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.bg,
  },
});
