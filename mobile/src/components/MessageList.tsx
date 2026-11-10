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
import { useRouter } from "expo-router";
import { fmtStampLabel, messageKey, type ChatMessage } from "@alexmessages/shared";
import { useChatState, useSession } from "../session";
import { colors, type } from "../theme";
import { Bubble } from "./Bubble";
import { CallEntry } from "./CallEntry";
import type { ImagePressHandler } from "./attachments";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { GlassIconButton, GlassSurface } from "./Glass";
import { GlassMenuButton } from "./NativeMenu";
import { NoScrollEdgeEffects } from "./ScrollEdge";
import { CALL_GREEN } from "../call/CallScreen";

const NO_MESSAGES: ChatMessage[] = [];
/** Consecutive messages from one sender within this window form a group. */
const GROUP_GAP_S = 5 * 60;
/** Leading icons of the call button's Android dropdown. */
const VIDEO_CALL_ICON = require("../../assets/icons/videocam-outline.xml");
const VOICE_CALL_ICON = require("../../assets/icons/call-outline.xml");
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

  // The store replaces a channel's history array (and the message objects in
  // it) on every change, so the array's identity is a valid memo key.
  const msgs = s.history[channel] || NO_MESSAGES;
  const hasMore = !!s.historyHasMore[channel];

  // Display rows, built oldest → newest, then reversed for the inverted list
  // (row 0 sits at the bottom). Rows are keyed by messageKey(m), which stays
  // the same when a sent message is confirmed and its id changes to the
  // server's, so the bubble isn't remounted on delivery.
  const rows = useMemo<Row[]>(() => {
    const out: Row[] = [];
    const real = msgs;
    if (hasMore) out.push({ kind: "loading", id: "loading-older" });
    for (let i = 0; i < real.length; i++) {
      const m = real[i];
      const prev = real[i - 1];
      const next = real[i + 1];
      const gapPrev = prev ? (m.created_at || 0) - (prev.created_at || 0) : Infinity;
      const stamp = !prev || gapPrev >= STAMP_GAP_S || dayOf(prev) !== dayOf(m);
      const key = messageKey(m);
      if (stamp) out.push({ kind: "stamp", id: `stamp-${key}`, label: fmtStampLabel(m.created_at) });
      const joinsPrev = !stamp && !!prev && sameRun(prev, m);
      const nextStamp =
        !next ||
        (next.created_at || 0) - (m.created_at || 0) >= STAMP_GAP_S ||
        dayOf(next) !== dayOf(m);
      const joinsNext = !nextStamp && !!next && sameRun(m, next);
      out.push({ kind: "msg", id: key, m, first: !joinsPrev, last: !joinsNext });
    }
    return out.reverse();
  }, [msgs, hasMore]);

  const window = useWindowDimensions();
  const [listW, setListW] = useState(0);
  // Row padding (12 + 12) plus the 64pt gutter on the far side of a bubble.
  const maxBubbleWidth = (listW || window.width) - 88;

  // Sending always returns to the newest message, even from deep in history
  // (as in Messages). Incoming messages follow on their own while you're at
  // the bottom (maintainVisibleContentPosition's autoscroll below).
  const lastMsg = msgs[msgs.length - 1];
  const lastSeenId = useRef<string | undefined>(lastMsg && messageKey(lastMsg));
  useEffect(() => {
    const id = lastMsg && messageKey(lastMsg);
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
          if (item.m.type === "call") return <CallEntry m={item.m} />;
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

const joinsRuns = (m: ChatMessage) => m.type !== "system" && m.type !== "call";

/** Whether b joins a's bubble run. System lines and call entries stand alone. */
function sameRun(a: ChatMessage, b: ChatMessage): boolean {
  return (
    joinsRuns(a) &&
    joinsRuns(b) &&
    a.user_id === b.user_id &&
    !b.reply_to &&
    (b.created_at || 0) - (a.created_at || 0) < GROUP_GAP_S
  );
}

function dayOf(m: ChatMessage): string {
  return new Date((m.created_at || 0) * 1000).toDateString();
}

/**
 * Floating conversation header: glass back button, a glass capsule with the
 * peer's avatar, name and presence (tap → profile), and the call button — a
 * menu of Video Call and Voice Call, or, while a call is live, a green
 * button back to the call screen. Sits over the stream.
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
  const router = useRouter();
  const peerId = store.peerOf(channel);
  const peer = store.userFor(peerId);
  const online = peerId != null && s.online.has(peerId);
  const name = peer?.display_name || peer?.username || "…";
  const inCall = !!s.call && s.call.phase !== "ended";
  const canCall = store.calls.supported && peerId != null && !!peer;

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
            <Text style={styles.capsuleName} numberOfLines={1} maxFontSizeMultiplier={1.2}>
              {name}
            </Text>
            <View style={styles.statusRow}>
              <View style={[styles.statusDot, online && styles.statusDotOn]} />
              <Text style={styles.capsuleStatus} maxFontSizeMultiplier={1.2} numberOfLines={1}>
                {online ? "Online" : "Offline"}
              </Text>
            </View>
          </View>
          <Icon name="chevron-forward" size={13} color={colors.faint} />
        </GlassSurface>
      </Pressable>
      <View style={styles.side}>
        {inCall ? (
          <GlassIconButton
            icon="call"
            label="Return to call"
            tint={CALL_GREEN}
            color={colors.surface}
            onPress={() => router.push("/call")}
          />
        ) : (
          canCall && (
            <GlassMenuButton
              label="Call"
              title={`Call ${name}`}
              systemImage="video"
              fallbackIcon="videocam-outline"
              iconSize={17}
              fallbackIconSize={22}
              iconColor={colors.ink}
              dropdown
              actions={[
                {
                  label: "Video Call",
                  systemImage: "video",
                  androidIcon: VIDEO_CALL_ICON,
                  onPress: () => store.startCall(peerId, "video"),
                },
                {
                  label: "Voice Call",
                  systemImage: "phone",
                  androidIcon: VOICE_CALL_ICON,
                  onPress: () => store.startCall(peerId, "audio"),
                },
              ]}
            />
          )
        )}
      </View>
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
    // A minimum, so larger text sizes grow the capsule instead of clipping
    // it; the texts cap their scaling (maxFontSizeMultiplier) so it stays
    // inside the fixed TOPBAR_H header.
    minHeight: 46,
    paddingVertical: 3,
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
  statusRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 4,
  },
  // The same presence dot as the DM list and the contact card.
  statusDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
    backgroundColor: colors.faint,
  },
  statusDotOn: {
    backgroundColor: colors.online,
  },
});
