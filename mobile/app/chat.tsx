import React, { useCallback, useEffect, useRef } from "react";
import { StyleSheet, Text, useWindowDimensions, View } from "react-native";
import { Redirect, useFocusEffect, useNavigationContainerRef, useRouter } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import { onOpenChannelRequest, useChatState, useSession } from "../src/session";
import { colors, type, WIDE_BREAKPOINT } from "../src/theme";
import { showConversation } from "../src/nav";
import { DMList } from "../src/components/DMList";
import { ConversationView } from "../src/components/ConversationView";

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

  // One push per tap: a second tap (or a double tap) on a row while the
  // first push is still on its way would stack the same thread twice. The
  // lock opens again once this screen has focus again (the user came back).
  const pushing = useRef(false);
  useFocusEffect(
    useCallback(() => {
      pushing.current = false;
    }, [])
  );
  const openChannel = useCallback(
    (channel: string) => {
      if (!wide && pushing.current) return;
      store.switchChannel(channel);
      if (!wide) {
        pushing.current = true;
        router.push({ pathname: "/conversation", params: { channel } });
      }
    },
    [wide, store, router]
  );

  // Notification taps → open that conversation (a tap that started the
  // app waits in notify.ts until the session is ready). A channel this
  // account is not part of (left over from another account) is ignored.
  // Open sheets close first, so the thread isn't pushed above them; during
  // a live call nothing moves (the call screen or its tile stays where it
  // is), and the thread is only selected.
  const navRef = useNavigationContainerRef();
  useEffect(() => {
    if (phase !== "ready") return;
    return onOpenChannelRequest((channel) => {
      if (store.peerOf(channel) == null) return;
      store.openChannelExternal(channel);
      const call = store.state.call;
      if (call && call.phase !== "ended") return;
      showConversation(navRef, channel, !wide);
    });
  }, [phase, store, navRef, wide]);

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
              // Keyed: a draft, a reply, or a recording stays with its
              // own conversation instead of carrying over to the next one.
              <ConversationView
                key={s.activeChannel}
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
      </SafeAreaView>
    );
  }

  return (
    <SafeAreaView style={styles.root} edges={["top"]}>
      {/* No "selected" row on phones: the thread is pushed, and tinting the
          tapped row green would flash on top of the grey press highlight. */}
      <DMList
        activeChannel={null}
        onOpenChannel={openChannel}
        onNewDm={onNewDm}
        onOpenSettings={onOpenSettings}
      />
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
