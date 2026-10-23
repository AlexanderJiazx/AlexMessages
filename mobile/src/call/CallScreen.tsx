import React, { useEffect, useState } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { callStatusText, type CallSession } from "@alexmessages/shared";
import { useSession } from "../session";
import { fontDisplay } from "../theme";
import { Avatar } from "../components/Avatar";
import { Icon, type IconName } from "../components/Icon";
import { Toasts } from "../components/Toasts";
import { CallVideo } from "./CallVideo";

/** The call screen's dark sage backdrop (the web call view's colors). */
export const CALL_BG = "#18211C";
const CALL_BACKDROP = "linear-gradient(to bottom, #2E4136 0%, #18211C 58%, #0F1512 100%)";
export const CALL_GREEN = "#34A853";
const CALL_RED = "#E5484D";
const ON_DARK = "rgba(255,255,255,0.72)";

/**
 * The call screen (app/call.tsx) for the shared CallManager's session.
 * Ringing here: the caller, Decline, and Accept. Otherwise: the peer's video
 * full screen (their avatar while their camera is off), your camera in the
 * top corner with a flip button, and speaker · camera · mute · end. The
 * chevron closes the screen and keeps the call; CallHost's floating tile
 * opens it again. Same layout on iOS and Android: on the dark backdrop the
 * buttons are translucent white discs, not the app's light glass.
 */
export function CallScreen({
  call,
  speaker,
  onToggleSpeaker,
  onMinimize,
}: {
  call: CallSession;
  speaker: boolean;
  onToggleSpeaker: () => void;
  onMinimize: () => void;
}) {
  const { store } = useSession();
  const insets = useSafeAreaInsets();
  const now = useSecondTicks(call.phase === "active");
  // The peer's track exists from the offer on; frames come once media flows.
  const [frames, setFrames] = useState(false);
  useEffect(() => setFrames(false), [call.key]);

  const name = call.peer.display_name || call.peer.username;
  const incoming = call.phase === "incoming";
  const ended = call.phase === "ended";
  const peerVideo = call.peerCameraOn && frames && !ended;
  const selfView = call.cameraOn && !!call.localStream && !ended;
  const status = callStatusText(call, now);
  const peerMuted = !call.peerMicOn && !incoming && !ended;

  return (
    <View style={styles.root} accessibilityViewIsModal>
      {call.remoteStream && !ended && (
        <CallVideo stream={call.remoteStream} style={StyleSheet.absoluteFill} onFrames={setFrames} />
      )}
      {/* Covers the video while there is none to see. Opacity can't hide an
          RTCView on Android (it renders into a SurfaceView), but a view
          drawn over it does. */}
      {!peerVideo && <View style={[StyleSheet.absoluteFill, styles.backdrop]} />}
      {peerVideo && (
        <>
          <View pointerEvents="none" style={[styles.shadeTop, { height: insets.top + 110 }]} />
          <View pointerEvents="none" style={[styles.shadeBottom, { height: insets.bottom + 170 }]} />
        </>
      )}

      {/* Padded from the hook, never a SafeAreaView: on the screen's first
          frames a SafeAreaView has no insets yet (it gets them natively a
          moment later, and in a full-screen modal on iOS not at all), so the
          chevron sat under the status bar while the modal opened. */}
      <View
        style={[styles.layer, { paddingTop: insets.top, paddingBottom: insets.bottom }]}
        pointerEvents="box-none"
      >
        <View style={[styles.topRow, selfView && styles.topRowBesideSelf]} pointerEvents="box-none">
          {!incoming && (
            <RoundButton icon="chevron-down" label="Minimize call" size={44} iconSize={24} onPress={onMinimize} />
          )}
          {peerVideo && (
            <View style={styles.topText}>
              <Text style={styles.topName} numberOfLines={1}>
                {name}
              </Text>
              <Text style={styles.topStatus}>{status}</Text>
            </View>
          )}
        </View>

        <View style={styles.middle} pointerEvents="box-none">
          {!peerVideo && (
            <View style={styles.peer}>
              <Avatar user={call.peer} size={112} />
              <Text style={styles.name} numberOfLines={1}>
                {name}
              </Text>
              <Text style={styles.status}>{status}</Text>
              {ended && !!call.error && <Text style={styles.error}>{call.error}</Text>}
            </View>
          )}
          {peerMuted && (
            <View style={styles.muted}>
              <Icon name="mic-off" size={13} color="#fff" />
              <Text style={styles.mutedText}>{name} is muted</Text>
            </View>
          )}
        </View>

        {incoming ? (
          <View style={styles.answerRow}>
            <RoundButton
              icon="call"
              kind="end"
              size={72}
              iconSize={30}
              label="Decline"
              caption="Decline"
              onPress={() => store.calls.hangUp()}
            />
            <RoundButton
              icon={call.media === "video" ? "videocam" : "call"}
              kind="accept"
              size={72}
              iconSize={30}
              label="Accept"
              caption="Accept"
              onPress={() => void store.calls.accept()}
            />
          </View>
        ) : (
          !ended && (
            <View style={styles.controls}>
              <RoundButton
                icon={speaker ? "volume-high" : "volume-medium"}
                on={speaker}
                label={speaker ? "Turn speaker off" : "Turn speaker on"}
                onPress={onToggleSpeaker}
              />
              <RoundButton
                icon={call.cameraOn ? "videocam" : "videocam-off"}
                on={!call.cameraOn}
                label={call.cameraOn ? "Turn camera off" : "Turn camera on"}
                onPress={() => void store.calls.toggleCamera()}
              />
              <RoundButton
                icon={call.micOn ? "mic" : "mic-off"}
                on={!call.micOn}
                label={call.micOn ? "Mute" : "Unmute"}
                onPress={() => store.calls.toggleMic()}
              />
              <RoundButton icon="call" kind="end" label="End call" onPress={() => store.calls.hangUp()} />
            </View>
          )
        )}
      </View>

      {selfView && (
        <View style={[styles.self, { top: insets.top + 8 }]}>
          <CallVideo
            stream={call.localStream}
            mirror={call.facing === "user"}
            zOrder={1}
            style={StyleSheet.absoluteFill}
          />
          <Pressable
            style={styles.flip}
            onPress={() => void store.calls.switchCamera()}
            accessibilityRole="button"
            accessibilityLabel="Switch camera"
            hitSlop={8}
          >
            <Icon name="camera-reverse" size={17} color="#fff" />
          </Pressable>
        </View>
      )}
      <Toasts bottom={insets.bottom + 150} />
    </View>
  );
}

/**
 * A round call control. kind "end" (red, the handset turned down) and
 * "accept" (green) are filled; plain controls are translucent, and white
 * while `on` (muted, camera off, speaker on — as in the iOS call screen).
 */
export function RoundButton({
  icon,
  label,
  onPress,
  kind = "plain",
  on = false,
  size = 64,
  iconSize = 26,
  caption,
}: {
  icon: IconName;
  label: string;
  onPress: () => void;
  kind?: "plain" | "end" | "accept";
  on?: boolean;
  size?: number;
  iconSize?: number;
  /** Text under the disc (inside the same button). */
  caption?: string;
}) {
  const bg =
    kind === "end" ? CALL_RED : kind === "accept" ? CALL_GREEN : on ? "#FFFFFF" : "rgba(255,255,255,0.16)";
  const fg = kind === "plain" && on ? "#1B241F" : "#FFFFFF";
  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityState={kind === "plain" ? { selected: on } : undefined}
      hitSlop={6}
      style={({ pressed }) => [styles.btnWrap, pressed && { opacity: 0.7 }]}
    >
      <View style={[styles.btn, { width: size, height: size, borderRadius: size / 2, backgroundColor: bg }]}>
        <View style={kind === "end" ? styles.hangUp : undefined}>
          <Icon name={icon} size={iconSize} color={fg} />
        </View>
      </View>
      {caption && <Text style={styles.caption}>{caption}</Text>}
    </Pressable>
  );
}

/** Date.now(), refreshed every second while `on` (the call timer). */
export function useSecondTicks(on: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!on) return;
    setNow(Date.now());
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [on]);
  return now;
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: CALL_BG,
  },
  backdrop: {
    experimental_backgroundImage: CALL_BACKDROP,
  },
  shadeTop: {
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
    experimental_backgroundImage: "linear-gradient(to bottom, rgba(0,0,0,0.5), rgba(0,0,0,0))",
  },
  shadeBottom: {
    position: "absolute",
    bottom: 0,
    left: 0,
    right: 0,
    experimental_backgroundImage: "linear-gradient(to top, rgba(0,0,0,0.5), rgba(0,0,0,0))",
  },
  layer: {
    flex: 1,
  },
  topRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
    minHeight: 60,
    paddingHorizontal: 12,
    paddingTop: 8,
  },
  // Keeps the name clear of the self view in the top-right corner.
  topRowBesideSelf: {
    paddingRight: 128,
  },
  topText: {
    flexShrink: 1,
  },
  topName: {
    fontSize: 17,
    fontWeight: "600",
    color: "#FFFFFF",
  },
  topStatus: {
    fontSize: 13,
    color: "rgba(255,255,255,0.85)",
    fontVariant: ["tabular-nums"],
  },
  middle: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: 24,
    paddingBottom: 40,
    gap: 18,
  },
  peer: {
    alignItems: "center",
    gap: 6,
    maxWidth: "100%",
  },
  name: {
    marginTop: 14,
    fontFamily: fontDisplay,
    fontSize: 34,
    color: "#FFFFFF",
    maxWidth: "100%",
  },
  status: {
    fontSize: 15,
    color: ON_DARK,
    fontVariant: ["tabular-nums"],
  },
  error: {
    marginTop: 4,
    fontSize: 14,
    color: "#FFB4AE",
    textAlign: "center",
    maxWidth: 320,
  },
  muted: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    paddingHorizontal: 11,
    paddingVertical: 5,
    borderRadius: 999,
    backgroundColor: "rgba(0,0,0,0.45)",
  },
  mutedText: {
    fontSize: 13,
    color: "#FFFFFF",
  },
  controls: {
    flexDirection: "row",
    justifyContent: "center",
    gap: 18,
    paddingHorizontal: 16,
    paddingTop: 12,
    paddingBottom: 18,
  },
  answerRow: {
    flexDirection: "row",
    justifyContent: "space-around",
    paddingHorizontal: 32,
    paddingBottom: 28,
  },
  btnWrap: {
    alignItems: "center",
    gap: 8,
  },
  btn: {
    alignItems: "center",
    justifyContent: "center",
  },
  hangUp: {
    transform: [{ rotate: "135deg" }],
  },
  caption: {
    fontSize: 14,
    color: "#FFFFFF",
  },
  self: {
    position: "absolute",
    right: 12,
    width: 108,
    height: 152,
    borderRadius: 16,
    overflow: "hidden",
    backgroundColor: "#000000",
  },
  flip: {
    position: "absolute",
    right: 6,
    bottom: 6,
    width: 32,
    height: 32,
    borderRadius: 16,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "rgba(0,0,0,0.45)",
  },
});
