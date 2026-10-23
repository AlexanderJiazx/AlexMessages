import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { AppState, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { usePathname, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { FullWindowOverlay } from "react-native-screens";
import { callStatusText, type CallSession } from "@alexmessages/shared";
import { useChatState } from "../session";
import { dismissCallNotification } from "../notify";
import { handOffMedia, setCallLive } from "../media";
import { shadow } from "../theme";
import { Avatar } from "../components/Avatar";
import { TOPBAR_H } from "../components/MessageList";
import { CallVideo } from "./CallVideo";
import { CALL_BG, useSecondTicks } from "./CallScreen";
import type { CallRoute } from "./route";
import { useCallRoute, useSystemSpeaker } from "./system";

interface CallView {
  /** The session the call screen shows; null when it has nothing to show. */
  call: CallSession | null;
  /** Whether the call plays through the loudspeaker. */
  speaker: boolean;
  toggleSpeaker: () => void;
}

const CallViewCtx = createContext<CallView>({ call: null, speaker: false, toggleSpeaker: () => {} });

/** What app/call.tsx renders. */
export function useCallView(): CallView {
  return useContext(CallViewCtx);
}

/**
 * False while the app is in the background. The call screen opens and
 * closes only outside it: on iOS a modal dismissed in the background never
 * finishes its transition, and react-native-screens' snapshot of the
 * closing screen comes out blank, so the app came back to a black screen.
 */
export function useAppVisible(): boolean {
  const [visible, setVisible] = useState(() => AppState.currentState !== "background");
  useEffect(() => {
    const sub = AppState.addEventListener("change", (s) => setVisible(s !== "background"));
    return () => sub.remove();
  }, []);
  return visible;
}

/**
 * The calls layer, wrapped around the root Stack (app/_layout.tsx):
 *  - opens the call screen (/call) when a call starts on this device; the
 *    screen closes itself once useCallView() has nothing to show (both
 *    wait while the app is in the background: useAppVisible),
 *  - stops in-app media (src/media.ts), then plays the ringtone or ringback
 *    and runs the in-call audio session: callAudio, or the system's call
 *    UI and its audio session when it has the call (./system: CallKit or
 *    LiveCommunicationKit on iOS; on Android the ring service, which rings
 *    while the app is off screen),
 *  - takes the incoming-call banner down once the call stops ringing,
 *  - while the call screen is closed mid-call, floats a tile over the app
 *    (modal sheets included) that opens it again.
 */
export function CallHost({ children }: { children: React.ReactNode }) {
  const call = useChatState().call;
  const router = useRouter();
  const pathname = usePathname();
  const onCallScreen = pathname === "/call";
  const onSheet = SHEETS.has(pathname.split("/")[1]);
  const onCallScreenRef = useRef(onCallScreen);
  onCallScreenRef.current = onCallScreen;

  const live = !!call && call.phase !== "ended";
  const key = call?.key ?? null;

  // An ended call stays on screen (saying why) unless this device hung up,
  // or it only rang here.
  const lastLive = useRef<{ key: number; phase: CallSession["phase"] } | null>(null);
  if (call && live) lastLive.current = { key: call.key, phase: call.phase };
  const onlyRang = !!call && lastLive.current?.key === call.key && lastLive.current.phase === "incoming";
  const shown = call && (live || !!call.error || (call.outcome !== "hung_up" && !onlyRang)) ? call : null;

  const visible = useAppVisible();
  const opened = useRef<number | null>(null);
  useEffect(() => {
    if (!live || key == null || opened.current === key || !visible) return;
    opened.current = key;
    if (!onCallScreenRef.current) router.push("/call");
  }, [live, key, visible, router]);

  const ready = useMediaHandoff(key, live);
  const route = useCallRoute(call, ready);
  useCallSounds(call, ready, route);

  const ringingChannel = call?.phase === "incoming" ? call.channel : null;
  useEffect(() => {
    if (!ringingChannel) return;
    return () => void dismissCallNotification(ringingChannel);
  }, [ringingChannel]);

  // Loudspeaker: video calls start on it, voice calls on the earpiece.
  const [speakerPick, setSpeakerPick] = useState<{ key: number | null; on: boolean }>({ key: null, on: false });
  const speaker = speakerPick.key === key && key != null ? speakerPick.on : call?.media === "video";
  const sounds = route.sounds;
  const toggleSpeaker = useCallback(() => {
    const on = !speaker;
    sounds.setSpeaker(on);
    setSpeakerPick({ key, on });
  }, [speaker, key, sounds]);
  // The route changed outside the app (Control Center, a headset).
  useSystemSpeaker((on) => setSpeakerPick({ key, on }));

  const view = useMemo(() => ({ call: shown, speaker, toggleSpeaker }), [shown, speaker, toggleSpeaker]);
  return (
    <CallViewCtx.Provider value={view}>
      {children}
      {call && live && !onCallScreen && (
        <OverSheet sheet={onSheet}>
          <MiniCall call={call} onPress={() => !onCallScreenRef.current && router.push("/call")} />
        </OverSheet>
      )}
    </CallViewCtx.Provider>
  );
}

/** The routes app/_layout.tsx presents as modal sheets. */
const SHEETS = new Set(["settings", "new-chat", "profile"]);

/**
 * Keeps the floating tile above a modal sheet while one is open. iOS
 * presents the sheets above the app's root view, so there the tile goes in
 * a window-level overlay, which passes touches outside the tile through to
 * the app. Only while a sheet is open: the overlay also draws above
 * everything iOS presents after it (context menus, the image viewer, the
 * call screen while it slides away), which belong above the tile. On
 * Android the sheets are views in the root view, and the tile, drawn after
 * the Stack, is already above them.
 */
function OverSheet({ sheet, children }: { sheet: boolean; children: React.ReactNode }) {
  if (Platform.OS !== "ios" || !sheet) return <>{children}</>;
  // Not modal for VoiceOver: the rest of the app stays reachable.
  return <FullWindowOverlay unstable_accessibilityContainerViewIsModal={false}>{children}</FullWindowOverlay>;
}

/**
 * Marks the call live for src/media.ts and stops in-app media when it
 * starts. True once the call may make its own sounds: at once, or
 * MEDIA_HANDOFF_MS later if something was playing or recording.
 */
function useMediaHandoff(key: number | null, live: boolean): boolean {
  const [readyKey, setReadyKey] = useState<number | null>(null);
  useEffect(() => {
    setCallLive(live);
    if (!live || key == null) return;
    const wait = handOffMedia(key) - Date.now();
    if (wait <= 0) {
      setReadyKey(key);
      return;
    }
    const t = setTimeout(() => setReadyKey(key), wait);
    return () => clearTimeout(t);
  }, [key, live]);
  return live && readyKey === key;
}

/** Ringtone while ringing here (when the route rings); the in-call audio
 *  session from dialing or answering until the end; ringback while the
 *  peer's phone rings. A route that changes mid-call (the system refused
 *  the call) hands the audio session over. */
function useCallSounds(call: CallSession | null, ready: boolean, route: CallRoute): void {
  const key = call?.key ?? null;
  const phase = call?.phase;
  const media = call?.media ?? "audio";
  const sounds = route.sounds;
  const ringing = ready && phase === "incoming" && route.ring;
  const inCall = ready && !!phase && phase !== "incoming" && phase !== "ended";
  const ringback = inCall && phase === "outgoing" && !!call?.ringing;

  useEffect(() => {
    if (!ringing) return;
    sounds.startRingtone();
    return () => sounds.stopRingtone();
  }, [ringing, key, sounds]);

  useEffect(() => {
    if (!inCall) return;
    sounds.start(media);
    return () => sounds.stop();
    // The media is fixed for a call's whole life.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inCall, key, sounds]);

  useEffect(() => {
    if (!ringback) return;
    sounds.startRingback();
    return () => sounds.stopRingback();
  }, [ringback, key, sounds]);
}

/** The floating "back to the call" tile: the peer's video, or their avatar. */
function MiniCall({ call, onPress }: { call: CallSession; onPress: () => void }) {
  const insets = useSafeAreaInsets();
  const now = useSecondTicks(call.phase === "active");
  const [frames, setFrames] = useState(false);
  const video = call.peerCameraOn && frames;
  const name = call.peer.display_name || call.peer.username;

  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={`Return to call with ${name}`}
      style={[styles.mini, { top: insets.top + TOPBAR_H + 8 }]}
    >
      <View style={styles.miniClip}>
        {call.remoteStream && (
          <CallVideo stream={call.remoteStream} zOrder={1} style={StyleSheet.absoluteFill} onFrames={setFrames} />
        )}
        {/* A view over the video hides it (see CallScreen). */}
        {!video && (
          <View style={[StyleSheet.absoluteFill, styles.miniCover]}>
            <Avatar user={call.peer} size={46} />
          </View>
        )}
        <View style={styles.miniFooter} pointerEvents="none">
          <Text style={styles.miniStatus} numberOfLines={1}>
            {callStatusText(call, now)}
          </Text>
        </View>
      </View>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  mini: {
    position: "absolute",
    right: 12,
    width: 96,
    height: 132,
    borderRadius: 18,
    backgroundColor: CALL_BG,
    ...shadow.float,
  },
  miniClip: {
    flex: 1,
    borderRadius: 18,
    overflow: "hidden",
  },
  miniCover: {
    alignItems: "center",
    justifyContent: "center",
    paddingBottom: 18,
    backgroundColor: CALL_BG,
  },
  miniFooter: {
    position: "absolute",
    left: 0,
    right: 0,
    bottom: 0,
    paddingVertical: 6,
    alignItems: "center",
    backgroundColor: "rgba(0,0,0,0.35)",
  },
  miniStatus: {
    fontSize: 12,
    fontWeight: "600",
    color: "#FFFFFF",
    fontVariant: ["tabular-nums"],
  },
});
