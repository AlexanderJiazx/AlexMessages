import React from "react";
import { Pressable, ScrollView, StyleSheet, Text, useWindowDimensions, View } from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import { nameFor } from "@alexmessages/shared";
import { useChatState, useSession } from "../src/session";
import { colors, type, WIDE_BREAKPOINT } from "../src/theme";
import { Avatar } from "../src/components/Avatar";
import { Icon, type IconName } from "../src/components/Icon";
import { GlassIconButton } from "../src/components/Glass";

/** Read-only peer profile — an iOS contact card: hero, actions, details. */
export default function ProfileScreen() {
  const { store } = useSession();
  const s = useChatState();
  const router = useRouter();
  const { uid: uidParam } = useLocalSearchParams<{ uid: string }>();
  const uid = Number(uidParam);
  const valid = Number.isFinite(uid);
  const u = valid ? s.users[uid] : null;
  const online = valid && s.online.has(uid);
  const isContact = valid && s.contacts.has(uid);
  const isSelf = s.me?.id === uid;

  const { width } = useWindowDimensions();
  const wide = width >= WIDE_BREAKPOINT;

  const message = () => {
    if (!valid) return;
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
      <View style={styles.nav}>
        <GlassIconButton icon="close" label="Close" onPress={() => router.back()} size={40} />
      </View>
      <ScrollView contentContainerStyle={styles.body}>
        <View style={styles.hero}>
          <Avatar user={u} size={112} />
          <Text style={styles.name}>{u?.display_name || nameFor(s.users, uid)}</Text>
          <View style={styles.statusRow}>
            <View style={[styles.dot, online && styles.dotOn]} />
            <Text style={styles.statusText}>
              @{u?.username || "?"} · {online ? "Online" : "Offline"}
            </Text>
          </View>
        </View>

        {!isSelf && valid && (
          <View style={styles.actions}>
            <Action icon="chatbubble" label="Message" onPress={message} />
            <Action
              icon={isContact ? "person-remove" : "person-add"}
              label={isContact ? "Remove" : "Add Contact"}
              a11y={isContact ? "Remove from contacts" : "Add to contacts"}
              onPress={() => void store.toggleContact(uid)}
            />
          </View>
        )}

        <View style={styles.card}>
          <Text style={styles.cardLabel}>Bio</Text>
          <Text style={[styles.bio, !u?.bio && styles.bioEmpty]}>{u?.bio || "No bio yet."}</Text>
        </View>

        <View style={styles.card}>
          <Detail k="Username" v={`@${u?.username || "?"}`} />
          <Detail k="User ID" v={`#${valid ? uid : "?"}`} last />
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

function Action({
  icon,
  label,
  a11y,
  onPress,
}: {
  icon: IconName;
  label: string;
  a11y?: string;
  onPress: () => void;
}) {
  return (
    <Pressable
      style={({ pressed }) => [styles.action, pressed && { opacity: 0.7 }]}
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={a11y || label}
    >
      <Icon name={icon} size={22} color={colors.sage} />
      <Text style={styles.actionLabel}>{label}</Text>
    </Pressable>
  );
}

function Detail({ k, v, last }: { k: string; v: string; last?: boolean }) {
  return (
    <View style={[styles.detail, !last && styles.detailSep]}>
      <Text style={styles.cardLabel}>{k}</Text>
      <Text style={styles.detailValue}>{v}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.grouped,
  },
  nav: {
    alignItems: "flex-end",
    paddingHorizontal: 14,
    paddingTop: 12,
  },
  body: {
    paddingHorizontal: 16,
    paddingBottom: 40,
  },
  hero: {
    alignItems: "center",
    marginTop: 4,
    marginBottom: 20,
  },
  name: {
    ...type.largeTitle,
    fontSize: 28,
    color: colors.ink,
    marginTop: 14,
    textAlign: "center",
  },
  statusRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    marginTop: 4,
  },
  dot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: colors.faint,
  },
  dotOn: {
    backgroundColor: "#4CB86E",
  },
  statusText: {
    ...type.subhead,
    color: colors.muted,
  },
  actions: {
    flexDirection: "row",
    gap: 10,
    marginBottom: 16,
  },
  action: {
    flex: 1,
    alignItems: "center",
    gap: 4,
    paddingVertical: 12,
    borderRadius: 14,
    backgroundColor: colors.surface,
  },
  actionLabel: {
    ...type.caption,
    fontWeight: "500",
    color: colors.sageDeep,
  },
  card: {
    backgroundColor: colors.surface,
    borderRadius: 14,
    paddingHorizontal: 16,
    paddingVertical: 12,
    marginBottom: 14,
  },
  cardLabel: {
    ...type.footnote,
    color: colors.muted,
  },
  bio: {
    ...type.body,
    color: colors.ink,
    marginTop: 3,
    lineHeight: 23,
  },
  bioEmpty: {
    color: colors.faint,
  },
  detail: {
    paddingVertical: 6,
  },
  detailSep: {
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.separator,
    paddingBottom: 10,
    marginBottom: 4,
  },
  detailValue: {
    ...type.body,
    color: colors.ink,
    marginTop: 2,
  },
});
