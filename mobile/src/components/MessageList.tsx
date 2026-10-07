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
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { GlassIconButton, GlassSurface } from "./Glass";

/** Consecutive messages from one sender within this window form a group. */
const GROUP_GAP_S = 5 * 60;
/** A centered timestamp is shown when the conversation pauses this long. */
const STAMP_GAP_S = 60 * 60;

type Row =
  | { kind: "stamp"; id: string; label: string }
  | { kind: "msg"; id: string; m: ChatMessage; first: boolean; last: boolean }
  | { kind: "loading"; id: string };

/**
 * The message stream — a bottom-pinned FlatList (newest at the bottom),
 * grouped into iMessage-style runs with centered timestamps at conversation
 * pauses, lazy history paging at the top edge, and reply-jump scrolling.
 * `topInset`/`bottomInset` reserve room for the floating header + composer
 * so content scrolls underneath them.
 *
 * Deliberately *not* `inverted`: on iOS 26 the flipped scroll view renders
 * its first batch of cells blurred once it sits under floating chrome. So
 * the list pins itself to the bottom instead — it opens at the newest
 * message, follows new ones while you're at the bottom, keeps your place
 * when older pages are prepended, and re-pins when the keyboard resizes it.
 * (On iOS the keyboard doesn't resize it: ConversationView lifts the whole
 * list with the composer, and `topSlack` keeps its top reachable meanwhile.)
 */
export function MessageList({
  channel,
  onImagePress,
  topInset,
  bottomInset,
  topSlack = 0,
}: {
  channel: string;
  onImagePress: (url: string) => void;
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

  // Display rows, oldest → newest.
  const rows = useMemo<Row[]>(() => {
    const out: Row[] = [];
    const real = msgs;
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
    if (s.historyHasMore[channel]) out.unshift({ kind: "loading", id: "loading-older" });
    return out;
  }, [msgs, channel, s.historyHasMore, version]);

  // ---- bottom pinning ----
  // Offsets are computed from our own content/viewport measurements rather
  // than scrollToEnd(), whose cached metrics lag behind keyboard resizes.
  const atBottom = useRef(true);
  // Stay pinned through follow-up layouts while one of our own animated pins
  // is mid-flight (after a send: the optimistic bubble, delivery footnote;
  // on open: cells replacing their estimated heights). Otherwise a scroll
  // event from that animation reads "not at the bottom" as content grows and
  // the list stops following. A drag hands control back to the user.
  const forcePinUntil = useRef(0);
  const scrollY = useRef(0);
  const contentH = useRef(0);
  const viewH = useRef(0);
  // Mirrors atBottom for rendering: position-holding is only wanted while
  // reading history — at the bottom it would fight the pinning.
  const [pinned, setPinned] = useState(true);
  const window = useWindowDimensions();
  const [listW, setListW] = useState(0);
  // Row padding (12 + 12) plus the 64pt gutter on the far side of a bubble.
  const maxBubbleWidth = (listW || window.width) - 88;
  const [ready, setReady] = useState(false);
  const pin = useCallback((animated: boolean) => {
    const offset = Math.max(0, contentH.current - viewH.current);
    if (animated) forcePinUntil.current = Math.max(forcePinUntil.current, Date.now() + 400);
    listRef.current?.scrollToOffset({ offset, animated });
  }, []);
  // Switching threads starts pinned again.
  useEffect(() => {
    atBottom.current = true;
    setPinned(true);
    setReady(false);
  }, [channel]);
  // Sending always jumps to the newest message, even from deep in history
  // (as in Messages); incoming messages only follow when already at the bottom.
  const lastMsg = msgs[msgs.length - 1];
  const lastSeenId = useRef<string | undefined>(lastMsg?.id);
  useEffect(() => {
    const id = lastMsg?.id;
    if (id === lastSeenId.current) return;
    lastSeenId.current = id;
    if (ready && lastMsg && s.me && lastMsg.user_id === s.me.id) {
      atBottom.current = true;
      forcePinUntil.current = Date.now() + 1500;
      setPinned(true);
      pin(true);
    }
  }, [lastMsg, ready, s.me, pin]);

  const onScroll = useCallback((e: NativeSyntheticEvent<NativeScrollEvent>) => {
    const { contentOffset, contentSize, layoutMeasurement } = e.nativeEvent;
    scrollY.current = contentOffset.y;
    const bottom =
      Date.now() < forcePinUntil.current ||
      contentOffset.y + layoutMeasurement.height >= contentSize.height - 80;
    atBottom.current = bottom;
    setPinned((p) => (p === bottom ? p : bottom));
  }, []);
  // The composer's height feeds bottomInset; while it animates (voice-message
  // morph, multiline growth) the list must follow frame by frame, not start
  // a fresh animated scroll on every frame.
  const pinnedInset = useRef(bottomInset);
  const onContentSizeChange = useCallback(
    (_w: number, h: number) => {
      contentH.current = h;
      const insetMoved = pinnedInset.current !== bottomInset;
      pinnedInset.current = bottomInset;
      if (!ready) {
        pin(false);
        // Reveal after the first pin so the oldest rows never flash.
        requestAnimationFrame(() => {
          pin(false);
          setReady(true);
        });
      } else if (atBottom.current || Date.now() < forcePinUntil.current) {
        pin(!insetMoved);
      }
    },
    [ready, pin, bottomInset]
  );
  const onScrollBeginDrag = useCallback(() => {
    forcePinUntil.current = 0;
  }, []);

  // Older history pages in at the top edge — but only once the list has
  // pinned itself to the newest message. It first lays out at offset 0, so
  // the top edge is "reached" before the pin lands; paging in history then
  // grows the content under the pin, which has to chase it (and could lose).
  const loadOlder = useCallback(() => {
    if (s.historyHasMore[channel] && !s.historyLoading[channel]) {
      void store.loadOlder(channel);
    }
  }, [channel, s.historyHasMore, s.historyLoading, store]);
  const onStartReached = useCallback(() => {
    if (ready) loadOlder();
  }, [ready, loadOlder]);
  // A thread shorter than the screen sits at its top edge from the start.
  useEffect(() => {
    if (ready && contentH.current <= viewH.current) loadOlder();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready]);
  // The keyboard went down while the list sat in the slack above its first
  // row: settle back onto the content instead of leaving a blank gap.
  const prevSlack = useRef(topSlack);
  useEffect(() => {
    if (topSlack < prevSlack.current && scrollY.current < -topSlack) {
      listRef.current?.scrollToOffset({ offset: -topSlack, animated: true });
    }
    prevSlack.current = topSlack;
  }, [topSlack]);
  const onLayout = useCallback(
    (e: LayoutChangeEvent) => {
      const { height: h, width: w } = e.nativeEvent.layout;
      setListW((prev) => (prev === w ? prev : w));
      const changed = viewH.current !== 0 && h !== viewH.current;
      viewH.current = h;
      // Keyboard / composer growth shrinks the viewport — stay on the newest.
      if (changed && atBottom.current) pin(false);
    },
    [pin]
  );

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
    <FlatList
      ref={listRef}
      data={rows}
      style={{ opacity: ready ? 1 : 0 }}
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
      initialNumToRender={Math.min(rows.length, 60)}
      onStartReached={onStartReached}
      onStartReachedThreshold={0.4}
      maintainVisibleContentPosition={pinned ? undefined : { minIndexForVisible: 1 }}
      onScroll={onScroll}
      onScrollBeginDrag={onScrollBeginDrag}
      scrollEventThrottle={32}
      onContentSizeChange={onContentSizeChange}
      onLayout={onLayout}
      contentContainerStyle={{ paddingTop: topInset + 6, paddingBottom: bottomInset + 6 }}
      contentInset={{ top: topSlack }}
      scrollIndicatorInsets={{ top: topInset + topSlack, bottom: bottomInset }}
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
          <GlassIconButton icon="chevron-back" label="Back" onPress={() => onBack?.()} iconSize={24} />
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
