import React, { useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  View,
  type ViewStyle,
} from "react-native";
import { GlassView } from "expo-glass-effect";
import * as Haptics from "expo-haptics";
import { toast, useSession } from "../session";
import { useVoiceRecorder } from "../audio";
import { colors, glassSupported, radius } from "../theme";
import { Icon } from "./Icon";

const WAVE_BARS = 28;

type Phase = "recording" | "busy";

/**
 * ChatGPT-style dictation bar: cancel · live waveform + timer · stop
 * (transcribe into the composer) · send-audio (voice message) · send
 * (transcribe and send immediately). Liquid glass on iOS 26+, flat on
 * Android — same layout, same sage send button as the web.
 */
export function DictationBar({
  onExit,
  onTranscribed,
}: {
  onExit: () => void;
  onTranscribed: (text: string) => void;
}) {
  const { api, store } = useSession();
  const rec = useVoiceRecorder();
  const [phase, setPhase] = useState<Phase>("recording");
  const [busyLabel, setBusyLabel] = useState("");
  const levelsRef = useRef<number[]>(new Array(WAVE_BARS).fill(0.08));
  const [, bump] = useState(0);

  useEffect(() => {
    void rec.start().then((ok) => {
      if (!ok) {
        toast("Microphone unavailable", true);
        onExit();
      }
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Shift the newest metering sample into the scrolling waveform.
  useEffect(() => {
    if (!rec.isRecording) return;
    levelsRef.current = [...levelsRef.current.slice(1), Math.max(0.06, rec.level)];
    bump((x) => x + 1);
  }, [rec.level, rec.isRecording]);

  const busy = (label: string) => {
    setPhase("busy");
    setBusyLabel(label);
  };

  const finishStop = async (): Promise<{ uri: string; mime: string; ext: string } | null> => {
    const clip = await rec.stop();
    if (!clip) {
      toast("Recording too short", true);
      setPhase("recording");
      return null;
    }
    return clip;
  };

  const doCancel = async () => {
    await rec.cancel();
    onExit();
  };

  const doTranscribe = async () => {
    busy("Transcribing…");
    const clip = await finishStop();
    if (!clip) return;
    try {
      const res = await api.transcribe({
        uri: clip.uri,
        name: `dictation.${clip.ext}`,
        type: clip.mime,
      });
      const t = (res.text || "").trim();
      if (t) onTranscribed(t);
      else toast("Nothing heard — try again", true);
    } catch {
      toast("Transcription failed", true);
    }
    onExit();
  };

  const doTranscribeAndSend = async () => {
    busy("Transcribing…");
    const clip = await finishStop();
    if (!clip) return;
    try {
      const res = await api.transcribe({
        uri: clip.uri,
        name: `dictation.${clip.ext}`,
        type: clip.mime,
      });
      const t = (res.text || "").trim();
      if (t) store.sendMessage(t);
      else toast("Nothing heard — try again", true);
    } catch {
      toast("Transcription failed", true);
    }
    onExit();
  };

  const doSendAudio = async () => {
    busy("Sending…");
    const clip = await finishStop();
    if (!clip) return;
    const name = `voice-message.${clip.ext}`;
    const ok = await store.uploadAttachment(
      { uri: clip.uri, name, type: clip.mime },
      name
    );
    if (ok) store.sendMessage("");
    onExit();
  };

  const secs = Math.floor(rec.durationMillis / 1000);
  const timer = `${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, "0")}`;

  const controls = (
    <>
      <BarButton
        icon="close"
        label="Cancel recording"
        onPress={() => void doCancel()}
        disabled={phase === "busy"}
      />
      <View style={styles.waveWrap}>
        {phase === "busy" ? (
          <View style={styles.busyRow}>
            <ActivityIndicator size="small" color={colors.sageDeep} />
            <Text style={styles.timer}>{busyLabel}</Text>
          </View>
        ) : (
          <>
            <View style={styles.wave}>
              {levelsRef.current.map((lv, i) => (
                <View
                  key={i}
                  style={[
                    styles.waveBar,
                    { height: Math.max(3, Math.round(lv * 26)) },
                  ]}
                />
              ))}
            </View>
            <Text style={styles.timer}>{timer}</Text>
          </>
        )}
      </View>
      <BarButton
        icon="stop"
        label="Stop and transcribe"
        onPress={() => void doTranscribe()}
        disabled={phase === "busy"}
      />
      <BarButton
        icon="musical-notes"
        label="Send as voice message"
        onPress={() => {
          void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
          void doSendAudio();
        }}
        disabled={phase === "busy"}
      />
      <BarButton
        icon="arrow-up"
        label="Transcribe and send"
        primary
        onPress={() => void doTranscribeAndSend()}
        disabled={phase === "busy"}
      />
    </>
  );

  if (glassSupported) {
    return (
      <GlassView style={styles.bar} glassEffectStyle="regular" isInteractive>
        {controls}
      </GlassView>
    );
  }
  return <View style={[styles.bar, styles.barFlat]}>{controls}</View>;
}

function BarButton({
  icon,
  label,
  onPress,
  primary,
  disabled,
}: {
  icon: React.ComponentProps<typeof Icon>["name"];
  label: string;
  onPress: () => void;
  primary?: boolean;
  disabled?: boolean;
}) {
  const style: ViewStyle[] = [styles.btn];
  if (primary) style.push(styles.btnPrimary);
  if (disabled) style.push({ opacity: 0.4 });
  return (
    <Pressable
      style={style}
      onPress={onPress}
      disabled={disabled}
      accessibilityLabel={label}
      accessibilityRole="button"
      hitSlop={4}
    >
      <Icon
        name={icon}
        size={primary ? 19 : 17}
        color={primary ? colors.surface : colors.ink2}
      />
    </Pressable>
  );
}

const styles = StyleSheet.create({
  bar: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    borderRadius: radius.pill,
    paddingHorizontal: 8,
    paddingVertical: 6,
    minHeight: 50,
  },
  barFlat: {
    backgroundColor: colors.surface,
  },
  btn: {
    width: 36,
    height: 36,
    borderRadius: 18,
    alignItems: "center",
    justifyContent: "center",
  },
  btnPrimary: {
    backgroundColor: colors.sage,
  },
  waveWrap: {
    flex: 1,
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingHorizontal: 4,
  },
  wave: {
    flex: 1,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "flex-end",
    gap: 2.5,
    height: 28,
  },
  waveBar: {
    width: 3,
    borderRadius: 2,
    backgroundColor: colors.sage,
  },
  busyRow: {
    flex: 1,
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  timer: {
    fontSize: 13,
    fontVariant: ["tabular-nums"],
    color: colors.ink2,
    fontWeight: "600",
  },
});
