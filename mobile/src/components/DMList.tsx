import React from "react";
import {
  FlatList,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { lastMessagePreviewFor, nameFor } from "@alexmessages/shared";
import { useChatState, useSession } from "../session";
import { colors, fontDisplay, radius } from "../theme";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { showActionSheet } from "./ActionSheet";

type ListItem = { kind: "header"; label: string } | { kind: "dm"; channel: string; peerId: number };

/**
 * The left rail: "Messages" title + compose button, pinned + recent DM rows,
 * bottom account trigger → Settings. Used full-screen on phones and as the
 * fixed-width sidebar in the wide layout.
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
  const items = store.dmListItems();
  const pinned = items.filter((it) => store.dmStateFor(it.channel).pinned);
  const normal = items.filter((it) => !store.dmStateFor(it.channel).pinned);
  const me = s.me;

  const data: ListItem[] = [];
  if (pinned.length) {
    data.push({ kind: "header", label: "Pinned" });
    for (const it of pinned) data.push({ kind: "dm", channel: it.channel, peerId: it.peerId });
    if (normal.length) data.push({ kind: "header", label: "Recent" });
  }
  for (const it of normal) data.push({ kind: "dm", channel: it.channel, peerId: it.peerId });

  return (
    <View style={styles.root}>
      <View style={styles.header}>
        <Text style={styles.title}>Messages</Text>
        <Pressable
          style={styles.composeBtn}
          onPress={onNewDm}
          accessibilityLabel="New conversation"
          hitSlop={8}
        >
          <Icon name="create-outline" size={20} color={colors.sageDeep} />
        </Pressable>
      </View>

      <FlatList
        data={data}
        keyExtractor={(it) => (it.kind === "dm" ? it.channel : `h:${it.label}`)}
        renderItem={({ item }) =>
          item.kind === "dm" ? (
            <DmRow
              channel={item.channel}
              peerId={item.peerId}
              active={item.channel === activeChannel}
              onPress={() => onOpenChannel(item.channel)}
            />
          ) : (
            <Text style={styles.section}>{item.label}</Text>
          )
        }
        contentContainerStyle={styles.list}
        ListEmptyComponent={
          <Text style={styles.empty}>
            No conversations yet.{"\n"}Start one with the compose button.
          </Text>
        }
      />

      <Pressable
        style={styles.account}
        onPress={onOpenSettings}
        accessibilityLabel="Settings"
        accessibilityRole="button"
      >
        <Avatar user={me} size={38} />
        <View style={styles.accountMeta}>
          <Text style={styles.accountName} numberOfLines={1}>
            {me ? me.display_name || me.username : "—"}
          </Text>
          <Text style={styles.accountHandle} numberOfLines={1}>
            {me ? `@${me.username}` : ""}
          </Text>
        </View>
        <Icon name="settings-outline" size={18} color={colors.muted} />
      </Pressable>
    </View>
  );
}

function DmRow({
  channel,
  peerId,
  active,
  onPress,
}: {
  channel: string;
  peerId: number;
  active: boolean;
  onPress: () => void;
}) {
  const s = useChatState();
  const { store } = useSession();
  const st = store.dmStateFor(channel);
  const unread = store.isUnread(channel);
  const preview = lastMessagePreviewFor(s.history[channel], s.me?.id ?? null);
  const peer = store.userFor(peerId);

  const openMenu = () => {
    const pinLabel = st.pinned ? "Unpin conversation" : "Pin conversation";
    const readLabel = unread ? "Mark as read" : "Mark as unread";
    showActionSheet({
      title: nameFor(s.users, peerId),
      options: [
        { label: pinLabel, onPress: () => void store.togglePin(channel, !st.pinned) },
        {
          label: readLabel,
          onPress: () => void (unread ? store.markRead(channel) : store.markUnread(channel)),
        },
        { label: "Delete for me", destructive: true, onPress: () => void store.deleteDm(channel) },
      ],
    });
  };

  return (
    <Pressable
      style={[styles.row, active && styles.rowActive]}
      onPress={onPress}
      onLongPress={openMenu}
      accessibilityRole="button"
      accessibilityLabel={`Conversation with ${nameFor(s.users, peerId)}${unread ? ", unread" : ""}`}
    >
      <Avatar user={peer} size={42} />
      <View style={styles.who}>
        <Text style={[styles.name, unread && styles.nameUnread]} numberOfLines={1}>
          {nameFor(s.users, peerId)}
        </Text>
        <Text style={[styles.sub, unread && styles.subUnread]} numberOfLines={1}>
          {preview}
        </Text>
      </View>
      {unread && <View style={styles.dot} accessibilityLabel="Unread messages" />}
    </Pressable>
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
    paddingTop: 8,
    paddingBottom: 10,
  },
  title: {
    fontFamily: fontDisplay,
    fontSize: 34,
    color: colors.ink,
    letterSpacing: 0.2,
  },
  composeBtn: {
    width: 38,
    height: 38,
    borderRadius: 19,
    backgroundColor: colors.sageTint,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
    alignItems: "center",
    justifyContent: "center",
  },
  section: {
    fontSize: 11,
    fontWeight: "700",
    letterSpacing: 1.1,
    textTransform: "uppercase",
    color: colors.faint,
    paddingHorizontal: 20,
    paddingTop: 14,
    paddingBottom: 6,
  },
  list: {
    paddingHorizontal: 10,
    paddingBottom: 12,
    flexGrow: 1,
  },
  empty: {
    marginTop: 48,
    textAlign: "center",
    color: colors.faint,
    fontSize: 14,
    lineHeight: 21,
  },
  row: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
    paddingHorizontal: 10,
    paddingVertical: 8,
    borderRadius: radius.m,
  },
  rowActive: {
    backgroundColor: colors.sageTint,
  },
  who: {
    flex: 1,
    gap: 1,
  },
  name: {
    fontSize: 15,
    fontWeight: "600",
    color: colors.ink,
  },
  nameUnread: {
    fontWeight: "800",
  },
  sub: {
    fontSize: 13,
    color: colors.muted,
  },
  subUnread: {
    color: colors.ink2,
    fontWeight: "500",
  },
  dot: {
    width: 9,
    height: 9,
    borderRadius: 5,
    backgroundColor: colors.sage,
    marginRight: 4,
  },
  account: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
    paddingHorizontal: 16,
    paddingVertical: 12,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: colors.line,
    backgroundColor: colors.surface,
  },
  accountMeta: {
    flex: 1,
  },
  accountName: {
    fontSize: 15,
    fontWeight: "700",
    color: colors.ink,
  },
  accountHandle: {
    fontSize: 12,
    color: colors.muted,
  },
});
