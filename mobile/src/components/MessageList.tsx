import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  StyleSheet,
  Text,
  useWindowDimensions,
  View,
  type LayoutChangeEvent,
  type NativeScrollEvent,
  type NativeSyntheticEvent,
} from "react-native";
import { fmtStampLabel, type ChatMessage } from "@alexmessages/shared";
import { useChatState, useSession } from "../session";
import { colors, type } from "../theme";
import { Bubble } from "./Bubble";
import type { ImagePressHandler } from "./attachments";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { GlassIconButton, GlassSurface } from "./Glass";
import { NoScrollEdgeEffects } from "./ScrollEdge";

/** Consecutive messages from one sender within this window form a group. */
const GROUP_GAP_S = 5 * 60;
/** A centered timestamp is shown when the conversation pauses this long. */
const STAMP_GAP_S = 60 * 60;

type Row =
  | { kind: "stamp"; id: string; label: string }
  | { kind: "msg"; id: string; m: ChatMessage; first: boolean; last: boolean }
  | { kind: "loading"; id: string };

/**
 * The message stream — an inverted FlatList, the standard chat list: rows are
 * newest-first and drawn from the bottom, so a thread opens on its newest
 * message with nothing to scroll, new messages land at the bottom, and older
 * history pages in at the top without moving what's on screen. Grouped into
 * iMessage-style runs with centered timestamps at conversation pauses, plus
 * reply-jump scrolling. `topInset`/`bottomInset` reserve room for the
 * floating header + composer so content scrolls underneath them.
 *
 * The scroll view is flipped (offset 0 is the bottom), so every "top" and
 * "bottom" handed to it is swapped: paddingTop/contentInset.top sit at the
 * bottom of the screen and paddingBottom/contentInset.bottom at the top.
 */
export function MessageList({
  channel,
  onImagePress,
  topInset,
  bottomInset,
  topSlack = 0,
}: {
  channel: string;
  onImagePress: ImagePressHandler;
  topInset: number;
  bottomInset: number;
  /**
   * Extra scroll room above the content: how far the list is lifted off the
   * top of the screen (iOS keyboard), so the oldest messages stay reachable.
   */
  topSlack?: number;
}) {
  const s = useChatState();
  const { store } = useSession();
  const listRef = useRef<FlatList<Row>>(null);

  // History arrays are mutated in place by the store — store.version bumps on
  // every emit, so memo deps key off it rather than object identity.
  const version = store.version;
  const msgs = useMemo(() => s.history[channel] || [], [channel, s.history, version]);

  // Display rows, built oldest → newest, then reversed for the inverted list
  // (row 0 sits at the bottom).
  const rows = useMemo<Row[]>(() => {
    const out: Row[] = [];
    const real = msgs;
    if (s.historyHasMore[channel]) out.push({ kind: "loading", id: "loading-older" });
    for (let i = 0; i < real.length; i++) {
      const m = real[i];
      const prev = real[i - 1];
      const next = real[i + 1];
      const gapPrev = prev ? (m.created_at || 0) - (prev.created_at || 0) : Infinity;
      const stamp = !prev || gapPrev >= STAMP_GAP_S || dayOf(prev) !== dayOf(m);
      if (stamp) out.push({ kind: "stamp", id: `stamp-${m.id}`, label: fmtStampLabel(m.created_at) });
      const joinsPrev = !stamp && !!prev && sameRun(prev, m);
      const nextStamp =
        !next ||
        (next.created_at || 0) - (m.created_at || 0) >= STAMP_GAP_S ||
        dayOf(next) !== dayOf(m);
      const joinsNext = !nextStamp && !!next && sameRun(m, next);
      out.push({ kind: "msg", id: m.id, m, first: !joinsPrev, last: !joinsNext });
    }
    return out.reverse();
  }, [msgs, channel, s.historyHasMore, version]);

  const window = useWindowDimensions();
  const [listW, setListW] = useState(0);
  // Row padding (12 + 12) plus the 64pt gutter on the far side of a bubble.
  const maxBubbleWidth = (listW || window.width) - 88;

  // Sending always returns to the newest message, even from deep in history
  // (as in Messages). Incoming messages follow on their own while you're at
  // the bottom (maintainVisibleContentPosition's autoscroll below).
  const lastMsg = msgs[msgs.length - 1];
  const lastSeenId = useRef<string | undefined>(lastMsg?.id);
  useEffect(() => {
    const id = lastMsg?.id;
    if (id === lastSeenId.current) return;
    lastSeenId.current = id;
    if (lastMsg && s.me && lastMsg.user_id === s.me.id) {
      listRef.current?.scrollToOffset({ offset: 0, animated: true });
    }
  }, [lastMsg, s.me]);

  // Older history pages in at the top edge (the list's end).
  const onEndReached = useCallback(() => {
    if (s.historyHasMore[channel] && !s.historyLoading[channel]) {
      void store.loadOlder(channel);
    }
  }, [channel, s.historyHasMore, s.historyLoading, store]);

  // The keyboard went down while the list sat in the slack above its oldest
  // row: settle back onto the content instead of leaving a blank gap.
  const metrics = useRef({ y: 0, content: 0, view: 0 });
  const onScroll = useCallback((e: NativeSyntheticEvent<NativeScrollEvent>) => {
    const { contentOffset, contentSize, layoutMeasurement } = e.nativeEvent;
    metrics.current = {
      y: contentOffset.y,
      content: contentSize.height,
      view: layoutMeasurement.height,
    };
  }, []);
  const prevSlack = useRef(topSlack);
  useEffect(() => {
    const { y, content, view } = metrics.current;
    const max = Math.max(0, content - view) + topSlack;
    if (topSlack < prevSlack.current && y > max) {
      listRef.current?.scrollToOffset({ offset: max, animated: true });
    }
    prevSlack.current = topSlack;
  }, [topSlack]);

  const onLayout = useCallback((e: LayoutChangeEvent) => {
    const w = e.nativeEvent.layout.width;
    setListW((prev) => (prev === w ? prev : w));
  }, []);

  const jumpTo = useCallback(
    (id: string) => {
      const idx = rows.findIndex((r) => r.kind === "msg" && r.m.id === id);
      if (idx >= 0) {
        listRef.current?.scrollToIndex({ index: idx, viewPosition: 0.5, animated: true });
      }
    },
    [rows]
  );

  return (
    <NoScrollEdgeEffects>
      <FlatList
        ref={listRef}
        inverted
        data={rows}
        keyExtractor={(r) => r.id}
        renderItem={({ item }) => {
          if (item.kind === "stamp") {
            return <Text style={styles.stamp}>{item.label}</Text>;
          }
          if (item.kind === "loading") {
            return (
              <View style={styles.loadingRow}>
                <ActivityIndicator size="small" color={colors.muted} />
              </View>
            );
          }
          return (
            <Bubble
              m={item.m}
              first={item.first}
              last={item.last}
              allMsgs={msgs}
              maxWidth={maxBubbleWidth}
              onImagePress={onImagePress}
              onJumpTo={jumpTo}
            />
          );
        }}
        onEndReached={onEndReached}
        onEndReachedThreshold={0.5}
        // Reading history while a message arrives keeps the view still; at the
        // bottom (within 80pt) it scrolls on to show the new message.
        maintainVisibleContentPosition={{
          minIndexForVisible: 0,
          autoscrollToTopThreshold: 80,
        }}
        onScroll={onScroll}
        scrollEventThrottle={32}
        onLayout={onLayout}
        // Flipped: top = screen bottom (composer), bottom = screen top (header).
        contentContainerStyle={{
          paddingTop: bottomInset + 6,
          paddingBottom: topInset + 6,
        }}
        contentInset={{ bottom: topSlack }}
        scrollIndicatorInsets={{
          top: bottomInset,
          bottom: topInset + topSlack,
        }}
        keyboardDismissMode="interactive"
        keyboardShouldPersistTaps="handled"
        onScrollToIndexFailed={({ index }) => {
          // Item not yet laid out — retry after a beat.
          setTimeout(
            () =>
              listRef.current?.scrollToIndex({
                index,
                viewPosition: 0.5,
                animated: false,
              }),
            220,
          );
        }}
        accessibilityLabel="Messages"
      />
    </NoScrollEdgeEffects>
  );
}

function sameRun(a: ChatMessage, b: ChatMessage): boolean {
  return (
    a.type !== "system" &&
    b.type !== "system" &&
    a.user_id === b.user_id &&
    !b.reply_to &&
    (b.created_at || 0) - (a.created_at || 0) < GROUP_GAP_S
  );
}

function dayOf(m: ChatMessage): string {
  return new Date((m.created_at || 0) * 1000).toDateString();
}

/**
 * Floating conversation header: glass back button + a glass capsule with the
 * peer's avatar, name and presence (tap → profile). Sits over the stream.
 */
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
    <View style={styles.topbar} pointerEvents="box-none">
      <View style={styles.side}>
        {showBack && (
          <GlassIconButton
            icon="chevron-back"
            label="Back"
            onPress={() => onBack?.()}
            iconSize={24}
          />
        )}
      </View>
      <Pressable
        onPress={() => peerId != null && onOpenProfile(peerId)}
        accessibilityLabel={`Open ${name}'s profile`}
        accessibilityRole="button"
        style={styles.capsuleHit}
      >
        <GlassSurface interactive style={styles.capsule}>
          <Avatar user={peer} size={32} />
          <View style={styles.capsuleText}>
            <Text style={styles.capsuleName} numberOfLines={1}>
              {name}
            </Text>
            <Text style={[styles.capsuleStatus, online && styles.capsuleStatusOn]}>
              {online ? "Online" : "Offline"}
            </Text>
          </View>
          <Icon name="chevron-forward" size={13} color={colors.faint} />
        </GlassSurface>
      </Pressable>
      <View style={styles.side} />
    </View>
  );
}

export const TOPBAR_H = 60;

const styles = StyleSheet.create({
  stamp: {
    ...type.caption,
    fontWeight: "500",
    color: colors.muted,
    textAlign: "center",
    marginTop: 18,
    marginBottom: 8,
  },
  loadingRow: {
    alignItems: "center",
    paddingVertical: 16,
  },
  topbar: {
    height: TOPBAR_H,
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 12,
  },
  side: {
    width: 44,
  },
  capsuleHit: {
    flex: 1,
    alignItems: "center",
  },
  capsule: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    height: 46,
    borderRadius: 23,
    paddingLeft: 7,
    paddingRight: 12,
    maxWidth: 260,
  },
  capsuleText: {
    flexShrink: 1,
  },
  capsuleName: {
    ...type.headline,
    fontSize: 16,
    color: colors.ink,
  },
  capsuleStatus: {
    fontSize: 11.5,
    color: colors.muted,
    marginTop: -1,
  },
  capsuleStatusOn: {
    color: colors.sage,
    fontWeight: "600",
  },
});
