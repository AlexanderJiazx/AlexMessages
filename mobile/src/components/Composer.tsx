import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Image,
  Keyboard,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import Animated, {
  FadeIn,
  FadeOut,
  useAnimatedStyle,
  useSharedValue,
  withDelay,
  withSpring,
  withTiming,
  type SharedValue,
} from "react-native-reanimated";
import { GlassContainer } from "expo-glass-effect";
import * as ImagePicker from "expo-image-picker";
import * as DocumentPicker from "expo-document-picker";
import * as Haptics from "expo-haptics";
// Deep import (see Icon.tsx) — avoids bundling every vector-icon font.
import MaterialCommunityIcons from "@expo/vector-icons/build/MaterialCommunityIcons";
import { Host, Image as SFImage } from "./SwiftUI";
import { fmtSize, nameFor, type Attachment } from "@alexmessages/shared";
import { toast, useChatState, useSession } from "../session";
import { colors, glassSupported, radius, type } from "../theme";
import { Icon } from "./Icon";
import { Glyph, VoicePanel, type VoicePanelHandle, type VoicePhase } from "./DictationBar";
import { GlassSurface } from "./Glass";
import { GlassMenuButton, type MenuAction } from "./NativeMenu";

const PILL_MIN = 44;
const PILL_VOICE = 64;
/**
 * The input's line height. Fixed, and a whole number of points: with SF's
 * natural 20.29pt line, UIKit rounds the text view's content height up to
 * whole points (43pt for one line) while layout sizes the view to the
 * nearest third (42.33pt) — content a fraction taller than the view, so even
 * a single line could be dragged, scroll bar and all. With 21pt lines both
 * come out at exactly 21·n + 22.
 */
const INPUT_LINE = 21;
/** The input grows to six full lines, then scrolls. */
const INPUT_MAX = 6 * INPUT_LINE + 22;
/** Links the input to ConversationView's KeyboardGestureArea. */
export const COMPOSER_INPUT_ID = "composer-input";
/**
 * Headroom the voice morph grows into. The composer reserves it as top
 * padding that the pill swallows, so the dock's layout height never changes
 * mid-morph; the host lifts the message list by the same amount on the UI
 * thread (see `voiceTall`) instead of chasing late onLayout events.
 */
export const VOICE_LIFT = PILL_VOICE - PILL_MIN;
const SLOT = 44;
const GAP = 8;
/** Glass shapes closer than this melt together (the "+" being absorbed). */
const MERGE = 6;
const SPRING_IN = { duration: 520, dampingRatio: 0.78 };
const SPRING_OUT = { duration: 380, dampingRatio: 0.72 };
/** Matches the recorder row's exiting FadeOut in DictationBar. */
const REC_EXIT_MS = 120;

/**
 * Floating composer, iOS 26 Messages-style: a round glass "+" (attach menu)
 * beside a glass pill holding the reply preview, pending-attachment
 * thumbnails, the autosizing input, and either the waveform (voice message)
 * or the sage send button.
 *
 * Voice messages morph in place like Messages: the pill swells and swallows
 * the "+" while the placeholder dissolves into a live waveform and the
 * waveform glyph becomes stop; stopping peels an X back out on the left and
 * turns the pill into a player with send. Both glass shapes sit in one
 * GlassContainer so they merge while overlapping.
 */
export function Composer({
  peerName,
  voiceTall,
}: {
  peerName: string;
  /** Morph progress (0 idle → 1 recorder height), shared with the host. */
  voiceTall?: SharedValue<number>;
}) {
  const s = useChatState();
  const { store, api } = useSession();
  const [text, setText] = useState("");
  const [voice, setVoice] = useState<VoicePhase | null>(null);
  const voiceRef = useRef<VoicePanelHandle>(null);
  const [uploading, setUploading] = useState<string | null>(null);

  const hasContent = !!(text.trim() || s.pendingAtt.length);
  const canSend = hasContent && !voice;

  const doSend = useCallback(() => {
    if (!canSend) return;
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(() => {});
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
      const mime = asset.mimeType || (asset.type === "video" ? "video/mp4" : "image/jpeg");
      await uploadOne({ uri: asset.uri, name, type: mime, size: asset.fileSize ?? undefined });
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

  const attachActions: MenuAction[] = [
    { label: "Photo Library", systemImage: "photo.on.rectangle", onPress: () => void pickFromLibrary() },
    { label: "Files", systemImage: "folder", onPress: () => void pickDocument() },
  ];

  const replyTo = s.replyTo;

  // ---- voice-message morph ----
  // slot: the left circle (1 = "+"/X shown, 0 = swallowed by the pill).
  // tall: the pill's voice height. idleFade: the text row's opacity.
  const slot = useSharedValue(1);
  const ownTall = useSharedValue(0);
  const tall = voiceTall ?? ownTall;
  const idleFade = useSharedValue(1);
  // The "+" is a native glass menu button; during the morph a GlassView twin
  // (which can merge with the pill) stands in for it. The twin is mounted
  // only when needed: a GlassView first laid out while hidden never picks
  // its effect back up.
  const [menuShown, setMenuShown] = useState(true);
  // Corner radius: half the one-line height while typing, so a multi-line
  // draft reads as a rounded rect (like Messages) instead of a stadium whose
  // curve eats the first line. The voice morph needs a capsule at every
  // height it passes through, so it switches to the tall radius for the
  // whole recording and only drops back once the collapse has settled.
  const [capsule, setCapsule] = useState(false);
  const prev = useRef<VoicePhase | null>(null);

  useEffect(() => {
    const from = prev.current;
    prev.current = voice;
    if (from === voice) return;
    if (voice === "recording") {
      setMenuShown(false);
      setCapsule(true);
      idleFade.value = withTiming(0, { duration: 140 });
      tall.value = withSpring(1, SPRING_IN);
      slot.value = withDelay(160, withSpring(0, SPRING_IN));
    } else if (voice === "review") {
      // Peel the "X" out only once the recorder has faded (VoicePanel's
      // 120ms exit): an exiting view is frozen at its old width, so narrowing
      // the pill under it slides the stop button out past the right edge.
      slot.value = withDelay(REC_EXIT_MS, withSpring(1, SPRING_OUT));
    } else {
      tall.value = withSpring(0, SPRING_OUT);
      slot.value = withSpring(1, SPRING_OUT);
      idleFade.value = withDelay(60, withTiming(1, { duration: 200 }));
      const t = setTimeout(() => {
        setMenuShown(true);
        setCapsule(false);
      }, 420);
      return () => clearTimeout(t);
    }
  }, [voice, slot, tall, idleFade]);

  const wrapStyle = useAnimatedStyle(() => ({
    // While recording the pill also reaches a little past the margins.
    paddingHorizontal: 12 - 4 * tall.value * (1 - slot.value),
  }));
  const slotStyle = useAnimatedStyle(() => ({
    width: SLOT * slot.value,
    marginRight: GAP * slot.value,
    marginBottom: ((PILL_VOICE - PILL_MIN) / 2) * tall.value,
  }));
  const circleStyle = useAnimatedStyle(() => ({
    transform: [{ translateX: (1 - slot.value) * 20 }, { scale: 0.55 + 0.45 * slot.value }],
  }));
  const rowStyle = useAnimatedStyle(() => ({
    minHeight: PILL_MIN + (PILL_VOICE - PILL_MIN) * tall.value,
  }));
  const idleStyle = useAnimatedStyle(() => ({ opacity: idleFade.value }));
  const reserveStyle = useAnimatedStyle(() => ({
    paddingTop: VOICE_LIFT * (1 - tall.value),
  }));

  const startVoice = () => {
    Keyboard.dismiss();
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(() => {});
    setVoice("recording");
  };

  return (
    <Animated.View style={reserveStyle} pointerEvents="box-none">
      {uploading && !voice && (
        <View style={styles.uploading}>
          <GlassSurface style={styles.uploadingChip}>
            <ActivityIndicator size="small" color={colors.sage} />
            <Text style={styles.uploadingText} numberOfLines={1}>
              Uploading {uploading}…
            </Text>
          </GlassSurface>
        </View>
      )}
      <GlassContainer spacing={MERGE}>
        <Animated.View style={[styles.wrap, wrapStyle]}>
          <Animated.View style={[styles.slot, slotStyle]}>
            {!menuShown && (
              <Animated.View
                style={[styles.slotCircle, circleStyle]}
                pointerEvents={voice === "review" ? "auto" : "none"}
              >
                <Pressable
                  onPress={() => voiceRef.current?.discard()}
                  accessibilityLabel={voice === "review" ? "Delete voice message" : undefined}
                  accessibilityElementsHidden={voice !== "review"}
                  accessibilityRole="button"
                  hitSlop={6}
                >
                  <GlassSurface interactive style={styles.circle}>
                    {voice === "review" ? (
                      <Animated.View key="x" entering={FadeIn.delay(REC_EXIT_MS + 60).duration(160)} exiting={FadeOut.duration(100)}>
                        <Glyph sf="xmark" ion="close" size={17} color={colors.ink2} />
                      </Animated.View>
                    ) : (
                      <Animated.View key="plus" style={idleStyle}>
                        <Glyph sf="plus" ion="add" size={18} color={colors.ink2} />
                      </Animated.View>
                    )}
                  </GlassSurface>
                </Pressable>
              </Animated.View>
            )}
            {/* Never unmounted — hidden with opacity during the morph, so the
                swap back doesn't flash a freshly mounted button. */}
            <View
              style={[styles.slotCircle, !menuShown && styles.hidden]}
              pointerEvents={menuShown ? "auto" : "none"}
              accessibilityElementsHidden={!menuShown}
              importantForAccessibility={menuShown ? "auto" : "no-hide-descendants"}
            >
              <GlassMenuButton
                actions={attachActions}
                systemImage="plus"
                fallbackIcon="add"
                label="Attach"
                title="Attach"
              />
            </View>
          </Animated.View>
          <GlassSurface interactive style={[styles.pill, capsule && styles.pillCapsule]}>
            {replyTo && !voice && (
              <View style={styles.replyBar}>
                <View style={styles.replyAccent} />
                <View style={styles.flex}>
                  <Text style={styles.replyTitle} numberOfLines={1}>
                    Replying to {nameFor(s.users, replyTo.user_id)}
                  </Text>
                  <Text style={styles.replyText} numberOfLines={1}>
                    {replyTo.text ||
                      (replyTo.attachments?.[0] ? `Attachment: ${replyTo.attachments[0].name}` : "")}
                  </Text>
                </View>
                <Pressable
                  onPress={() => store.setReplyTo(null)}
                  accessibilityLabel="Cancel reply"
                  hitSlop={10}
                  style={styles.replyClose}
                >
                  <Icon name="close" size={14} color={colors.muted} />
                </Pressable>
              </View>
            )}

            {!!s.pendingAtt.length && !voice && (
              <ScrollView
                horizontal
                showsHorizontalScrollIndicator={false}
                contentContainerStyle={styles.pendingRow}
              >
                {s.pendingAtt.map((a, i) => (
                  <PendingAtt
                    key={i}
                    a={a}
                    apiUrl={api.url(a.url)}
                    onRemove={() => store.removePendingAtt(i)}
                  />
                ))}
              </ScrollView>
            )}

            <Animated.View style={[styles.inputRow, rowStyle]}>
              <Animated.View
                style={[styles.inputLine, idleStyle]}
                pointerEvents={voice ? "none" : "auto"}
              >
                <TextInput
                  style={styles.input}
                  placeholder={s.activeChannel ? `Message ${peerName}` : "Message"}
                  placeholderTextColor={colors.muted}
                  value={text}
                  onChangeText={setText}
                  multiline
                  nativeID={COMPOSER_INPUT_ID}
                  editable={!voice}
                  selectionColor={colors.sage}
                  accessibilityLabel="Message input"
                  testID="composer-input"
                />
                {hasContent ? (
                  <Pressable
                    style={({ pressed }) => [styles.sendBtn, pressed && { opacity: 0.8 }]}
                    onPress={doSend}
                    disabled={!canSend}
                    accessibilityLabel="Send"
                    accessibilityRole="button"
                    hitSlop={6}
                  >
                    <Icon name="arrow-up" size={20} color={colors.surface} />
                  </Pressable>
                ) : (
                  <Pressable
                    style={styles.micBtn}
                    onPress={startVoice}
                    accessibilityLabel="Dictate"
                    accessibilityRole="button"
                    hitSlop={6}
                  >
                    <WaveformIcon />
                  </Pressable>
                )}
              </Animated.View>
              {voice && (
                <Animated.View style={StyleSheet.absoluteFill} exiting={FadeOut.duration(140)}>
                  <VoicePanel
                    ref={voiceRef}
                    phase={voice}
                    onPhase={setVoice}
                    onExit={() => setVoice(null)}
                    onTranscribed={(t) => setText((cur) => (cur ? `${cur} ${t}` : t))}
                  />
                </Animated.View>
              )}
            </Animated.View>
          </GlassSurface>
        </Animated.View>
      </GlassContainer>
    </Animated.View>
  );
}

/**
 * Voice-message glyph: the SF Symbol `waveform` on iOS 26 (rendered by
 * SwiftUI, non-interactive so the RN Pressable keeps the touch), Material's
 * waveform elsewhere. `ignoreSafeArea`: the Host's UIHostingController
 * otherwise applies the keyboard safe area, which pushes the glyph to the
 * top of the pill whenever the keyboard is up.
 */
function WaveformIcon() {
  if (glassSupported) {
    return (
      <Host matchContents ignoreSafeArea="all" pointerEvents="none">
        <SFImage systemName="waveform" size={19} color={colors.muted} />
      </Host>
    );
  }
  return <MaterialCommunityIcons name="waveform" size={22} color={colors.muted} />;
}

/** Staged attachment — image thumbnail or a compact file chip, with an ✕. */
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
    <View style={styles.pendingItem}>
      {isImg ? (
        <Image source={{ uri: apiUrl }} style={styles.pendingImg} />
      ) : (
        <View style={styles.pendingChip}>
          <Icon name="document-outline" size={18} color={colors.sageDeep} />
          <View style={styles.flex}>
            <Text style={styles.pendingName} numberOfLines={1}>
              {a.name}
            </Text>
            <Text style={styles.pendingSize}>{fmtSize(a.size)}</Text>
          </View>
        </View>
      )}
      <Pressable style={styles.pendingX} onPress={onRemove} accessibilityLabel="Remove" hitSlop={8}>
        <Icon name="close" size={11} color={colors.surface} />
      </Pressable>
    </View>
  );
}

const styles = StyleSheet.create({
  flex: {
    flex: 1,
  },
  wrap: {
    flexDirection: "row",
    alignItems: "flex-end",
    paddingTop: 6,
    paddingBottom: 6,
  },
  slot: {
    height: SLOT,
  },
  slotCircle: {
    position: "absolute",
    left: 0,
    top: 0,
  },
  hidden: {
    opacity: 0,
  },
  circle: {
    width: SLOT,
    height: SLOT,
    borderRadius: SLOT / 2,
    alignItems: "center",
    justifyContent: "center",
  },
  pill: {
    flex: 1,
    minHeight: PILL_MIN,
    borderRadius: PILL_MIN / 2,
    overflow: glassSupported ? undefined : "hidden",
  },
  pillCapsule: {
    // Capsule at every height the morph passes through (clamped to h/2).
    borderRadius: PILL_VOICE / 2,
  },
  inputRow: {
    minHeight: PILL_MIN,
  },
  inputLine: {
    flexGrow: 1,
    flexDirection: "row",
    alignItems: "flex-end",
  },
  input: {
    flex: 1,
    ...type.body,
    lineHeight: INPUT_LINE,
    color: colors.ink,
    paddingLeft: 16,
    paddingRight: 6,
    paddingTop: 11,
    paddingBottom: 11,
    maxHeight: INPUT_MAX,
  },
  sendBtn: {
    width: 34,
    height: 34,
    borderRadius: 17,
    margin: 5,
    backgroundColor: colors.sage,
    alignItems: "center",
    justifyContent: "center",
  },
  micBtn: {
    width: 40,
    height: PILL_MIN,
    alignItems: "center",
    justifyContent: "center",
    marginRight: 2,
  },
  replyBar: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    paddingLeft: 14,
    paddingRight: 10,
    paddingTop: 10,
    paddingBottom: 4,
  },
  replyAccent: {
    width: 3,
    alignSelf: "stretch",
    borderRadius: 2,
    backgroundColor: colors.sage,
  },
  replyTitle: {
    ...type.footnote,
    fontWeight: "600",
    color: colors.sageDeep,
  },
  replyText: {
    ...type.footnote,
    color: colors.muted,
  },
  replyClose: {
    width: 24,
    height: 24,
    borderRadius: 12,
    backgroundColor: "rgba(27,36,31,0.08)",
    alignItems: "center",
    justifyContent: "center",
  },
  pendingRow: {
    gap: 10,
    paddingHorizontal: 12,
    paddingTop: 12,
    paddingBottom: 2,
  },
  pendingItem: {
    paddingTop: 4,
    paddingRight: 4,
  },
  pendingImg: {
    width: 64,
    height: 64,
    borderRadius: radius.m,
    backgroundColor: colors.line2,
  },
  pendingChip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    height: 64,
    width: 170,
    paddingHorizontal: 12,
    borderRadius: radius.m,
    backgroundColor: colors.sageTint,
  },
  pendingName: {
    ...type.footnote,
    fontWeight: "600",
    color: colors.ink,
  },
  pendingSize: {
    ...type.caption,
    color: colors.muted,
  },
  pendingX: {
    position: "absolute",
    top: 0,
    right: 0,
    width: 20,
    height: 20,
    borderRadius: 10,
    backgroundColor: "rgba(27,36,31,0.75)",
    alignItems: "center",
    justifyContent: "center",
  },
  uploading: {
    alignItems: "center",
    paddingBottom: 4,
  },
  uploadingChip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingHorizontal: 14,
    height: 34,
    borderRadius: 17,
    maxWidth: 280,
  },
  uploadingText: {
    ...type.footnote,
    color: colors.ink2,
    flexShrink: 1,
  },
});
