import React, { useCallback, useMemo, useRef } from "react";
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { formatDate, isoDate, type ChatMessage } from "@alexmessages/shared";
import { useChatState, useSession } from "../session";
import { colors, fontDisplay } from "../theme";
import { Bubble } from "./Bubble";
import { Icon } from "./Icon";

type Row =
  | { kind: "sep"; id: string; label: string }
  | { kind: "msg"; id: string; m: ChatMessage; prev: ChatMessage | null }
  | { kind: "loading"; id: string };

/**
 * The message stream — an inverted FlatList (newest at the bottom) with day
 * separators, lazy history paging when scrolled to the oldest edge, and
 * reply-jump scrolling. Port of the web Stream component.
 */
export function MessageList({
  channel,
  onOpenProfile,
  onImagePress,
  emptyNode,
}: {
  channel: string | null;
  onOpenProfile: (uid: number) => void;
  onImagePress: (url: string) => void;
  emptyNode?: React.ReactNode;
}) {
  const s = useChatState();
  const { store } = useSession();
  const listRef = useRef<FlatList<Row>>(null);

  // History arrays are mutated in place by the store — store.version bumps on
  // every emit, so memo deps key off it rather than object identity.
  const version = store.version;
  const msgs = useMemo(
    () => (channel ? s.history[channel] : undefined) || [],
    [channel, s.history, version]
  );

  // Build display rows oldest→newest, then reverse for the inverted list.
  const rows = useMemo<Row[]>(() => {
    const out: Row[] = [];
    let lastDate = "";
    let prev: ChatMessage | null = null;
    for (const m of msgs) {
      const d = isoDate(m.created_at);
      if (d && d !== lastDate) {
        out.push({ kind: "sep", id: `sep-${d}`, label: formatDate(d) });
        lastDate = d;
      }
      out.push({ kind: "msg", id: m.id, m, prev });
      if (m.type === "message") prev = m;
    }
    if (channel && s.historyHasMore[channel]) {
      out.push({ kind: "loading", id: "loading-older" });
    }
    return out.reverse();
  }, [msgs, channel, s.historyHasMore, version]);

  const onEndReached = useCallback(() => {
    // Inverted: "end" = oldest visible edge → load the previous page.
    if (channel && s.historyHasMore[channel] && !s.historyLoading[channel]) {
      void store.loadOlder(channel);
    }
  }, [channel, s.historyHasMore, s.historyLoading, store]);

  const jumpTo = useCallback(
    (id: string) => {
      const idx = rows.findIndex((r) => r.kind === "msg" && r.m.id === id);
      if (idx >= 0) {
        listRef.current?.scrollToIndex({ index: idx, viewPosition: 0.5, animated: true });
      }
    },
    [rows]
  );

  if (!channel) {
    return (
      <View style={styles.emptyWrap}>
        {emptyNode ?? (
          <>
            <Text style={styles.emptyTitle}>No conversation selected</Text>
            <Text style={styles.emptySub}>
              Pick a conversation from the list, or start a new one with the compose button.
            </Text>
          </>
        )}
      </View>
    );
  }

  return (
    <FlatList
      ref={listRef}
      data={rows}
      inverted
      keyExtractor={(r) => r.id}
      renderItem={({ item }) => {
        if (item.kind === "sep") {
          return (
            <View style={styles.daySep}>
              <Text style={styles.daySepText}>{item.label}</Text>
            </View>
          );
        }
        if (item.kind === "loading") {
          return (
            <View style={styles.loadingRow}>
              <ActivityIndicator size="small" color={colors.sage} />
              <Text style={styles.loadingText}>Loading older messages…</Text>
            </View>
          );
        }
        return (
          <Bubble
            m={item.m}
            prev={item.prev}
            allMsgs={msgs}
            onOpenProfile={onOpenProfile}
            onImagePress={onImagePress}
            onJumpTo={jumpTo}
          />
        );
      }}
      onEndReached={onEndReached}
      onEndReachedThreshold={0.3}
      contentContainerStyle={styles.list}
      keyboardDismissMode="interactive"
      keyboardShouldPersistTaps="handled"
      onScrollToIndexFailed={({ index }) => {
        // Item not yet laid out — retry after a beat.
        setTimeout(
          () => listRef.current?.scrollToIndex({ index, viewPosition: 0.5, animated: false }),
          220
        );
      }}
      accessibilityLabel="Messages"
    />
  );
}

/** Conversation header: back button (narrow), peer name, presence chip. */
export function Topbar({
  channel,
  onBack,
  onOpenProfile,
  showBack,
}: {
  channel: string;
  onBack?: () => void;
  onOpenProfile: (uid: number) => void;
  showBack: boolean;
}) {
  const s = useChatState();
  const { store } = useSession();
  const peerId = store.peerOf(channel);
  const peer = store.userFor(peerId);
  const online = peerId != null && s.online.has(peerId);
  const name = peer?.display_name || peer?.username || "…";

  return (
    <View style={styles.topbar}>
      {showBack && (
        <Pressable onPress={onBack} accessibilityLabel="Back" hitSlop={10} style={styles.backBtn}>
          <IconBack />
        </Pressable>
      )}
      <Pressable
        style={styles.titleWrap}
        onPress={() => peerId != null && onOpenProfile(peerId)}
        accessibilityLabel={`Open ${name}'s profile`}
      >
        <Text style={styles.topTitle} numberOfLines={1}>
          {name}
        </Text>
        <View style={styles.presenceRow}>
          <View style={[styles.presenceDot, online && styles.presenceDotOn]} />
          <Text style={styles.presenceText}>{online ? "online" : "offline"}</Text>
        </View>
      </Pressable>
      <View style={styles.topbarRight} />
    </View>
  );
}

function IconBack() {
  return <Icon name="chevron-back" size={24} color={colors.ink2} />;
}

const styles = StyleSheet.create({
  list: {
    paddingVertical: 10,
  },
  daySep: {
    alignItems: "center",
    marginVertical: 12,
  },
  daySepText: {
    fontSize: 11,
    fontWeight: "700",
    letterSpacing: 0.8,
    textTransform: "uppercase",
    color: colors.faint,
    backgroundColor: colors.paper,
    paddingHorizontal: 10,
    paddingVertical: 3,
    borderRadius: 9,
    overflow: "hidden",
  },
  loadingRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    gap: 8,
    paddingVertical: 14,
  },
  loadingText: {
    fontSize: 12,
    color: colors.muted,
  },
  emptyWrap: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
    padding: 32,
  },
  emptyTitle: {
    fontFamily: fontDisplay,
    fontSize: 26,
    color: colors.ink,
    marginBottom: 8,
  },
  emptySub: {
    fontSize: 14,
    color: colors.muted,
    textAlign: "center",
    lineHeight: 20,
    maxWidth: 280,
  },
  topbar: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 8,
    paddingVertical: 8,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.line,
    backgroundColor: colors.surface,
    minHeight: 56,
  },
  backBtn: {
    width: 36,
    height: 36,
    alignItems: "center",
    justifyContent: "center",
  },
  titleWrap: {
    flex: 1,
    alignItems: "center",
    gap: 1,
  },
  topTitle: {
    fontSize: 16,
    fontWeight: "700",
    color: colors.ink,
    maxWidth: "92%",
  },
  presenceRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 5,
  },
  presenceDot: {
    width: 7,
    height: 7,
    borderRadius: 4,
    backgroundColor: colors.line,
  },
  presenceDotOn: {
    backgroundColor: colors.sage,
  },
  presenceText: {
    fontSize: 11.5,
    color: colors.muted,
  },
  topbarRight: {
    width: 36,
  },
});
