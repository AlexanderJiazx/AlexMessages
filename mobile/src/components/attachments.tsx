import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Image,
  Linking,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useAudioPlayer, useAudioPlayerStatus, type AudioPlayer } from "expo-audio";
import { useVideoPlayer, VideoView } from "expo-video";
import {
  attachmentKind,
  fmtSize,
  hash,
  isVoiceMessage,
  wavPeaks,
  type Attachment,
} from "@alexmessages/shared";
import { colors, radius } from "../theme";
import { Icon } from "./Icon";
import { useSession } from "../session";
import { enablePlayback } from "../audio";

const MAX_W = 260;

/**
 * Attachment rendering — image with reserved box + fullscreen viewer,
 * inline video player, voice-message waveform player, named audio-file
 * card, or a file card. Mirrors the web
 * client's attachmentKind routing.
 */
export function AttachmentView({
  a,
  mine,
  onImagePress,
}: {
  a: Attachment;
  mine: boolean;
  onImagePress: (url: string) => void;
}) {
  const kind = attachmentKind(a);
  switch (kind) {
    case "image":
      return <ImageAttachment a={a} onPress={() => onImagePress(a.url)} />;
    case "video":
      return <VideoAttachment a={a} />;
    case "audio":
      return isVoiceMessage(a) ? (
        <VoiceAttachment a={a} mine={mine} />
      ) : (
        <AudioFileAttachment a={a} mine={mine} />
      );
    default:
      return <FileAttachment a={a} mine={mine} />;
  }
}

function useAttachmentUrl(path: string): string {
  const { api } = useSession();
  return api.url(path);
}

/** Image with a reserved box while loading (dimensions from the server). */
function ImageAttachment({ a, onPress }: { a: Attachment; onPress: () => void }) {
  const uri = useAttachmentUrl(a.url);
  const [state, setState] = useState<"loading" | "ok" | "err">("loading");
  const w = a.width | 0;
  const h = a.height | 0;
  const known = w > 0 && h > 0;
  const scale = known ? Math.min(1, MAX_W / w, 300 / h) : 1;
  const dw = known ? Math.max(1, Math.round(w * scale)) : MAX_W;
  const dh = known ? Math.max(1, Math.round(h * scale)) : 200;
  return (
    <Pressable
      onPress={onPress}
      style={[styles.imgWrap, { width: dw, height: dh }]}
      accessibilityLabel={`Image ${a.name}`}
    >
      {state === "loading" && (
        <View style={styles.imgLoading}>
          <ActivityIndicator color={colors.sage} />
        </View>
      )}
      {state === "err" ? (
        <View style={styles.imgLoading}>
          <Icon name="image-outline" size={22} color={colors.faint} />
          <Text style={styles.imgErrText}>Couldn't load image</Text>
        </View>
      ) : (
        <Image
          source={{ uri }}
          style={{ width: dw, height: dh }}
          resizeMode="cover"
          onLoad={() => setState("ok")}
          onError={() => setState("err")}
        />
      )}
    </Pressable>
  );
}

/** Inline video player via expo-video. */
function VideoAttachment({ a }: { a: Attachment }) {
  const uri = useAttachmentUrl(a.url);
  const player = useVideoPlayer(uri, (p) => {
    p.loop = false;
  });
  const w = a.width | 0;
  const h = a.height | 0;
  const scale = w > 0 && h > 0 ? Math.min(1, MAX_W / w, 280 / h) : 1;
  const dw = w > 0 && h > 0 ? Math.round(w * scale) : MAX_W;
  const dh = w > 0 && h > 0 ? Math.round(h * scale) : 180;
  return (
    <View>
      <VideoView
        player={player}
        style={{ width: dw, height: dh, borderRadius: radius.s }}
        contentFit="contain"
        nativeControls
        accessibilityLabel={`Video ${a.name}`}
      />
      <Text style={styles.mediaName} numberOfLines={1}>
        {a.name}
      </Text>
    </View>
  );
}

/** The voice message / audio file currently playing; starting another pauses it. */
let currentPlayer: AudioPlayer | null = null;

/** Shared play/pause for the in-bubble players. */
function usePlayback(uri: string) {
  const player = useAudioPlayer(uri);
  const status = useAudioPlayerStatus(player);

  useEffect(() => {
    if (status.didJustFinish) {
      player.pause();
      void player.seekTo(0);
    }
  }, [status.didJustFinish, player]);

  useEffect(
    () => () => {
      if (currentPlayer === player) currentPlayer = null;
    },
    [player],
  );

  const toggle = useCallback(async () => {
    if (status.playing) {
      player.pause();
      return;
    }
    // Recording leaves the session in a mode the silent switch mutes.
    await enablePlayback();
    if (currentPlayer && currentPlayer !== player) currentPlayer.pause();
    currentPlayer = player;
    if (status.duration > 0 && status.currentTime >= status.duration - 0.1) await player.seekTo(0);
    player.play();
  }, [player, status.playing, status.duration, status.currentTime]);

  return { player, status, toggle };
}

const VOICE_BARS = 30;
/** Decoded waveforms per URL — a sent clip never changes. */
const peakCache = new Map<string, Promise<{ peaks: number[]; duration: number } | null>>();

/** Real envelope for WAV clips (web + iOS recordings); null otherwise. */
function loadPeaks(uri: string, name: string) {
  if (!/\.wav$/i.test(name)) return Promise.resolve(null);
  let p = peakCache.get(uri);
  if (!p) {
    p = fetch(uri)
      .then((r) => (r.ok ? r.arrayBuffer() : null))
      .then((buf) => (buf ? wavPeaks(buf, VOICE_BARS) : null))
      .catch(() => null);
    peakCache.set(uri, p);
  }
  return p;
}

/** Stable stand-in bars (Android's AAC clips can't be decoded in JS). */
function placeholderPeaks(seed: string): number[] {
  let h = hash(seed) || 1;
  const out: number[] = [];
  for (let i = 0; i < VOICE_BARS; i++) {
    h = (h * 1103515245 + 12345) & 0x7fffffff;
    out.push(0.2 + ((h >> 8) % 1000) / 1250);
  }
  return out;
}

function fmtClock(sec: number): string {
  const s = Math.max(0, Math.round(sec));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

/** Recorded voice message — Messages-style play · waveform · time. */
function VoiceAttachment({ a, mine }: { a: Attachment; mine: boolean }) {
  const uri = useAttachmentUrl(a.url);
  const { player, status, toggle } = usePlayback(uri);
  const [peaks, setPeaks] = useState(() => placeholderPeaks(a.url));
  const [decodedDur, setDecodedDur] = useState(0);
  const [waveW, setWaveW] = useState(0);

  useEffect(() => {
    let live = true;
    void loadPeaks(uri, a.name).then((d) => {
      if (!live || !d) return;
      setPeaks(d.peaks);
      setDecodedDur(d.duration);
    });
    return () => {
      live = false;
    };
  }, [uri, a.name]);

  const dur = status.duration > 0 ? status.duration : decodedDur;
  const progress = dur > 0 ? Math.min(1, status.currentTime / dur) : 0;
  const started = status.playing || status.currentTime > 0;
  const fg = mine ? colors.surface : colors.sageDeep;
  const dim = mine ? "rgba(255,255,255,0.45)" : "rgba(53,80,64,0.28)";

  return (
    <View style={styles.voice}>
      <Pressable
        style={({ pressed }) => [styles.voiceBtn, mine && styles.voiceBtnMine, pressed && { opacity: 0.7 }]}
        onPress={() => void toggle()}
        hitSlop={6}
        accessibilityRole="button"
        accessibilityLabel={status.playing ? "Pause voice message" : "Play voice message"}
      >
        <Icon name={status.playing ? "pause" : "play"} size={16} color={fg} />
      </Pressable>
      <Pressable
        style={styles.voiceWave}
        onLayout={(e) => setWaveW(e.nativeEvent.layout.width)}
        onPress={(e) => {
          if (!dur || !waveW) return;
          void player.seekTo(Math.min(1, Math.max(0, e.nativeEvent.locationX / waveW)) * dur);
        }}
        accessibilityLabel="Voice message position"
      >
        {peaks.map((v, i) => (
          <View
            key={i}
            style={[
              styles.voiceBar,
              { height: Math.max(3, Math.round(v * 24)) },
              { backgroundColor: (i + 0.5) / peaks.length <= progress ? fg : dim },
            ]}
          />
        ))}
      </Pressable>
      <Text style={[styles.voiceTime, mine && styles.voiceTimeMine]}>
        {fmtClock(started ? dur - status.currentTime : dur)}
      </Text>
    </View>
  );
}

/** Uploaded audio file — named card with play/pause and a progress track. */
function AudioFileAttachment({ a, mine }: { a: Attachment; mine: boolean }) {
  const uri = useAttachmentUrl(a.url);
  const { status, toggle } = usePlayback(uri);
  const pct = status.duration > 0 ? Math.min(1, status.currentTime / status.duration) : 0;

  return (
    <View style={[styles.audioWrap, mine && styles.audioWrapMine]}>
      <Pressable
        style={[styles.audioBtn, mine && styles.audioBtnMine]}
        onPress={() => void toggle()}
        accessibilityLabel={status.playing ? "Pause audio" : "Play audio"}
      >
        <Icon
          name={status.playing ? "pause" : "play"}
          size={15}
          color={mine ? colors.surface : colors.sageDeep}
        />
      </Pressable>
      <View style={styles.audioBody}>
        <View style={[styles.audioTrack, mine && styles.audioTrackMine]}>
          <View
            style={[
              styles.audioFill,
              mine && styles.audioFillMine,
              { width: `${Math.max(2, pct * 100)}%` },
            ]}
          />
        </View>
        <Text
          style={[styles.audioName, mine && styles.audioNameMine]}
          numberOfLines={1}
        >
          {a.name} · {fmtSize(a.size)}
        </Text>
      </View>
    </View>
  );
}

/** Generic file — opens via the system browser/handler. */
function FileAttachment({ a, mine }: { a: Attachment; mine: boolean }) {
  const uri = useAttachmentUrl(a.url);
  return (
    <Pressable
      style={[styles.file, mine && styles.fileMine]}
      onPress={() => void Linking.openURL(uri).catch(() => {})}
      accessibilityLabel={`Open file ${a.name}`}
    >
      <View style={[styles.fileIco, mine && styles.fileIcoMine]}>
        <Icon name="document-outline" size={16} color={mine ? colors.surface : colors.sageDeep} />
      </View>
      <View style={styles.fileMeta}>
        <Text style={[styles.fileName, mine && styles.fileTextMine]} numberOfLines={1}>
          {a.name}
        </Text>
        <Text style={[styles.fileSize, mine && styles.fileTextMine]}>{fmtSize(a.size)}</Text>
      </View>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  imgWrap: {
    borderRadius: radius.s,
    overflow: "hidden",
    backgroundColor: colors.line2,
  },
  imgLoading: {
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
    bottom: 0,
    alignItems: "center",
    justifyContent: "center",
    gap: 6,
  },
  imgErrText: {
    fontSize: 12,
    color: colors.faint,
  },
  mediaName: {
    marginTop: 4,
    fontSize: 11,
    color: colors.muted,
    maxWidth: MAX_W,
  },
  voice: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    width: 220,
    paddingVertical: 2,
  },
  voiceBtn: {
    width: 32,
    height: 32,
    borderRadius: 16,
    backgroundColor: colors.sageTint,
    alignItems: "center",
    justifyContent: "center",
  },
  voiceBtnMine: {
    backgroundColor: "rgba(255,255,255,0.22)",
  },
  voiceWave: {
    flex: 1,
    height: 28,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
  },
  voiceBar: {
    width: 3,
    borderRadius: 1.5,
  },
  voiceTime: {
    fontSize: 13,
    color: colors.muted,
    fontVariant: ["tabular-nums"],
    minWidth: 32,
    textAlign: "right",
  },
  voiceTimeMine: {
    color: "rgba(255,255,255,0.85)",
  },
  audioWrap: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    width: MAX_W,
    paddingVertical: 4,
  },
  audioWrapMine: {},
  audioBtn: {
    width: 34,
    height: 34,
    borderRadius: 17,
    backgroundColor: colors.sageTint,
    alignItems: "center",
    justifyContent: "center",
  },
  audioBtnMine: {
    backgroundColor: "rgba(255,255,255,0.22)",
  },
  audioBody: {
    flex: 1,
    gap: 4,
  },
  audioTrack: {
    height: 4,
    borderRadius: 2,
    backgroundColor: colors.line,
    overflow: "hidden",
  },
  audioTrackMine: {
    backgroundColor: "rgba(255,255,255,0.28)",
  },
  audioFill: {
    height: 4,
    borderRadius: 2,
    backgroundColor: colors.sage,
  },
  audioFillMine: {
    backgroundColor: colors.surface,
  },
  audioName: {
    fontSize: 11,
    color: colors.muted,
  },
  audioNameMine: {
    color: "rgba(255,255,255,0.85)",
  },
  file: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    backgroundColor: colors.sageTint,
    borderRadius: radius.s,
    padding: 10,
    width: MAX_W,
  },
  fileMine: {
    backgroundColor: "rgba(255,255,255,0.16)",
  },
  fileIco: {
    width: 32,
    height: 32,
    borderRadius: 8,
    backgroundColor: colors.surface,
    alignItems: "center",
    justifyContent: "center",
  },
  fileIcoMine: {
    backgroundColor: "rgba(255,255,255,0.22)",
  },
  fileMeta: {
    flex: 1,
  },
  fileName: {
    fontSize: 13,
    fontWeight: "600",
    color: colors.ink,
  },
  fileSize: {
    fontSize: 11,
    color: colors.muted,
    marginTop: 1,
  },
  fileTextMine: {
    color: colors.surface,
  },
});
