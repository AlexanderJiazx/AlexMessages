import React from "react";
import { Pressable, ScrollView, StyleSheet, Text, useWindowDimensions, View } from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import { nameFor } from "@alexmessages/shared";
import { useChatState, useSession } from "../src/session";
import { colors, fontDisplay, radius, WIDE_BREAKPOINT } from "../src/theme";
import { Avatar } from "../src/components/Avatar";
import { Icon } from "../src/components/Icon";

/** Read-only peer profile sheet: avatar, name, bio, contact toggle, message. */
export default function ProfileScreen() {
  const { store } = useSession();
  const s = useChatState();
  const router = useRouter();
  const { uid: uidParam } = useLocalSearchParams<{ uid: string }>();
  const uid = Number(uidParam);
  const u = Number.isFinite(uid) ? s.users[uid] : null;
  const online = Number.isFinite(uid) && s.online.has(uid);
  const isContact = Number.isFinite(uid) && s.contacts.has(uid);
  const isSelf = s.me?.id === uid;

  const { width } = useWindowDimensions();
  const wide = width >= WIDE_BREAKPOINT;

  const message = () => {
    if (!Number.isFinite(uid)) return;
    store.openDM(uid);
    const channel = store.state.activeChannel;
    router.back();
    if (!wide && channel) {
      // Narrow → pushed conversation; wide selects in place via store state.
      setTimeout(() => router.push({ pathname: "/conversation", params: { channel } }), 60);
    }
  };

  return (
    <SafeAreaView style={styles.root} edges={["top", "bottom"]}>
      <View style={styles.header}>
        <Text style={styles.title}>Profile</Text>
        <Pressable onPress={() => router.back()} accessibilityLabel="Close" hitSlop={10}>
          <Icon name="close" size={22} color={colors.muted} />
        </Pressable>
      </View>
      <ScrollView contentContainerStyle={styles.body}>
        <View style={styles.banner} />
        <View style={styles.profileRow}>
          <Avatar user={u} size={72} />
          <View style={styles.nameBlock}>
            <Text style={styles.name}>{u?.display_name || nameFor(s.users, uid)}</Text>
            <Text style={styles.handle}>@{u?.username || "?"}</Text>
            <View style={styles.statusRow}>
              <View style={[styles.dot, online && styles.dotOn]} />
              <Text style={styles.statusText}>{online ? "online" : "offline"}</Text>
            </View>
          </View>
        </View>

        <Text style={styles.label}>Bio</Text>
        <Text style={[styles.bio, !u?.bio && styles.bioEmpty]}>
          {u?.bio || "No bio yet."}
        </Text>

        {!isSelf && (
          <View style={styles.btnRow}>
            <Pressable style={[styles.btn, styles.btnPrimary]} onPress={message} accessibilityRole="button">
              <Icon name="mail-outline" size={15} color={colors.surface} />
              <Text style={styles.btnPrimaryText}>Message</Text>
            </Pressable>
            {Number.isFinite(uid) && (
              <Pressable
                style={styles.btn}
                onPress={() => void store.toggleContact(uid)}
                accessibilityRole="button"
              >
                <Text style={styles.btnText}>
                  {isContact ? "Remove from contacts" : "Add to contacts"}
                </Text>
              </Pressable>
            )}
          </View>
        )}

        <Text style={[styles.label, { marginTop: 22 }]}>Account</Text>
        <View style={styles.card}>
          <Row k="Username" v={`@${u?.username || "?"}`} />
          <Row k="User ID" v={`#${Number.isFinite(uid) ? uid : "?"}`} />
          <Row
            k="Status"
            v={online ? "Online" : "Offline"}
            vColor={online ? colors.sageDeep : colors.muted}
          />
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

function Row({ k, v, vColor }: { k: string; v: string; vColor?: string }) {
  return (
    <View style={styles.row}>
      <Text style={styles.rowK}>{k}</Text>
      <Text style={[styles.rowV, vColor ? { color: vColor } : null]}>{v}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.paper,
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 20,
    paddingTop: 20,
    paddingBottom: 4,
  },
  title: {
    fontFamily: fontDisplay,
    fontSize: 26,
    color: colors.ink,
  },
  body: {
    padding: 20,
  },
  banner: {
    height: 54,
    borderRadius: radius.m,
    backgroundColor: colors.sageSoft,
    marginBottom: -24,
  },
  profileRow: {
    flexDirection: "row",
    alignItems: "flex-end",
    gap: 14,
    paddingHorizontal: 12,
  },
  nameBlock: {
    paddingBottom: 2,
  },
  name: {
    fontSize: 22,
    fontWeight: "800",
    color: colors.ink,
  },
  handle: {
    fontSize: 13.5,
    color: colors.muted,
    marginTop: 1,
  },
  statusRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 5,
    marginTop: 5,
  },
  dot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: colors.line,
  },
  dotOn: {
    backgroundColor: colors.sage,
  },
  statusText: {
    fontSize: 12,
    color: colors.muted,
  },
  label: {
    fontSize: 11,
    fontWeight: "700",
    letterSpacing: 1,
    textTransform: "uppercase",
    color: colors.faint,
    marginTop: 24,
    marginBottom: 8,
  },
  bio: {
    fontSize: 15,
    lineHeight: 22,
    color: colors.ink2,
    backgroundColor: colors.surface,
    borderRadius: radius.m,
    padding: 14,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
  },
  bioEmpty: {
    color: colors.faint,
    fontStyle: "italic",
  },
  btnRow: {
    flexDirection: "row",
    gap: 10,
    marginTop: 18,
  },
  btn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    backgroundColor: colors.surface,
    borderRadius: radius.pill,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
    paddingHorizontal: 16,
    paddingVertical: 10,
  },
  btnPrimary: {
    backgroundColor: colors.sage,
    borderColor: colors.sage,
  },
  btnText: {
    fontSize: 14,
    fontWeight: "600",
    color: colors.ink2,
  },
  btnPrimaryText: {
    fontSize: 14,
    fontWeight: "700",
    color: colors.surface,
  },
  card: {
    backgroundColor: colors.surface,
    borderRadius: radius.m,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
    paddingHorizontal: 14,
  },
  row: {
    flexDirection: "row",
    justifyContent: "space-between",
    paddingVertical: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.line2,
  },
  rowK: {
    fontSize: 13.5,
    color: colors.muted,
  },
  rowV: {
    fontSize: 13.5,
    fontWeight: "600",
    color: colors.ink,
  },
});
