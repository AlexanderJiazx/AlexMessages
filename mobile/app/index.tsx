import React from "react";
import { ActivityIndicator, StyleSheet, Text, View } from "react-native";
import { Redirect } from "expo-router";
import { useSession } from "../src/session";
import { colors, fontDisplay } from "../src/theme";

/** Boot gate: restoring session → spinner; then route to chat or login. */
export default function Index() {
  const { phase } = useSession();
  if (phase === "ready") return <Redirect href="/chat" />;
  if (phase === "login") return <Redirect href="/login" />;
  return (
    <View style={styles.boot}>
      <Text style={styles.brand}>Alex Messages</Text>
      <ActivityIndicator color={colors.sage} />
    </View>
  );
}

const styles = StyleSheet.create({
  boot: {
    flex: 1,
    backgroundColor: colors.bg,
    alignItems: "center",
    justifyContent: "center",
    gap: 18,
  },
  brand: {
    fontFamily: fontDisplay,
    fontSize: 32,
    color: colors.ink,
  },
});
