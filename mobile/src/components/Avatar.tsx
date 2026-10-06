import React from "react";
import { Image, StyleSheet, Text, View } from "react-native";
import { colorFor, initialsFor, type PublicUser } from "@alexmessages/shared";
import { colors } from "../theme";
import { useSession } from "../session";

type UserLike = Pick<PublicUser, "id" | "avatar" | "display_name" | "username">;

/**
 * Avatar circle: the user's photo when set, otherwise initials on their
 * deterministic peer color (same hash as web/Android).
 */
export function Avatar({ user, size = 40 }: { user: UserLike | null | undefined; size?: number }) {
  const { api } = useSession();
  const uri = user?.avatar ? api.url(user.avatar) : null;
  const bg = colorFor(user?.id);
  const users = user ? { [user.id]: user as PublicUser } : {};
  const initials = user ? initialsFor(users, user.id) : "?";
  return (
    <View
      style={[
        styles.circle,
        { width: size, height: size, borderRadius: size / 2, backgroundColor: bg },
      ]}
    >
      {uri ? (
        <Image
          source={{ uri }}
          style={{ width: size, height: size, borderRadius: size / 2 }}
          accessibilityLabel={`${user?.display_name} avatar`}
        />
      ) : (
        <Text style={[styles.initials, { fontSize: size * 0.38 }]}>{initials}</Text>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  circle: {
    alignItems: "center",
    justifyContent: "center",
    overflow: "hidden",
  },
  initials: {
    color: colors.surface,
    fontWeight: "700",
    letterSpacing: 0.5,
  },
});
