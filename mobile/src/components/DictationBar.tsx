import React, { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import Animated, { FadeIn, FadeOut } from "react-native-reanimated";
import { useAudioPlayer, useAudioPlayerStatus } from "expo-audio";
import * as Haptics from "expo-haptics";
import { Host, Image as SFImage } from "./SwiftUI";
import type { SFSymbol } from "sf-symbols-typescript";
import { toast, useSession } from "../session";
import { useVoiceRecorder } from "../audio";
import { colors, glassSupported, type } from "../theme";
import { Icon, type IconName } from "./Icon";

export type VoicePhase = "recording" | "review";

export interface VoicePanelHandle {
  /** Throw the recording away (the composer's X). */
  discard(): void;
}

const BAR_W = 3;
const BAR_GAP = 2;
const REC = colors.danger;

interface Clip {
  uri: string;
  mime: string;
  ext: string;
  millis: number;
}

/**
 * The voice-message contents of the composer pill, iOS 26 Messages-style.
 * The composer owns the geometry (the pill growing over the "+", the X
 * peeling back out); this panel owns the recorder and crossfades between
 *   recording — live waveform · timer · stop
 *   review    — play · recorded waveform · duration · transcribe · send
 */
export const VoicePanel = forwardRef<
  VoicePanelHandle,
  {
    phase: VoicePhase;
    onPhase: (p: VoicePhase) => void;
    onExit: () => void;
    onTranscribed: (text: string) => void;
  }
>(function VoicePanel({ phase, onPhase, onExit, onTranscribed }, ref) {
  const { api, store } = useSession();
  const rec = useVoiceRecorder();
  const recRef = useRef(rec);
  recRef.current = rec;
  const samples = useRef<number[]>([]);
  const [, bump] = useState(0);
  const [clip, setClip] = useState<Clip | null>(null);
  const [busy, setBusy] = useState<"send" | "transcribe" | null>(null);
  const player = useAudioPlayer(null);
  const status = useAudioPlayerStatus(player);

  useEffect(() => {
    void rec.start().then((ok) => {
      if (!ok) {
        toast("Microphone unavailable", true);
        onExit();
      }
    });
    // Leaving the conversation mid-recording must release the mic.
    return () => void recRef.current.cancel();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Each metering sample becomes one waveform bar.
  useEffect(() => {
    if (!rec.isRecording) return;
    samples.current.push(Math.max(0.04, rec.level));
    bump((x) => x + 1);
  }, [rec.level, rec.isRecording]);

  useEffect(() => {
    if (status.didJustFinish) {
      player.pause();
      void player.seekTo(0);
    }
  }, [status.didJustFinish, player]);

  useImperativeHandle(ref, () => ({
    discard() {
      player.pause();
      void rec.cancel();
      onExit();
    },
  }));

  const stop = async () => {
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(() => {});
    const millis = rec.durationMillis;
    const c = await rec.stop();
    if (!c) {
      toast("Recording too short", true);
      onExit();
      return;
    }
    player.replace({ uri: c.uri });
    setClip({ ...c, millis });
    onPhase("review");
  };

  const togglePlay = () => {
    if (status.playing) player.pause();
    else player.play();
  };

  const send = async () => {
    if (!clip) return;
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light).catch(() => {});
    player.pause();
    setBusy("send");
    const name = `voice-message.${clip.ext}`;
    const ok = await store.uploadAttachment({ uri: clip.uri, name, type: clip.mime }, name);
    if (!ok) {
      setBusy(null);
      return;
    }
    store.sendMessage("");
    onExit();
  };

  const transcribe = async () => {
    if (!clip) return;
    player.pause();
    setBusy("transcribe");
    try {
      const res = await api.transcribe({ uri: clip.uri, name: `dictation.${clip.ext}`, type: clip.mime });
      const t = (res.text || "").trim();
      if (!t) {
        toast("Nothing heard — try again", true);
        setBusy(null);
        return;
      }
      onTranscribed(t);
      onExit();
    } catch {
      toast("Transcription failed", true);
      setBusy(null);
    }
  };

  if (phase === "recording" || !clip) {
    return (
      <Animated.View
        key="recording"
        entering={FadeIn.duration(220)}
        exiting={FadeOut.duration(120)}
        style={styles.row}
      >
        <View style={styles.recWave}>
          <Waveform samples={samples.current} live color={REC} />
        </View>
        <Text style={[styles.time, { color: REC }]}>{fmtClock(rec.durationMillis)}</Text>
        <Pressable
          onPress={() => void stop()}
          accessibilityLabel="Stop recording"
          accessibilityRole="button"
          hitSlop={8}
          style={({ pressed }) => [styles.stopBtn, pressed && styles.stopBtnPressed]}
        >
          <View style={styles.stopSquare} />
        </Pressable>
      </Animated.View>
    );
  }

  const progress = status.duration > 0 ? status.currentTime / status.duration : 0;
  const shown = status.playing || status.currentTime > 0 ? status.currentTime * 1000 : clip.millis;
  return (
    <Animated.View key="review" entering={FadeIn.duration(200)} style={[styles.row, styles.reviewRow]}>
      <RoundButton
        label={status.playing ? "Pause voice message" : "Play voice message"}
        onPress={togglePlay}
        disabled={!!busy}
      >
        <Glyph sf={status.playing ? "pause.fill" : "play.fill"} ion={status.playing ? "pause" : "play"} size={15} />
      </RoundButton>
      <View style={styles.reviewWave}>
        <Waveform samples={samples.current} progress={progress} color={colors.faint} played={colors.ink2} />
      </View>
      <View style={styles.durChip}>
        <Text style={styles.durText}>{fmtClock(shown)}</Text>
      </View>
      <RoundButton label="Transcribe to text" onPress={() => void transcribe()} disabled={!!busy}>
        {busy === "transcribe" ? (
          <ActivityIndicator size="small" color={colors.ink2} />
        ) : (
          <Glyph sf="text.bubble" ion="chatbubble-ellipses-outline" size={16} />
        )}
      </RoundButton>
      <Pressable
        onPress={() => void send()}
        disabled={!!busy}
        accessibilityLabel="Send voice message"
        accessibilityRole="button"
        hitSlop={6}
        style={({ pressed }) => [styles.sendBtn, pressed && { opacity: 0.8 }]}
      >
        {busy === "send" ? (
          <ActivityIndicator size="small" color={colors.surface} />
        ) : (
          <Icon name="arrow-up" size={19} color={colors.surface} />
        )}
      </Pressable>
    </Animated.View>
  );
});

/**
 * Bars sized to the available width. Live: the newest samples enter at the
 * right and empty slots show as a dotted baseline. Review: the whole clip is
 * bucketed to fit, tinted up to the playhead.
 */
function Waveform({
  samples,
  live,
  progress = 0,
  color,
  played,
}: {
  samples: number[];
  live?: boolean;
  progress?: number;
  color: string;
  played?: string;
}) {
  const [w, setW] = useState(0);
  const n = Math.max(0, Math.floor((w + BAR_GAP) / (BAR_W + BAR_GAP)));
  let bars: (number | null)[];
  if (live) {
    const tail = samples.slice(-n);
    bars = [...new Array<null>(n - tail.length).fill(null), ...tail];
  } else {
    const count = Math.min(n, Math.max(12, samples.length));
    bars = [];
    for (let i = 0; i < count; i++) {
      const a = Math.floor((i * samples.length) / count);
      const b = Math.max(a + 1, Math.floor(((i + 1) * samples.length) / count));
      bars.push(Math.max(0.04, ...samples.slice(a, b)));
    }
  }
  return (
    <View style={styles.wave} onLayout={(e) => setW(e.nativeEvent.layout.width)}>
      {bars.map((lv, i) => (
        <View
          key={i}
          style={[
            styles.bar,
            lv == null
              ? styles.dot
              : { height: Math.max(3, Math.round(lv * 28)) },
            { backgroundColor: played && i / bars.length < progress ? played : color },
            lv == null && { opacity: 0.35 },
          ]}
        />
      ))}
    </View>
  );
}

function RoundButton({
  label,
  onPress,
  disabled,
  children,
}: {
  label: string;
  onPress: () => void;
  disabled?: boolean;
  children: React.ReactNode;
}) {
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      accessibilityLabel={label}
      accessibilityRole="button"
      hitSlop={4}
      style={({ pressed }) => [styles.roundBtn, (pressed || disabled) && { opacity: 0.6 }]}
    >
      {children}
    </Pressable>
  );
}

/**
 * SF Symbol on iOS 26 (non-interactive, so the Pressable keeps the touch);
 * Ionicons elsewhere. Ignores safe areas so the keyboard can't offset it.
 */
export function Glyph({
  sf,
  ion,
  size,
  color = colors.ink2,
}: {
  sf: SFSymbol;
  ion: IconName;
  size: number;
  color?: string;
}) {
  if (glassSupported) {
    return (
      <Host matchContents ignoreSafeArea="all" pointerEvents="none">
        <SFImage systemName={sf} size={size} color={color} />
      </Host>
    );
  }
  return <Icon name={ion} size={size + 3} color={color} />;
}

function fmtClock(ms: number): string {
  const s = Math.floor(ms / 1000);
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

const styles = StyleSheet.create({
  row: {
    flex: 1,
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    paddingLeft: 20,
    paddingRight: 13,
  },
  // Concentric with the 64pt capsule: each end control sits as far from the
  // side as from the top/bottom (34pt play → 15, 30pt send → 17).
  reviewRow: {
    gap: 12,
    paddingLeft: 15,
    paddingRight: 17,
  },
  recWave: {
    flex: 1,
  },
  reviewWave: {
    flex: 1,
    paddingHorizontal: 4,
  },
  wave: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "flex-end",
    gap: BAR_GAP,
    height: 30,
    overflow: "hidden",
  },
  bar: {
    width: BAR_W,
    borderRadius: BAR_W / 2,
  },
  dot: {
    height: 3,
  },
  time: {
    ...type.callout,
    fontVariant: ["tabular-nums"],
    minWidth: 36,
    textAlign: "right",
  },
  stopBtn: {
    width: 38,
    height: 38,
    borderRadius: 19,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "rgba(196,72,63,0.12)",
  },
  stopBtnPressed: {
    backgroundColor: "rgba(196,72,63,0.24)",
  },
  stopSquare: {
    width: 13,
    height: 13,
    borderRadius: 3,
    backgroundColor: REC,
  },
  roundBtn: {
    width: 34,
    height: 34,
    borderRadius: 17,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "rgba(27,36,31,0.06)",
  },
  durChip: {
    height: 28,
    paddingHorizontal: 11,
    borderRadius: 14,
    justifyContent: "center",
    backgroundColor: "rgba(27,36,31,0.06)",
  },
  durText: {
    ...type.subhead,
    color: colors.ink2,
    fontVariant: ["tabular-nums"],
  },
  sendBtn: {
    width: 42,
    height: 30,
    borderRadius: 15,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: colors.sage,
  },
});
