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
  firstUrl,
  fmtStampLabel,
  nameFor,
  type ChatMessage,
  type LinkPreview,
} from "@alexmessages/shared";
import { toast, useChatState, useSession } from "../session";
import { colors, type } from "../theme";
import { Icon } from "./Icon";
import { AttachmentView } from "./attachments";
import { LongPressMenu, type MenuAction } from "./NativeMenu";

const R = 20;
const R_JOIN = 6;

/**
 * One message in a 1:1 thread, iMessage-style: own messages right-aligned in
 * sage, the peer's left-aligned in warm grey, no avatars or names (it's a DM).
 * `first`/`last` say where the bubble sits in its run so the joined corners
 * tighten. Long-press → Reply / Edit / Copy.
 */
export function Bubble({
  m,
  first,
  last,
  allMsgs,
  maxWidth,
  onImagePress,
  onJumpTo,
}: {
  m: ChatMessage;
  first: boolean;
  last: boolean;
  allMsgs: ChatMessage[];
  /** Widest a bubble may be (the row width less the far-side gutter). */
  maxWidth: number;
  onImagePress: (url: string) => void;
  onJumpTo: (id: string) => void;
}) {
  const s = useChatState();
  const { store } = useSession();
  const mine = s.me != null && m.user_id === s.me.id;
  const [editing, setEditing] = useState(false);
  const status = store.deliveryStatus(m.channel);
  const replyTarget = m.reply_to
    ? allMsgs.find((x) => x.id === m.reply_to)
    : null;

  if (m.type === "system") {
    return <Text style={styles.systemText}>{m.text}</Text>;
  }

  const hasText = !!m.text;
  const atts = m.attachments || [];
  const mediaOnly =
    !hasText &&
    atts.length > 0 &&
    atts.every((a) => /^image\//.test(a.mime || ""));

  const actions: MenuAction[] = [
    {
      label: "Reply",
      systemImage: "arrowshape.turn.up.left",
      onPress: () => store.setReplyTo(m),
    },
  ];
  if (mine && hasText) {
    actions.push({
      label: "Edit",
      systemImage: "pencil",
      onPress: () => setEditing(true),
    });
  }
  if (hasText) {
    actions.push({
      label: "Copy",
      systemImage: "doc.on.doc",
      onPress: () =>
        void Clipboard.setStringAsync(m.text || "").then(() => toast("Copied")),
    });
  }

  // Joined corners: the side facing the run tightens where a neighbour sits.
  const shape = mine
    ? {
        borderTopRightRadius: first ? R : R_JOIN,
        borderBottomRightRadius: last ? R : R_JOIN,
      }
    : {
        borderTopLeftRadius: first ? R : R_JOIN,
        borderBottomLeftRadius: last ? R : R_JOIN,
      };

  return (
    <View
      style={[
        styles.row,
        mine ? styles.rowMine : styles.rowThem,
        first && styles.rowFirst,
      ]}
    >
      {replyTarget && (
        <ReplyQuote
          target={replyTarget}
          mine={mine}
          author={m.user_id}
          onJump={onJumpTo}
        />
      )}
      <View style={[styles.lineRow, mine && styles.lineRowMine]}>
        {m._status === "failed" && (
          <Icon name="alert-circle" size={22} color={colors.danger} />
        )}
        <LongPressMenu
          actions={actions}
          cornerRadius={R}
          title={fmtStampLabel(m.created_at)}
          disabled={editing}
          style={styles.menuHost}
        >
          <View
            style={[
              styles.bubble,
              mediaOnly
                ? styles.bubbleMedia
                : mine
                  ? styles.bubbleMine
                  : styles.bubbleThem,
              shape,
              { maxWidth },
              m._status === "sending" && { opacity: 0.75 },
            ]}
          >
            {editing ? (
              <EditArea m={m} mine={mine} onDone={() => setEditing(false)} />
            ) : (
              hasText && <BodyText text={m.text} mine={mine} />
            )}
            {atts.length > 0 && (
              <View style={[styles.atts, hasText && { marginTop: 8 }]}>
                {atts.map((a, i) => (
                  <AttachmentView
                    key={i}
                    a={a}
                    mine={mine}
                    onImagePress={onImagePress}
                  />
                ))}
              </View>
            )}
          </View>
        </LongPressMenu>
      </View>
      <LinkPreviewSlot text={m.text || ""} mine={mine} />
      {m.edited_at != null && !editing && (
        <Text style={styles.meta}>Edited</Text>
      )}
      {m._status === "failed" ? (
        <Pressable
          onPress={() => store.retrySend(m.client_id || m.id)}
          accessibilityRole="button"
          hitSlop={8}
        >
          <Text style={[styles.meta, styles.metaFailed]}>
            Not Delivered · <Text style={styles.metaRetry}>Retry</Text>
          </Text>
        </Pressable>
      ) : (
        status &&
        status.messageId === m.id && (
          <Text
            style={[styles.meta, status.label === "Read" && styles.metaRead]}
          >
            {status.label}
          </Text>
        )
      )}
    </View>
  );
}

/** Message body — code spans, links, line breaks (shared bodySegments). */
function BodyText({ text, mine }: { text: string; mine: boolean }) {
  const segs = bodySegments(text);
  return (
    <Text style={[styles.body, mine && styles.bodyMine]} selectable={false}>
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

/** Faded quote of the replied-to message above the reply — tap jumps to it. */
function ReplyQuote({
  target,
  mine,
  author,
  onJump,
}: {
  target: ChatMessage;
  mine: boolean;
  author: number | null;
  onJump: (id: string) => void;
}) {
  const s = useChatState();
  const meId = s.me?.id;
  const who = (uid: number | null) =>
    uid === meId ? "you" : nameFor(s.users, uid);
  const label = mine
    ? `You replied to ${who(target.user_id)}`
    : `${nameFor(s.users, author)} replied to ${who(target.user_id)}`;
  const preview = (
    target.text ||
    (target.attachments?.[0] ? `Attachment: ${target.attachments[0].name}` : "")
  )
    .replace(/\s+/g, " ")
    .slice(0, 120);
  return (
    <Pressable
      onPress={() => onJump(target.id)}
      style={[styles.quoteWrap, mine && styles.quoteWrapMine]}
      accessibilityRole="button"
      accessibilityLabel={`${label}: ${preview}`}
    >
      <Text style={styles.quoteLabel}>{label}</Text>
      <View style={[styles.quoteRow, mine && styles.quoteRowMine]}>
        <View style={styles.quoteBar} />
        <Text style={styles.quoteText} numberOfLines={2}>
          {preview}
        </Text>
      </View>
    </Pressable>
  );
}

/** Inline editor for own text messages. */
function EditArea({
  m,
  mine,
  onDone,
}: {
  m: ChatMessage;
  mine: boolean;
  onDone: () => void;
}) {
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
        style={[styles.editInput, mine && styles.bodyMine]}
        value={val}
        onChangeText={setVal}
        multiline
        autoFocus
        selectionColor={mine ? colors.surface : colors.sage}
        accessibilityLabel="Edit message"
        testID="edit-input"
      />
      <View style={styles.editActions}>
        <Pressable onPress={onDone} hitSlop={8} accessibilityRole="button">
          <Text style={[styles.editBtn, mine && styles.editBtnMine]}>
            Cancel
          </Text>
        </Pressable>
        <Pressable onPress={save} hitSlop={8} accessibilityRole="button">
          <Text
            style={[
              styles.editBtn,
              styles.editSave,
              mine && styles.editBtnMine,
            ]}
          >
            Save
          </Text>
        </Pressable>
      </View>
    </View>
  );
}

/** Link preview card under the bubble — hydrates lazily via the store cache. */
function LinkPreviewSlot({ text, mine }: { text: string; mine: boolean }) {
  const { store, api } = useSession();
  const [p, setP] = useState<LinkPreview | null>(null);
  const url = firstUrl(text || "");

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
    host = new URL(p.url).host.replace(/^www\./, "");
  } catch {
    /* ignore */
  }
  const img = p.image
    ? p.image.startsWith("http")
      ? p.image
      : api.url(p.image)
    : null;
  return (
    <Pressable
      style={[styles.lp, mine && styles.lpMine]}
      onPress={() => void Linking.openURL(p.url).catch(() => {})}
      accessibilityRole="link"
    >
      {img && (
        <Image source={{ uri: img }} style={styles.lpImg} resizeMode="cover" />
      )}
      <View style={styles.lpMeta}>
        {!!p.title && (
          <Text style={styles.lpTitle} numberOfLines={2}>
            {p.title}
          </Text>
        )}
        <Text style={styles.lpHost} numberOfLines={1}>
          {p.site_name || host}
        </Text>
      </View>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  systemText: {
    ...type.caption,
    color: colors.faint,
    textAlign: "center",
    paddingVertical: 6,
  },
  row: {
    paddingHorizontal: 12,
    marginTop: 2,
  },
  rowFirst: {
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
  lineRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    maxWidth: "100%",
  },
  lineRowMine: {
    justifyContent: "flex-end",
  },
  menuHost: {
    flexShrink: 1,
  },
  bubble: {
    borderRadius: R,
    paddingHorizontal: 14,
    paddingVertical: 8,
    flexShrink: 1,
  },
  bubbleThem: {
    backgroundColor: colors.bubbleThem,
  },
  bubbleMine: {
    backgroundColor: colors.sage,
  },
  bubbleMedia: {
    paddingHorizontal: 0,
    paddingVertical: 0,
    overflow: "hidden",
  },
  body: {
    ...type.body,
    lineHeight: 22,
    color: colors.ink,
  },
  bodyMine: {
    color: colors.surface,
  },
  code: {
    fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
    fontSize: 15,
    backgroundColor: "rgba(27,36,31,0.08)",
  },
  codeMine: {
    backgroundColor: "rgba(255,255,255,0.18)",
  },
  link: {
    color: colors.sageDeep,
    textDecorationLine: "underline",
  },
  linkMine: {
    color: colors.surface,
  },
  atts: {
    gap: 6,
  },
  meta: {
    ...type.caption,
    color: colors.muted,
    marginTop: 3,
    marginHorizontal: 6,
  },
  metaRead: {
    fontWeight: "600",
  },
  metaFailed: {
    color: colors.danger,
  },
  metaRetry: {
    fontWeight: "700",
  },
  quoteWrap: {
    marginTop: 8,
    marginBottom: 3,
    maxWidth: "88%",
    alignItems: "flex-start",
  },
  quoteWrapMine: {
    alignItems: "flex-end",
  },
  quoteLabel: {
    ...type.caption,
    color: colors.muted,
    marginBottom: 3,
    marginHorizontal: 6,
  },
  quoteRow: {
    flexDirection: "row",
    gap: 8,
    paddingVertical: 7,
    paddingRight: 12,
    paddingLeft: 10,
    borderRadius: 14,
    backgroundColor: "rgba(27,36,31,0.05)",
  },
  quoteRowMine: {
    backgroundColor: "rgba(79,122,94,0.12)",
  },
  quoteBar: {
    width: 3,
    borderRadius: 2,
    backgroundColor: colors.sage,
    opacity: 0.6,
  },
  quoteText: {
    ...type.subhead,
    color: colors.ink2,
    flexShrink: 1,
  },
  editInput: {
    minWidth: 180,
    ...type.body,
    color: colors.ink,
    paddingVertical: 2,
    maxHeight: 180,
  },
  editActions: {
    flexDirection: "row",
    justifyContent: "flex-end",
    gap: 18,
    marginTop: 8,
    marginBottom: 2,
  },
  editBtn: {
    ...type.subhead,
    fontWeight: "500",
    color: colors.muted,
  },
  editSave: {
    fontWeight: "700",
    color: colors.sageDeep,
  },
  editBtnMine: {
    color: colors.surface,
  },
  lp: {
    marginTop: 4,
    width: 260,
    borderRadius: 18,
    overflow: "hidden",
    backgroundColor: colors.bubbleThem,
  },
  lpMine: {
    alignSelf: "flex-end",
  },
  lpImg: {
    width: "100%",
    height: 140,
    backgroundColor: colors.line2,
  },
  lpMeta: {
    paddingHorizontal: 14,
    paddingVertical: 10,
    gap: 2,
  },
  lpTitle: {
    ...type.subhead,
    fontWeight: "600",
    color: colors.ink,
  },
  lpHost: {
    ...type.footnote,
    color: colors.muted,
  },
});
