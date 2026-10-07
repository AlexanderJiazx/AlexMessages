import React from "react";
import { StyleSheet, Text, View } from "react-native";
import { isRemoteUser } from "@alexmessages/shared";
import type { PublicUser } from "@alexmessages/shared";
import { colors } from "../theme";

/** Small "Matrix" pill marking a bridged remote user. Null for local users. */
export function MatrixBadge({ user }: { user: PublicUser | null | undefined }) {
  if (!isRemoteUser(user)) return null;
  return (
    <View style={styles.badge} accessibilityLabel="Matrix user">
      <Text style={styles.text}>Matrix</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  badge: {
    paddingHorizontal: 7,
    paddingVertical: 1.5,
    borderRadius: 999,
    backgroundColor: colors.sageTint,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.sageSoft,
  },
  text: {
    fontSize: 10,
    fontWeight: "600",
    letterSpacing: 0.5,
    textTransform: "uppercase",
    color: colors.sageDeep,
  },
});
