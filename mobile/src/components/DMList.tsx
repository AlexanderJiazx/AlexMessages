import React, { useMemo, useRef, useState } from "react";
import {
  Animated,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  useWindowDimensions,
  View,
} from "react-native";
import {
  fmtListTime,
  lastMessagePreviewFor,
  nameFor,
} from "@alexmessages/shared";
import { useChatState, useSession } from "../session";
import { colors, glassSupported, radius, type } from "../theme";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { LongPressMenu, type MenuAction } from "./NativeMenu";
import { EdgeFade, GlassIconButton, GlassSurface } from "./Glass";

const BAR_H = 56;

/**
 * The conversation list, iMessage-style: floating glass controls (account →
 * Settings on the left, compose on the right), a large "Messages" title that
 * hands off to a centered inline title on scroll, a search field, pinned
 * threads as a grid of large avatars, then the recent threads. Used
 * full-screen on phones and as the sidebar in the wide layout.
 */
export function DMList({
  onOpenChannel,
  onNewDm,
  onOpenSettings,
  activeChannel,
}: {
  onOpenChannel: (channel: string) => void;
  onNewDm: () => void;
  onOpenSettings: () => void;
  activeChannel: string | null;
}) {
  const s = useChatState();
  const { store } = useSession();
  const [query, setQuery] = useState("");
  // Rows are hosted by the native long-press menu, which measures them
  // without the list's width limit — so they get an explicit width.
  const window = useWindowDimensions();
  const [listW, setListW] = useState(0);
  const rowW = listW || window.width;
  const scrollY = useRef(new Animated.Value(0)).current;

  const q = query.trim().toLowerCase();
  const items = store.dmListItems().filter((it) => {
    if (!q) return true;
    const u = store.userFor(it.peerId);
    return (
      nameFor(s.users, it.peerId).toLowerCase().includes(q) ||
      (u?.username || "").toLowerCase().includes(q)
    );
  });
  const pinned = q ? [] : items.filter((it) => store.dmStateFor(it.channel).pinned);
  const recent = q ? items : items.filter((it) => !store.dmStateFor(it.channel).pinned);

  const inlineTitleOpacity = scrollY.interpolate({
    inputRange: [28, 48],
    outputRange: [0, 1],
    extrapolate: "clamp",
  });

  const header = (
    <View>
      <Text style={styles.largeTitle} accessibilityRole="header">
        Messages
      </Text>
      <View style={styles.search}>
        <Icon name="search" size={17} color={colors.muted} />
        <TextInput
          style={styles.searchInput}
          value={query}
          onChangeText={setQuery}
          placeholder="Search"
          placeholderTextColor={colors.muted}
          autoCapitalize="none"
          autoCorrect={false}
          clearButtonMode="while-editing"
          returnKeyType="search"
          accessibilityLabel="Search conversations"
        />
      </View>
      {pinned.length > 0 && (
        <View style={styles.pinnedGrid}>
          {pinned.map((it) => (
            <PinnedCell
              key={it.channel}
              channel={it.channel}
              peerId={it.peerId}
              active={it.channel === activeChannel}
              width={(rowW - 16) / 3}
              onPress={() => onOpenChannel(it.channel)}
            />
          ))}
        </View>
      )}
    </View>
  );

  return (
    <View
      style={styles.root}
      onLayout={(e) => {
        const w = e.nativeEvent.layout.width;
        setListW((prev) => (prev === w ? prev : w));
      }}
    >
      <Animated.FlatList
        data={recent}
        keyExtractor={(it) => it.channel}
        ListHeaderComponent={header}
        renderItem={({ item, index }) => (
          <DmRow
            channel={item.channel}
            peerId={item.peerId}
            active={item.channel === activeChannel}
            last={index === recent.length - 1}
            width={rowW}
            onPress={() => onOpenChannel(item.channel)}
          />
        )}
        contentContainerStyle={styles.list}
        keyboardShouldPersistTaps="handled"
        keyboardDismissMode="on-drag"
        onScroll={Animated.event([{ nativeEvent: { contentOffset: { y: scrollY } } }], {
          useNativeDriver: true,
        })}
        scrollEventThrottle={16}
        ListEmptyComponent={
          q ? (
            <Text style={styles.noResults}>No results for “{query.trim()}”</Text>
          ) : pinned.length ? null : (
            <EmptyState onNewDm={onNewDm} />
          )
        }
      />

      {/* Floating bar: content dissolves under it, controls are glass. */}
      <EdgeFade edge="top" height={BAR_H + 18} />
      <View style={styles.bar} pointerEvents="box-none">
        <Pressable
          onPress={onOpenSettings}
          accessibilityLabel="Settings"
          accessibilityRole="button"
          hitSlop={6}
        >
          <GlassSurface interactive style={styles.meBtn}>
            <Avatar user={s.me} size={34} />
          </GlassSurface>
        </Pressable>
        <Animated.Text
          style={[styles.inlineTitle, { opacity: inlineTitleOpacity }]}
          numberOfLines={1}
          importantForAccessibility="no"
        >
          Messages
        </Animated.Text>
        <GlassIconButton
          icon="create-outline"
          label="New conversation"
          onPress={onNewDm}
          iconSize={21}
          color={colors.sageDeep}
        />
      </View>
    </View>
  );
}

/** Long-press menu actions shared by list rows and pinned cells. */
function useDmActions(channel: string): MenuAction[] {
  const { store } = useSession();
  const st = store.dmStateFor(channel);
  const unread = store.isUnread(channel);
  return [
    {
      label: st.pinned ? "Unpin conversation" : "Pin conversation",
      systemImage: st.pinned ? "pin.slash" : "pin",
      onPress: () => void store.togglePin(channel, !st.pinned),
    },
    {
      label: unread ? "Mark as read" : "Mark as unread",
      systemImage: unread ? "message" : "message.badge",
      onPress: () => void (unread ? store.markRead(channel) : store.markUnread(channel)),
    },
    {
      label: "Delete for me",
      systemImage: "trash",
      destructive: true,
      onPress: () => void store.deleteDm(channel),
    },
  ];
}

function DmRow({
  channel,
  peerId,
  active,
  last,
  width,
  onPress,
}: {
  channel: string;
  peerId: number;
  active: boolean;
  last: boolean;
  width: number;
  onPress: () => void;
}) {
  const s = useChatState();
  const { store } = useSession();
  const unread = store.isUnread(channel);
  const preview = lastMessagePreviewFor(s.history[channel], s.me?.id ?? null);
  const when = fmtListTime(store.lastMessageTS(channel));
  const name = nameFor(s.users, peerId);
  const online = s.online.has(peerId);
  const actions = useDmActions(channel);

  return (
    <LongPressMenu actions={actions} title={name} cornerRadius={14} fill>
      <Pressable
        style={({ pressed }) => [
          styles.row,
          { width },
          active && styles.rowActive,
          pressed && !active && styles.rowPressed,
        ]}
        onPress={onPress}
        accessibilityRole="button"
        accessibilityLabel={`Conversation with ${name}${unread ? ", unread" : ""}`}
      >
        <View style={styles.dotSlot}>
          {unread && <View style={styles.unreadDot} accessibilityLabel="Unread messages" />}
        </View>
        <View>
          <Avatar user={store.userFor(peerId)} size={52} />
          {online && <View style={styles.onlineBadge} />}
        </View>
        <View style={[styles.rowBody, !last && styles.rowSeparator]}>
          <View style={styles.rowTop}>
            <Text style={styles.rowName} numberOfLines={1}>
              {name}
            </Text>
            <Text style={[styles.rowTime, unread && styles.rowTimeUnread]}>{when}</Text>
            <Icon name="chevron-forward" size={14} color={colors.faint} />
          </View>
          <Text style={[styles.rowPreview, unread && styles.rowPreviewUnread]} numberOfLines={2}>
            {preview || " "}
          </Text>
        </View>
      </Pressable>
    </LongPressMenu>
  );
}

function PinnedCell({
  channel,
  peerId,
  active,
  width,
  onPress,
}: {
  channel: string;
  peerId: number;
  active: boolean;
  width: number;
  onPress: () => void;
}) {
  const s = useChatState();
  const { store } = useSession();
  const unread = store.isUnread(channel);
  const name = nameFor(s.users, peerId);
  const actions = useDmActions(channel);
  return (
    <LongPressMenu actions={actions} title={name} cornerRadius={20}>
      <Pressable
        style={({ pressed }) => [
          styles.pinCellInner,
          { width },
          (pressed || active) && { opacity: 0.7 },
        ]}
        onPress={onPress}
        accessibilityRole="button"
        accessibilityLabel={`Conversation with ${name}${unread ? ", unread" : ""}`}
      >
        <View>
          <Avatar user={store.userFor(peerId)} size={72} />
          {unread && <View style={styles.pinUnread} />}
        </View>
        <Text style={[styles.pinName, unread && { fontWeight: "600", color: colors.ink }]} numberOfLines={1}>
          {name.split(" ")[0]}
        </Text>
      </Pressable>
    </LongPressMenu>
  );
}

function EmptyState({ onNewDm }: { onNewDm: () => void }) {
  return (
    <View style={styles.empty}>
      <View style={styles.emptyIcon}>
        <Icon name="chatbubbles-outline" size={34} color={colors.sage} />
      </View>
      <Text style={styles.emptyTitle}>No Messages Yet</Text>
      <Text style={styles.emptySub}>Start a conversation with anyone on this server.</Text>
      <Pressable style={styles.emptyBtn} onPress={onNewDm} accessibilityRole="button">
        <Text style={styles.emptyBtnText}>New Message</Text>
      </Pressable>
    </View>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  bar: {
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
    height: BAR_H,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
  },
  meBtn: {
    width: 44,
    height: 44,
    borderRadius: 22,
    alignItems: "center",
    justifyContent: "center",
  },
  inlineTitle: {
    ...type.headline,
    color: colors.ink,
    position: "absolute",
    left: 80,
    right: 80,
    textAlign: "center",
  },
  list: {
    paddingTop: BAR_H,
    paddingBottom: 32,
    flexGrow: 1,
  },
  largeTitle: {
    ...type.largeTitle,
    color: colors.ink,
    paddingHorizontal: 20,
    paddingTop: 2,
    paddingBottom: 10,
  },
  search: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    marginHorizontal: 16,
    marginBottom: 10,
    paddingHorizontal: 10,
    height: 38,
    borderRadius: glassSupported ? 19 : 11,
    backgroundColor: "rgba(118,128,112,0.13)",
  },
  searchInput: {
    flex: 1,
    ...type.body,
    color: colors.ink,
    paddingVertical: 0,
  },
  pinnedGrid: {
    flexDirection: "row",
    flexWrap: "wrap",
    justifyContent: "center",
    paddingHorizontal: 8,
    paddingTop: 6,
    paddingBottom: 8,
  },
  pinCellInner: {
    backgroundColor: colors.bg,
    alignItems: "center",
    paddingVertical: 8,
    gap: 6,
  },
  pinName: {
    ...type.footnote,
    color: colors.muted,
    maxWidth: 96,
  },
  pinUnread: {
    position: "absolute",
    top: 2,
    left: 2,
    width: 16,
    height: 16,
    borderRadius: 8,
    backgroundColor: colors.sage,
    borderWidth: 2.5,
    borderColor: colors.bg,
  },
  row: {
    flexDirection: "row",
    alignItems: "center",
    paddingRight: 16,
    // Opaque so the context-menu preview lifts out as a solid card.
    backgroundColor: colors.bg,
  },
  rowPressed: {
    backgroundColor: "#EAE8E1",
  },
  rowActive: {
    backgroundColor: colors.sageTint,
  },
  dotSlot: {
    width: 22,
    alignItems: "center",
  },
  unreadDot: {
    width: 10,
    height: 10,
    borderRadius: 5,
    backgroundColor: colors.sage,
  },
  onlineBadge: {
    position: "absolute",
    right: 0,
    bottom: 1,
    width: 14,
    height: 14,
    borderRadius: 7,
    backgroundColor: "#4CB86E",
    borderWidth: 2.5,
    borderColor: colors.bg,
  },
  rowBody: {
    flex: 1,
    marginLeft: 12,
    paddingVertical: 11,
    minHeight: 76,
    justifyContent: "center",
  },
  rowSeparator: {
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.separator,
  },
  rowTop: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    marginBottom: 2,
  },
  rowName: {
    ...type.headline,
    color: colors.ink,
    flex: 1,
  },
  rowTime: {
    ...type.subhead,
    color: colors.muted,
  },
  rowTimeUnread: {
    color: colors.sage,
  },
  rowPreview: {
    ...type.subhead,
    color: colors.muted,
    lineHeight: 20,
  },
  rowPreviewUnread: {
    color: colors.ink2,
  },
  noResults: {
    ...type.callout,
    color: colors.muted,
    textAlign: "center",
    marginTop: 40,
  },
  empty: {
    alignItems: "center",
    paddingHorizontal: 40,
    paddingTop: 72,
  },
  emptyIcon: {
    width: 72,
    height: 72,
    borderRadius: 36,
    backgroundColor: colors.sageTint,
    alignItems: "center",
    justifyContent: "center",
    marginBottom: 16,
  },
  emptyTitle: {
    ...type.title2,
    color: colors.ink,
    marginBottom: 6,
  },
  emptySub: {
    ...type.subhead,
    color: colors.muted,
    textAlign: "center",
    lineHeight: 20,
    marginBottom: 20,
  },
  emptyBtn: {
    backgroundColor: colors.sage,
    borderRadius: radius.pill,
    paddingHorizontal: 22,
    paddingVertical: 12,
  },
  emptyBtnText: {
    ...type.headline,
    color: colors.surface,
  },
});
