import React, { useEffect, useState } from "react";
import {
  Image,
  Linking,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import * as Clipboard from "expo-clipboard";
import {
  bodySegments,
  colorFor,
  firstUrl,
  fmtTime,
  nameFor,
  type ChatMessage,
  type LinkPreview,
} from "@alexmessages/shared";
import { toast, useChatState, useSession } from "../session";
import { colors, radius } from "../theme";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { AttachmentView } from "./attachments";
import { showActionSheet } from "./ActionSheet";

const GROUP_GAP_S = 5 * 60;

/**
 * One message row: avatar slot, header, bubble (reply-ref, text, media),
 * link preview, delivery remark. Long-press → Reply/Edit/Copy actions
 * (the native equivalent of the web client's hover actions).
 */
export function Bubble({
  m,
  prev,
  allMsgs,
  onOpenProfile,
  onImagePress,
  onJumpTo,
}: {
  m: ChatMessage;
  prev: ChatMessage | null;
  allMsgs: ChatMessage[];
  onOpenProfile: (uid: number) => void;
  onImagePress: (url: string) => void;
  onJumpTo: (id: string) => void;
}) {
  const s = useChatState();
  const { store } = useSession();
  const mine = s.me != null && m.user_id === s.me.id;
  const isCont =
    !!prev &&
    prev.type === "message" &&
    prev.user_id === m.user_id &&
    !m.reply_to &&
    Math.abs((m.created_at || 0) - (prev.created_at || 0)) < GROUP_GAP_S;
  const [editing, setEditing] = useState(false);
  const status = store.deliveryStatus(m.channel);
  const replyTarget = m.reply_to ? allMsgs.find((x) => x.id === m.reply_to) : null;

  if (m.type === "system") {
    return (
      <View style={styles.systemRow}>
        <Text style={styles.systemText}>{m.text}</Text>
      </View>
    );
  }

  const openActions = () => {
    const labels: string[] = ["Reply", "Copy"];
    if (mine && m.text) labels.splice(1, 0, "Edit");
    showActionSheet({
      title: "Message",
      message: (m.text || "").slice(0, 80) || undefined,
      options: labels.map((l) => ({ label: l, onPress: () => runAction(l) })),
    });
  };

  const runAction = (label: string) => {
    if (label === "Reply") store.setReplyTo(m);
    else if (label === "Edit") setEditing(true);
    else if (label === "Copy") {
      void Clipboard.setStringAsync(m.text || "").then(() => toast("Copied"));
    }
  };

  return (
    <View style={[styles.row, mine && styles.rowMine]}>
      {!mine && (
        <View style={styles.avSlot}>
          {isCont ? (
            <Text style={styles.gutterTime}>{fmtTime(m.created_at)}</Text>
          ) : (
            <Pressable onPress={() => m.user_id != null && onOpenProfile(m.user_id)}>
              <Avatar user={store.userFor(m.user_id)} size={34} />
            </Pressable>
          )}
        </View>
      )}
      <View style={[styles.col, mine && styles.colMine]}>
        {!isCont && (
          <View style={[styles.head, mine && styles.headMine]}>
            {!mine && (
              <Pressable onPress={() => m.user_id != null && onOpenProfile(m.user_id)}>
                <Text style={[styles.author, { color: colorFor(m.user_id) }]}>
                  {nameFor(s.users, m.user_id)}
                </Text>
              </Pressable>
            )}
            <Text style={styles.time}>{fmtTime(m.created_at)}</Text>
          </View>
        )}
        <Pressable
          style={[styles.bubble, mine ? styles.bubbleMine : styles.bubbleThem]}
          onLongPress={openActions}
          accessible={!editing}
        >
          {replyTarget && <ReplyRef target={replyTarget} mine={mine} onJump={onJumpTo} />}
          {editing ? (
            <EditArea m={m} mine={mine} onDone={() => setEditing(false)} />
          ) : (
            !!m.text && (
              <View>
                <BodyText text={m.text} mine={mine} />
                {m.edited_at != null && (
                  <Text style={[styles.editedTag, mine && styles.editedTagMine]}>(edited)</Text>
                )}
              </View>
            )
          )}
          {!!m.attachments?.length && (
            <View style={styles.atts}>
              {m.attachments.map((a, i) => (
                <AttachmentView key={i} a={a} mine={mine} onImagePress={onImagePress} />
              ))}
            </View>
          )}
        </Pressable>
        <LinkPreviewSlot text={m.text || ""} />
        {m._status === "failed" && (
          <View style={styles.statusRow}>
            <Text style={styles.statusFailed}>Failed to send · </Text>
            <Pressable onPress={() => store.retrySend(m.client_id || m.id)}>
              <Text style={styles.statusRetry}>Retry</Text>
            </Pressable>
          </View>
        )}
        {status && status.messageId === m.id && (
          <Text style={styles.status}>{status.label}</Text>
        )}
      </View>
    </View>
  );
}

/** Message body — code spans, links, line breaks (shared bodySegments). */
function BodyText({ text, mine }: { text: string; mine: boolean }) {
  const segs = bodySegments(text);
  return (
    <Text style={[styles.body, mine && styles.bodyMine]}>
      {segs.map((seg, i) => {
        switch (seg.kind) {
          case "code":
            return (
              <Text key={i} style={[styles.code, mine && styles.codeMine]}>
                {seg.text}
              </Text>
            );
          case "link":
            return (
              <Text
                key={i}
                style={[styles.link, mine && styles.linkMine]}
                onPress={() => void Linking.openURL(seg.href).catch(() => {})}
                suppressHighlighting
              >
                {seg.text}
              </Text>
            );
          case "br":
            return <Text key={i}>{"\n"}</Text>;
          default:
            return <Text key={i}>{seg.text}</Text>;
        }
      })}
    </Text>
  );
}

/** "replying to <name>: preview" chip — tap jumps to the target. */
function ReplyRef({
  target,
  mine,
  onJump,
}: {
  target: ChatMessage;
  mine: boolean;
  onJump: (id: string) => void;
}) {
  const s = useChatState();
  const tc = colorFor(target.user_id);
  const preview = (
    target.text || (target.attachments?.[0] ? `Attachment: ${target.attachments[0].name}` : "")
  )
    .replace(/\s+/g, " ")
    .slice(0, 80);
  return (
    <Pressable
      style={[styles.replyRef, mine && styles.replyRefMine]}
      onPress={() => onJump(target.id)}
    >
      <Icon name="arrow-undo" size={11} color={mine ? "rgba(255,255,255,0.8)" : colors.muted} />
      <Text style={[styles.replyLabel, mine && styles.replyLabelMine]}>replying to </Text>
      <Text style={[styles.replyName, { color: mine ? colors.sageMint : tc }]}>
        {nameFor(s.users, target.user_id)}
      </Text>
      <Text style={[styles.replyPreview, mine && styles.replyPreviewMine]} numberOfLines={1}>
        {preview}
      </Text>
    </Pressable>
  );
}

/** Inline editor for own text messages. */
function EditArea({ m, mine, onDone }: { m: ChatMessage; mine: boolean; onDone: () => void }) {
  const { store } = useSession();
  const [val, setVal] = useState(m.text || "");
  const save = () => {
    const text = val.trim();
    if (text && text !== m.text) store.editMessage(m, text);
    onDone();
  };
  return (
    <View>
      <TextInput
        style={[styles.editInput, mine && styles.editInputMine]}
        value={val}
        onChangeText={setVal}
        multiline
        autoFocus
        accessibilityLabel="Edit message"
        testID="edit-input"
      />
      <View style={styles.editActions}>
        <Pressable onPress={onDone} style={styles.editBtn}>
          <Text style={[styles.editCancel, mine && styles.editTextMine]}>Cancel</Text>
        </Pressable>
        <Pressable onPress={save} style={styles.editBtn}>
          <Text style={[styles.editSave, mine && styles.editTextMine]}>Save</Text>
        </Pressable>
      </View>
    </View>
  );
}

/** Link preview card — hydrates lazily via the store's negative cache. */
function LinkPreviewSlot({ text }: { text: string }) {
  const s = useChatState();
  const { store } = useSession();
  const [p, setP] = useState<LinkPreview | null>(null);
  const url = firstUrl(text || "");
  void s;

  useEffect(() => {
    let live = true;
    if (!url) return;
    const cached = store.state.linkPreviews[url];
    if (cached === "none") return;
    if (cached) {
      setP(cached);
      return;
    }
    store
      .linkPreview(url)
      .then((res) => {
        if (live) setP(res);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [url, store]);

  if (!p || (!p.title && !p.image)) return null;
  let host = "";
  try {
    host = new URL(p.url).host;
  } catch {
    /* ignore */
  }
  return (
    <Pressable
      style={styles.linkPreview}
      onPress={() => void Linking.openURL(p.url).catch(() => {})}
    >
      {!!p.image && (
        <ImagePreview uri={p.image} />
      )}
      <View style={styles.lpMeta}>
        <Text style={styles.lpSite} numberOfLines={1}>
          {p.site_name || host}
        </Text>
        {!!p.title && (
          <Text style={styles.lpTitle} numberOfLines={2}>
            {p.title}
          </Text>
        )}
        {!!p.description && (
          <Text style={styles.lpDesc} numberOfLines={2}>
            {p.description}
          </Text>
        )}
      </View>
    </Pressable>
  );
}

function ImagePreview({ uri }: { uri: string }) {
  const { api } = useSession();
  const src = uri.startsWith("http") ? uri : api.url(uri);
  return (
    <View style={styles.lpImgWrap}>
      <Image source={{ uri: src }} style={styles.lpImg} resizeMode="cover" />
    </View>
  );
}

const styles = StyleSheet.create({
  systemRow: {
    alignItems: "center",
    paddingVertical: 6,
  },
  systemText: {
    fontSize: 12,
    color: colors.faint,
    fontStyle: "italic",
  },
  row: {
    flexDirection: "row",
    paddingHorizontal: 12,
    marginTop: 2,
    marginBottom: 2,
  },
  rowMine: {},
  avSlot: {
    width: 40,
    alignItems: "center",
    paddingTop: 2,
  },
  gutterTime: {
    fontSize: 10,
    color: colors.faint,
    marginTop: 12,
  },
  col: {
    flex: 1,
    maxWidth: "82%",
  },
  colMine: {
    alignSelf: "flex-end",
    alignItems: "flex-end",
  },
  head: {
    flexDirection: "row",
    alignItems: "baseline",
    gap: 8,
    marginBottom: 3,
    marginLeft: 2,
  },
  headMine: {
    alignSelf: "flex-end",
    marginRight: 2,
  },
  author: {
    fontSize: 13,
    fontWeight: "700",
  },
  time: {
    fontSize: 11,
    color: colors.faint,
  },
  bubble: {
    borderRadius: 18,
    paddingHorizontal: 14,
    paddingVertical: 9,
  },
  bubbleThem: {
    backgroundColor: colors.surface,
    borderTopLeftRadius: 6,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line2,
  },
  bubbleMine: {
    backgroundColor: colors.sage,
    borderTopRightRadius: 6,
  },
  body: {
    fontSize: 15,
    lineHeight: 21,
    color: colors.ink,
  },
  bodyMine: {
    color: colors.surface,
  },
  code: {
    fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
    fontSize: 13.5,
    backgroundColor: "rgba(27,36,31,0.08)",
  },
  codeMine: {
    backgroundColor: "rgba(255,255,255,0.18)",
  },
  link: {
    color: colors.sage,
    textDecorationLine: "underline",
  },
  linkMine: {
    color: colors.sageMint,
  },
  editedTag: {
    fontSize: 11,
    color: colors.faint,
    marginTop: 2,
  },
  editedTagMine: {
    color: "rgba(255,255,255,0.7)",
  },
  replyRef: {
    flexDirection: "row",
    alignItems: "center",
    gap: 4,
    backgroundColor: "rgba(27,36,31,0.05)",
    borderRadius: radius.s,
    paddingHorizontal: 8,
    paddingVertical: 5,
    marginBottom: 6,
    maxWidth: "100%",
  },
  replyRefMine: {
    backgroundColor: "rgba(255,255,255,0.14)",
  },
  replyLabel: {
    fontSize: 11.5,
    color: colors.muted,
  },
  replyLabelMine: {
    color: "rgba(255,255,255,0.8)",
  },
  replyName: {
    fontSize: 11.5,
    fontWeight: "700",
  },
  replyPreview: {
    fontSize: 11.5,
    color: colors.muted,
    flexShrink: 1,
  },
  replyPreviewMine: {
    color: "rgba(255,255,255,0.8)",
  },
  atts: {
    gap: 8,
    marginTop: 4,
  },
  statusRow: {
    flexDirection: "row",
    justifyContent: "flex-end",
    marginTop: 3,
    marginRight: 2,
  },
  statusFailed: {
    fontSize: 11,
    color: colors.danger,
  },
  statusRetry: {
    fontSize: 11,
    color: colors.sage,
    fontWeight: "700",
  },
  status: {
    fontSize: 11,
    color: colors.faint,
    marginTop: 3,
    marginRight: 2,
    alignSelf: "flex-end",
  },
  editInput: {
    minWidth: 200,
    fontSize: 15,
    color: colors.ink,
    paddingVertical: 2,
    maxHeight: 160,
  },
  editInputMine: {
    color: colors.surface,
  },
  editActions: {
    flexDirection: "row",
    justifyContent: "flex-end",
    gap: 14,
    marginTop: 6,
  },
  editBtn: {
    paddingVertical: 2,
    paddingHorizontal: 4,
  },
  editCancel: {
    fontSize: 13,
    color: colors.muted,
    fontWeight: "600",
  },
  editSave: {
    fontSize: 13,
    color: colors.sageDeep,
    fontWeight: "700",
  },
  editTextMine: {
    color: colors.surface,
  },
  linkPreview: {
    marginTop: 6,
    backgroundColor: colors.surface,
    borderRadius: radius.m,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
    overflow: "hidden",
    maxWidth: 300,
  },
  lpImgWrap: {
    width: "100%",
    height: 120,
    backgroundColor: colors.line2,
  },
  lpImg: {
    width: "100%",
    height: "100%",
  },
  lpMeta: {
    padding: 10,
    gap: 2,
  },
  lpSite: {
    fontSize: 10.5,
    fontWeight: "700",
    letterSpacing: 0.6,
    textTransform: "uppercase",
    color: colors.faint,
  },
  lpTitle: {
    fontSize: 13.5,
    fontWeight: "700",
    color: colors.ink,
  },
  lpDesc: {
    fontSize: 12,
    color: colors.muted,
    lineHeight: 16,
  },
});
