import React, { useCallback, useState } from "react";
import {
  ActivityIndicator,
  Image,
  Linking,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useAudioPlayer } from "expo-audio";
import { useVideoPlayer, VideoView } from "expo-video";
import {
  attachmentKind,
  fmtSize,
  type Attachment,
} from "@alexmessages/shared";
import { colors, radius } from "../theme";
import { Icon } from "./Icon";
import { useSession } from "../session";

const MAX_W = 260;

/**
 * Attachment rendering — image with reserved box + fullscreen viewer,
 * inline video player, audio player pill, or a file card. Mirrors the web
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
      return <AudioAttachment a={a} mine={mine} />;
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

/** Audio attachment — compact play/pause pill with progress. */
function AudioAttachment({ a, mine }: { a: Attachment; mine: boolean }) {
  const uri = useAttachmentUrl(a.url);
  const player = useAudioPlayer(uri);
  const [playing, setPlaying] = useState(false);
  const [pos, setPos] = useState(0);

  const toggle = useCallback(() => {
    if (playing) {
      player.pause();
      setPlaying(false);
    } else {
      if (player.duration > 0 && pos >= player.duration - 0.1) player.seekTo(0);
      player.play();
      setPlaying(true);
    }
  }, [playing, player, pos]);

  // Poll playback position while playing.
  React.useEffect(() => {
    if (!playing) return;
    const t = setInterval(() => {
      setPos(player.currentTime);
      if (player.duration > 0 && player.currentTime >= player.duration) setPlaying(false);
    }, 250);
    return () => clearInterval(t);
  }, [playing, player]);

  const dur = player.duration || 0;
  const pct = dur > 0 ? Math.min(1, pos / dur) : 0;

  return (
    <View style={[styles.audioWrap, mine && styles.audioWrapMine]}>
      <Pressable
        style={[styles.audioBtn, mine && styles.audioBtnMine]}
        onPress={toggle}
        accessibilityLabel={playing ? "Pause audio" : "Play audio"}
      >
        <Icon
          name={playing ? "pause" : "play"}
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
