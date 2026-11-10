import React, { useEffect, useState } from "react";
import { ActivityIndicator, StyleSheet, Text, View } from "react-native";
import { Redirect } from "expo-router";
import { Image } from "expo-image";
import { loadIconFont } from "../src/components/Icon";
import { useSession } from "../src/session";
import { colors, fontDisplay } from "../src/theme";

const LOGO = require("../assets/icon.png");

/** Boot gate: restoring session and loading the icon font → spinner; then
 *  route to chat or login, whose first frame then has its icons. */
export default function Index() {
  const { phase } = useSession();
  const [iconsReady, setIconsReady] = useState(false);
  useEffect(() => {
    void loadIconFont().then(() => setIconsReady(true));
  }, []);
  if (iconsReady && phase === "ready") return <Redirect href="/chat" />;
  if (iconsReady && phase === "login") return <Redirect href="/login" />;
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
