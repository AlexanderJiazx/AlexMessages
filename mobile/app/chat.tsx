import React, { useCallback, useEffect } from "react";
import { StyleSheet, Text, useWindowDimensions, View } from "react-native";
import { Redirect, useRouter } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import { onOpenChannelRequest, useChatState, useSession } from "../src/session";
import { colors, type, WIDE_BREAKPOINT } from "../src/theme";
import { DMList } from "../src/components/DMList";
import { ConversationView } from "../src/components/ConversationView";
import { Toasts } from "../src/components/Toasts";

/**
 * Chat home — the adaptive surface:
 *  - narrow (phones): the DM list; conversations push /conversation.
 *  - wide (tablets/desktop/web): rail + active conversation side by side.
 */
export default function ChatHome() {
  const { phase, store } = useSession();
  const s = useChatState();
  const router = useRouter();
  const { width } = useWindowDimensions();
  const wide = width >= WIDE_BREAKPOINT;

  const openChannel = useCallback(
    (channel: string) => {
      store.switchChannel(channel);
      if (!wide) {
        router.push({ pathname: "/conversation", params: { channel } });
      }
    },
    [wide, store, router]
  );

  // Notification taps → open that conversation.
  useEffect(
    () =>
      onOpenChannelRequest((channel) => {
        store.openChannelExternal(channel);
        if (!wide) {
          router.push({ pathname: "/conversation", params: { channel } });
        }
      }),
    [store, router, wide]
  );

  const onNewDm = useCallback(
    () => router.push({ pathname: "/new-chat", params: { wide: wide ? "1" : "0" } }),
    [router, wide]
  );
  const onOpenSettings = useCallback(() => router.push("/settings"), [router]);
  const onOpenProfile = useCallback(
    (uid: number) => router.push({ pathname: "/profile", params: { uid: String(uid) } }),
    [router]
  );

  if (phase === "login") return <Redirect href="/login" />;
  if (phase === "boot") return <View style={styles.root} />;

  if (wide) {
    return (
      <SafeAreaView style={styles.root} edges={["top", "bottom"]}>
        <View style={styles.row}>
          <View style={styles.rail}>
            <DMList
              activeChannel={s.activeChannel}
              onOpenChannel={openChannel}
              onNewDm={onNewDm}
              onOpenSettings={onOpenSettings}
            />
          </View>
          <View style={styles.main}>
            {s.activeChannel && store.channelExists(s.activeChannel) ? (
              <ConversationView
                channel={s.activeChannel}
                showBack={false}
                fullBleed={false}
                onOpenProfile={onOpenProfile}
              />
            ) : (
              <View style={styles.empty}>
                <Text style={styles.emptyTitle}>No Conversation Selected</Text>
                <Text style={styles.emptySub}>
                  Pick a conversation from the list, or start a new one with the compose button.
                </Text>
              </View>
            )}
          </View>
        </View>
        <Toasts />
      </SafeAreaView>
    );
  }

  return (
    <SafeAreaView style={styles.root} edges={["top"]}>
      <DMList
        activeChannel={s.activeChannel}
        onOpenChannel={openChannel}
        onNewDm={onNewDm}
        onOpenSettings={onOpenSettings}
      />
      <Toasts />
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  row: {
    flex: 1,
    flexDirection: "row",
  },
  rail: {
    width: 340,
    borderRightWidth: StyleSheet.hairlineWidth,
    borderRightColor: colors.separator,
  },
  main: {
    flex: 1,
  },
  empty: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
    padding: 32,
    backgroundColor: colors.bg,
  },
  emptyTitle: {
    ...type.title2,
    color: colors.ink,
    marginBottom: 8,
  },
  emptySub: {
    ...type.subhead,
    color: colors.muted,
    textAlign: "center",
    lineHeight: 20,
    maxWidth: 300,
  },
});
