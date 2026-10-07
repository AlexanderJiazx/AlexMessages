import React from "react";
import { ActivityIndicator, StyleSheet, Text, View } from "react-native";
import { Redirect } from "expo-router";
import { Image } from "expo-image";
import { useSession } from "../src/session";
import { colors, fontDisplay } from "../src/theme";

const LOGO = require("../assets/icon.png");

/** Boot gate: restoring session → spinner; then route to chat or login. */
export default function Index() {
  const { phase } = useSession();
  if (phase === "ready") return <Redirect href="/chat" />;
  if (phase === "login") return <Redirect href="/login" />;
  return (
    <View style={styles.boot}>
      <Image source={LOGO} style={styles.logo} />
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
  logo: {
    width: 84,
    height: 84,
    borderRadius: 22,
  },
  brand: {
    fontFamily: fontDisplay,
    fontSize: 32,
    color: colors.ink,
  },
});
