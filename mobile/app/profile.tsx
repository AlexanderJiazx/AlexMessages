import React from "react";
import { Platform, Pressable, ScrollView, StyleSheet, Text, useWindowDimensions, View } from "react-native";
import { Stack, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import { nameFor } from "@alexmessages/shared";
import { useChatState, useSession } from "../src/session";
import { colors, type, WIDE_BREAKPOINT } from "../src/theme";
import { Avatar } from "../src/components/Avatar";
import { Icon, type IconName } from "../src/components/Icon";

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
  const canCall = store.calls.supported && !!u;
  // One call at a time: the call buttons wait until the current call ends.
  const inCall = !!s.call && s.call.phase !== "ended";

  const { width } = useWindowDimensions();
  const wide = width >= WIDE_BREAKPOINT;

  const navigation = useNavigation();
  const message = () => {
    if (!valid) return;
    // The screen under this card (it is a modal on the root stack).
    const st = navigation.getState() as
      | { index: number; routes: { name: string; params?: { channel?: string } }[] }
      | undefined;
    const below = st ? st.routes[st.index - 1] : undefined;
    store.openDM(uid);
    const channel = store.state.activeChannel;
    router.back();
    // Narrow → the conversation; wide selects in place via store state. A
    // card opened from that very thread just closes back onto it, instead
    // of stacking a second copy of the thread.
    const threadBelow = below?.name === "conversation" && below.params?.channel === channel;
    if (!wide && channel && !threadBelow) {
      // Small delay so the sheet dismisses before the push.
      setTimeout(() => router.push({ pathname: "/conversation", params: { channel } }), 60);
    }
  };

  return (
    <SafeAreaView style={styles.root} edges={["bottom"]}>
      {/* The native header (app/_layout.tsx). On iOS Done closes the card,
          as it closes Settings; Android closes it with the header's up
          arrow (and its toolbar would need image icons). */}
      {Platform.OS === "ios" && (
        <Stack.Toolbar placement="right">
          <Stack.Toolbar.Button variant="done" onPress={() => router.back()} accessibilityLabel="Done">
            Done
          </Stack.Toolbar.Button>
        </Stack.Toolbar>
      )}
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
            {/* The call screen opens over this card, and ending the call
                comes back to it. */}
            {canCall && (
              <>
                <Action
                  icon="call"
                  label="Voice Call"
                  a11y={`Voice call ${u.display_name || u.username}`}
                  disabled={inCall}
                  onPress={() => store.startCall(uid, "audio")}
                />
                <Action
                  icon="videocam"
                  label="Video Call"
                  a11y={`Video call ${u.display_name || u.username}`}
                  disabled={inCall}
                  onPress={() => store.startCall(uid, "video")}
                />
              </>
            )}
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
  disabled,
  onPress,
}: {
  icon: IconName;
  label: string;
  a11y?: string;
  disabled?: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      style={({ pressed }) => [styles.action, pressed && { opacity: 0.7 }, disabled && styles.actionOff]}
      onPress={onPress}
      disabled={disabled}
      accessibilityRole="button"
      accessibilityLabel={a11y || label}
      accessibilityState={{ disabled: !!disabled }}
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
  body: {
    paddingHorizontal: 16,
    paddingTop: 8,
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
    backgroundColor: colors.online,
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
  actionOff: {
    opacity: 0.4,
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
