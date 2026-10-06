import React, { useCallback, useState } from "react";
import {
  ActivityIndicator,
  Image,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import * as ImagePicker from "expo-image-picker";
import * as DocumentPicker from "expo-document-picker";
import { fmtSize, nameFor, type Attachment } from "@alexmessages/shared";
import { toast, useChatState, useSession } from "../session";
import { colors, radius, shadow } from "../theme";
import { Icon } from "./Icon";
import { DictationBar } from "./DictationBar";
import { showActionSheet } from "./ActionSheet";

/**
 * The pill composer: reply bar, pending-attachment chips, autosizing input,
 * attach sheet (photo library / file), mic → DictationBar, sage send button.
 */
export function Composer({ peerName }: { peerName: string }) {
  const s = useChatState();
  const { store, api } = useSession();
  const [text, setText] = useState("");
  const [dictating, setDictating] = useState(false);
  const [uploading, setUploading] = useState<string | null>(null);

  const canSend = !!(text.trim() || s.pendingAtt.length) && !dictating;

  const doSend = useCallback(() => {
    if (!canSend) return;
    store.sendMessage(text);
    setText("");
  }, [canSend, store, text]);

  const uploadOne = useCallback(
    async (file: { uri: string; name: string; type: string; size?: number }) => {
      if (file.size && file.size > s.maxUpload) {
        toast(`${file.name} exceeds ${Math.round(s.maxUpload / 1024 / 1024)}MB`, true);
        return;
      }
      setUploading(file.name);
      await store.uploadAttachment({ uri: file.uri, name: file.name, type: file.type }, file.name);
      setUploading(null);
    },
    [s.maxUpload, store]
  );

  const pickFromLibrary = useCallback(async () => {
    const res = await ImagePicker.launchImageLibraryAsync({
      mediaTypes: ["images", "videos", "livePhotos"],
      allowsMultipleSelection: true,
      quality: 0.9,
    });
    if (res.canceled) return;
    for (const asset of res.assets) {
      const name = asset.fileName || `photo-${Date.now()}.jpg`;
      const type =
        asset.mimeType || (asset.type === "video" ? "video/mp4" : "image/jpeg");
      await uploadOne({ uri: asset.uri, name, type, size: asset.fileSize ?? undefined });
    }
  }, [uploadOne]);

  const pickDocument = useCallback(async () => {
    const res = await DocumentPicker.getDocumentAsync({ multiple: true, copyToCacheDirectory: true });
    if (res.canceled) return;
    for (const asset of res.assets) {
      await uploadOne({
        uri: asset.uri,
        name: asset.name,
        type: asset.mimeType || "application/octet-stream",
        size: asset.size ?? undefined,
      });
    }
  }, [uploadOne]);

  const openAttachSheet = useCallback(() => {
    showActionSheet({
      title: "Attach",
      options: [
        { label: "Photo Library", onPress: () => void pickFromLibrary() },
        { label: "Files", onPress: () => void pickDocument() },
      ],
    });
  }, [pickDocument, pickFromLibrary]);

  const replyTo = s.replyTo;

  return (
    <View style={styles.wrap}>
      <View style={styles.composer}>
        {replyTo && (
          <View style={styles.replyBar}>
            <Icon name="arrow-undo" size={13} color={colors.muted} />
            <Text style={styles.replyText} numberOfLines={1}>
              Replying to{" "}
              <Text style={styles.replyName}>{nameFor(s.users, replyTo.user_id)}</Text>
              {"  ·  "}
              {replyTo.text ||
                (replyTo.attachments?.[0] ? `Attachment: ${replyTo.attachments[0].name}` : "")}
            </Text>
            <Pressable
              onPress={() => store.setReplyTo(null)}
              accessibilityLabel="Cancel reply"
              hitSlop={8}
            >
              <Icon name="close" size={14} color={colors.muted} />
            </Pressable>
          </View>
        )}

        {!!s.pendingAtt.length && (
          <View style={styles.pendingRow}>
            {s.pendingAtt.map((a, i) => (
              <PendingAtt key={i} a={a} apiUrl={api.url(a.url)} onRemove={() => store.removePendingAtt(i)} />
            ))}
          </View>
        )}

        {dictating ? (
          <DictationBar
            onExit={() => setDictating(false)}
            onTranscribed={(t) => setText((cur) => (cur ? `${cur} ${t}` : t))}
          />
        ) : (
          <View style={styles.inputRow}>
            <Pressable
              style={styles.toolBtn}
              onPress={openAttachSheet}
              accessibilityLabel="Attach"
              hitSlop={6}
            >
              <Icon name="add" size={21} color={colors.muted} />
            </Pressable>
            <TextInput
              style={styles.input}
              placeholder={s.activeChannel ? `Message ${peerName}` : "Message"}
              placeholderTextColor={colors.faint}
              value={text}
              onChangeText={setText}
              multiline
              accessibilityLabel="Message input"
              testID="composer-input"
            />
            {text.trim() || s.pendingAtt.length ? (
              <Pressable
                style={[styles.sendBtn, !canSend && { opacity: 0.5 }]}
                onPress={doSend}
                disabled={!canSend}
                accessibilityLabel="Send"
                accessibilityRole="button"
              >
                <Icon name="arrow-up" size={18} color={colors.surface} />
              </Pressable>
            ) : (
              <Pressable
                style={styles.toolBtn}
                onPress={() => setDictating(true)}
                accessibilityLabel="Dictate"
                hitSlop={6}
              >
                <Icon name="mic-outline" size={20} color={colors.muted} />
              </Pressable>
            )}
          </View>
        )}
      </View>
      {uploading && (
        <View style={styles.metaRow}>
          <ActivityIndicator size="small" color={colors.sage} />
          <Text style={styles.metaText}>Uploading {uploading}…</Text>
        </View>
      )}
    </View>
  );
}

/** Staged attachment chip — image thumbnails get the iMessage-style preview. */
function PendingAtt({
  a,
  apiUrl,
  onRemove,
}: {
  a: Attachment;
  apiUrl: string;
  onRemove: () => void;
}) {
  const isImg = (a.mime || "").startsWith("image/");
  return (
    <View style={isImg ? styles.pendingThumb : styles.pendingChip}>
      {isImg ? (
        <Image source={{ uri: apiUrl }} style={styles.pendingImg} />
      ) : (
        <View style={styles.pendingChipBody}>
          <Text style={styles.pendingName} numberOfLines={1}>
            {a.name}
          </Text>
          <Text style={styles.pendingSize}>{fmtSize(a.size)}</Text>
        </View>
      )}
      <Pressable style={styles.pendingX} onPress={onRemove} accessibilityLabel="Remove" hitSlop={6}>
        <Icon name="close" size={10} color={colors.surface} />
      </Pressable>
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: {
    paddingHorizontal: 12,
    paddingBottom: 6,
    paddingTop: 4,
    backgroundColor: "transparent",
  },
  composer: {
    backgroundColor: colors.surface,
    borderRadius: 26,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line,
    paddingHorizontal: 8,
    paddingVertical: 6,
    ...shadow.card,
  },
  inputRow: {
    flexDirection: "row",
    alignItems: "flex-end",
    gap: 4,
  },
  toolBtn: {
    width: 38,
    height: 38,
    borderRadius: 19,
    alignItems: "center",
    justifyContent: "center",
  },
  input: {
    flex: 1,
    fontSize: 16,
    color: colors.ink,
    paddingHorizontal: 6,
    paddingTop: 9,
    paddingBottom: 9,
    maxHeight: 120,
  },
  sendBtn: {
    width: 38,
    height: 38,
    borderRadius: 19,
    backgroundColor: colors.sage,
    alignItems: "center",
    justifyContent: "center",
  },
  replyBar: {
    flexDirection: "row",
    alignItems: "center",
    gap: 7,
    paddingHorizontal: 10,
    paddingVertical: 7,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.line2,
    marginBottom: 2,
  },
  replyText: {
    flex: 1,
    fontSize: 12.5,
    color: colors.muted,
  },
  replyName: {
    fontWeight: "700",
    color: colors.ink2,
  },
  pendingRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
    paddingHorizontal: 8,
    paddingVertical: 6,
  },
  pendingThumb: {
    width: 56,
    height: 56,
    borderRadius: radius.s,
    overflow: "visible",
  },
  pendingImg: {
    width: 56,
    height: 56,
    borderRadius: radius.s,
  },
  pendingChip: {
    backgroundColor: colors.sageTint,
    borderRadius: radius.s,
    paddingHorizontal: 10,
    paddingVertical: 8,
    maxWidth: 160,
    overflow: "visible",
  },
  pendingChipBody: {
    gap: 1,
  },
  pendingName: {
    fontSize: 12,
    fontWeight: "600",
    color: colors.ink,
  },
  pendingSize: {
    fontSize: 10.5,
    color: colors.muted,
  },
  pendingX: {
    position: "absolute",
    top: -5,
    right: -5,
    width: 18,
    height: 18,
    borderRadius: 9,
    backgroundColor: colors.ink,
    alignItems: "center",
    justifyContent: "center",
  },
  metaRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    paddingHorizontal: 14,
    paddingTop: 4,
  },
  metaText: {
    fontSize: 12,
    color: colors.muted,
  },
});
