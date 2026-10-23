import React from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { callEntryText, fmtTime, type ChatMessage } from "@alexmessages/shared";
import { useChatState, useSession } from "../session";
import { colors, type } from "../theme";
import { Icon } from "./Icon";

/**
 * A finished call in the thread ("Video call · 4:32", "Missed voice call"),
 * on the caller's side like a message, with a button that calls again —
 * the web's CallEntry.
 */
export function CallEntry({ m }: { m: ChatMessage }) {
  const s = useChatState();
  const { store } = useSession();
  const call = m.call;
  if (!call) return null;
  const t = callEntryText(call, s.me?.id);
  const peerId = store.peerOf(m.channel);
  const inCall = !!s.call && s.call.phase !== "ended";
  const again = t.outgoing ? "Call again" : "Call back";
  const icon = call.media === "video" ? "videocam" : "call";
  const accent = t.missed ? colors.danger : colors.sageDeep;

  return (
    <View style={[styles.row, t.outgoing ? styles.rowMine : styles.rowThem]}>
      <View style={[styles.card, t.outgoing && styles.cardMine]}>
        <View style={[styles.icon, t.missed && styles.iconMissed]}>
          <Icon name={icon} size={17} color={accent} />
        </View>
        <View style={styles.text}>
          <Text style={[styles.title, t.missed && styles.titleMissed]} numberOfLines={1}>
            {t.title}
          </Text>
          <Text style={styles.detail} numberOfLines={1}>
            {[t.detail, fmtTime(call.started_at)].filter(Boolean).join(" · ")}
          </Text>
        </View>
        {store.calls.supported && peerId != null && (
          <Pressable
            onPress={() => store.startCall(peerId, call.media)}
            disabled={inCall}
            accessibilityRole="button"
            accessibilityLabel={again}
            accessibilityState={{ disabled: inCall }}
            hitSlop={6}
            style={({ pressed }) => [styles.again, (pressed || inCall) && { opacity: 0.45 }]}
          >
            <Icon name={icon} size={15} color={colors.sageDeep} />
          </Pressable>
        )}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  row: {
    paddingHorizontal: 12,
    marginTop: 8,
  },
  rowMine: {
    alignItems: "flex-end",
    paddingLeft: 64,
  },
  rowThem: {
    alignItems: "flex-start",
    paddingRight: 64,
  },
  card: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    maxWidth: "100%",
    paddingVertical: 9,
    paddingLeft: 10,
    paddingRight: 12,
    borderRadius: 18,
    backgroundColor: colors.surface,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
  },
  cardMine: {
    backgroundColor: colors.sageTint,
    borderColor: colors.sageSoft,
  },
  icon: {
    width: 34,
    height: 34,
    borderRadius: 17,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: colors.sageSoft,
  },
  iconMissed: {
    backgroundColor: "#FBECEA",
  },
  text: {
    flexShrink: 1,
  },
  title: {
    ...type.subhead,
    fontWeight: "600",
    color: colors.ink,
  },
  titleMissed: {
    color: colors.danger,
  },
  detail: {
    ...type.footnote,
    color: colors.muted,
    fontVariant: ["tabular-nums"],
  },
  again: {
    width: 32,
    height: 32,
    borderRadius: 16,
    marginLeft: 4,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: colors.sageSoft,
  },
});
