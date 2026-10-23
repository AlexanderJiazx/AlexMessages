import { useEffect, useRef, useState } from "react";
import type { CallSession, CallStream } from "@shared/call";
import { callStatusText, colorFor, initialsFor, shade } from "@shared/format";
import type { PublicUser } from "@shared/types";
import { store } from "../client";
import { hasSeveralCameras, mirrorsSelfView } from "../call-platform";
import { useChatState } from "../hooks";
import { Icon, type IconName } from "./icons";

/**
 * The call screen for store.state.call (the shared CallManager's session):
 * a ringing card for an incoming call, and for everything else a call view —
 * the peer's video (or their avatar while their camera is off), the self
 * view, and the controls — that can shrink to a floating window so the chat
 * stays usable. It also plays the ringtone and the caller's ringback.
 */
export function CallView() {
  const call = useChatState().call;
  const [minimized, setMinimized] = useState(false);
  // The phase this session had before it ended (refs survive the re-render).
  const lastLive = useRef<{ key: number; phase: CallSession["phase"] } | null>(null);
  if (call && call.phase !== "ended") lastLive.current = { key: call.key, phase: call.phase };
  useCallSounds(call);
  useLeaveWarning(!!call && call.phase !== "ended");
  const key = call?.key;
  useEffect(() => setMinimized(false), [key]);

  if (!call) return null;
  if (call.phase === "ended" && !call.error) {
    // A call this device hung up, or one that only rang here, just goes away.
    const onlyRang = lastLive.current?.key === call.key && lastLive.current.phase === "incoming";
    if (call.outcome === "hung_up" || onlyRang) return null;
  }
  if (call.phase === "incoming") return <IncomingCall call={call} />;
  return <CallWindow call={call} minimized={minimized} onMinimize={setMinimized} />;
}

function IncomingCall({ call }: { call: CallSession }) {
  const name = call.peer.display_name || call.peer.username;
  return (
    <div className="call-incoming" role="dialog" aria-label={`Incoming call from ${name}`}>
      <PeerAvatar peer={call.peer} size={52} />
      <div className="call-incoming-text">
        <b>{name}</b>
        <span>{callStatusText(call)}</span>
      </div>
      <div className="call-incoming-actions">
        <CallButton label="Decline" icon="phone" kind="end" onClick={() => store.calls.hangUp()} />
        <CallButton
          label="Accept"
          icon={call.media === "video" ? "video" : "phone"}
          kind="accept"
          onClick={() => void store.calls.accept()}
        />
      </div>
    </div>
  );
}

function CallWindow({
  call,
  minimized,
  onMinimize,
}: {
  call: CallSession;
  minimized: boolean;
  onMinimize: (minimized: boolean) => void;
}) {
  const now = useSecondTicks(call.phase === "active");
  const [severalCameras, setSeveralCameras] = useState(false);
  useEffect(() => {
    if (call.cameraOn) void hasSeveralCameras().then(setSeveralCameras);
  }, [call.cameraOn]);
  // The peer's track exists from the offer on; frames come once media flows.
  const [remoteFrames, setRemoteFrames] = useState(false);

  const name = call.peer.display_name || call.peer.username;
  const ended = call.phase === "ended";
  const peerVideo = call.peerCameraOn && remoteFrames && !ended;
  const status = callStatusText(call, now);

  return (
    <div
      className={`call-view phase-${call.phase}${minimized ? " mini" : ""}${peerVideo ? " has-video" : ""}`}
      role="dialog"
      aria-label={`Call with ${name}`}
    >
      {/* The peer's audio plays through this element even while it's hidden. */}
      <StreamVideo
        className={`call-remote${peerVideo ? "" : " hidden"}`}
        stream={call.remoteStream}
        onFrames={setRemoteFrames}
      />
      {peerVideo ? (
        <div className="call-top">
          <b>{name}</b>
          <span className="call-status">{status}</span>
        </div>
      ) : (
        <div className="call-peer">
          <PeerAvatar peer={call.peer} size={minimized ? 56 : 112} />
          <b className="call-peer-name">{name}</b>
          <span className="call-status">{status}</span>
          {ended && call.error && !minimized && <span className="call-error">{call.error}</span>}
        </div>
      )}
      {!call.peerMicOn && !ended && (
        <span className="call-peer-muted">
          <Icon name="micOff" size={13} sw={2} />
          {minimized ? "Muted" : `${name} is muted`}
        </span>
      )}
      {call.cameraOn && call.localStream && !ended && (
        <StreamVideo
          className={`call-self${mirrorsSelfView(call.localStream) ? " mirrored" : ""}`}
          stream={call.localStream}
          muted
        />
      )}
      {!ended && (
        <div className="call-controls">
          <CallButton
            label={call.micOn ? "Mute" : "Unmute"}
            icon={call.micOn ? "mic" : "micOff"}
            kind={call.micOn ? undefined : "off"}
            onClick={() => store.calls.toggleMic()}
          />
          <CallButton
            label={call.cameraOn ? "Turn camera off" : "Turn camera on"}
            icon={call.cameraOn ? "video" : "videoOff"}
            kind={call.cameraOn ? undefined : "off"}
            onClick={() => void store.calls.toggleCamera()}
          />
          {severalCameras && call.cameraOn && !minimized && (
            <CallButton label="Switch camera" icon="switchCamera" onClick={() => void store.calls.switchCamera()} />
          )}
          <CallButton label="End call" icon="phone" kind="end" onClick={() => store.calls.hangUp()} />
        </div>
      )}
      {!ended && (
        <button
          className="call-size"
          type="button"
          aria-label={minimized ? "Expand call" : "Minimize call"}
          title={minimized ? "Expand" : "Minimize"}
          onClick={() => onMinimize(!minimized)}
        >
          <Icon name={minimized ? "maximize" : "minimize"} size={16} sw={2} />
        </button>
      )}
    </div>
  );
}

function CallButton({
  label,
  icon,
  kind,
  onClick,
}: {
  label: string;
  icon: IconName;
  /** end: red hang-up; accept: green; off: a switched-off mic or camera. */
  kind?: "end" | "accept" | "off";
  onClick: () => void;
}) {
  return (
    <button className={`call-btn${kind ? " " + kind : ""}`} type="button" aria-label={label} title={label} onClick={onClick}>
      <Icon name={icon} size={22} sw={1.9} />
    </button>
  );
}

/** A <video> bound to a MediaStream; remote audio plays through it. */
function StreamVideo({
  stream,
  muted = false,
  className,
  onFrames,
}: {
  stream: CallStream | null;
  muted?: boolean;
  className: string;
  /** Whether the element has a video frame to show. */
  onFrames?: (has: boolean) => void;
}) {
  const ref = useRef<HTMLVideoElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.muted = muted; // React doesn't keep the muted property in sync
    const media = stream as unknown as MediaStream | null;
    if (el.srcObject !== media) el.srcObject = media;
    if (media) void el.play().catch(() => undefined);
  }, [stream, muted]);
  useEffect(() => {
    const el = ref.current;
    if (!el || !onFrames) return;
    const check = () => onFrames(el.videoWidth > 0);
    const events = ["loadeddata", "resize", "emptied"];
    for (const e of events) el.addEventListener(e, check);
    check();
    return () => {
      for (const e of events) el.removeEventListener(e, check);
    };
  }, [onFrames]);
  return <video ref={ref} className={className} autoPlay playsInline />;
}

/** The peer's photo or initials; the peer may not be in the user list. */
function PeerAvatar({ peer, size }: { peer: PublicUser; size: number }) {
  const c = colorFor(peer.id);
  return (
    <div
      className="call-avatar"
      style={{
        width: size,
        height: size,
        fontSize: size * 0.36,
        background: `linear-gradient(135deg, ${c}, ${shade(c, -0.18)})`,
      }}
    >
      {peer.avatar ? <img src={peer.avatar} alt="" /> : <span>{initialsFor({ [peer.id]: peer }, peer.id)}</span>}
    </div>
  );
}

/** Re-renders once a second while on; returns the render's clock. */
function useSecondTicks(on: boolean): number {
  const [, tick] = useState(0);
  useEffect(() => {
    if (!on) return;
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, [on]);
  return Date.now();
}

/** Ringtone while a call rings here; ringback while ours rings the peer. */
function useCallSounds(call: CallSession | null) {
  const ringing = call?.phase === "incoming";
  const ringback = call?.phase === "outgoing" && call.ringing;
  useEffect(() => {
    if (!ringing) return;
    const audio = new Audio("/sound/RingTone.mp3");
    audio.loop = true;
    // Browsers refuse until the page has had a click; the call still shows.
    void audio.play().catch(() => undefined);
    return () => {
      audio.pause();
      audio.removeAttribute("src");
    };
  }, [ringing]);
  useEffect(() => (ringback ? playRingback() : undefined), [ringback]);
}

/** The ringback tone a caller hears: 440 Hz + 480 Hz, 2 s on, 4 s off. */
function playRingback(): () => void {
  const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!Ctx) return () => undefined;
  const ctx = new Ctx();
  const gain = ctx.createGain();
  gain.gain.value = 0;
  gain.connect(ctx.destination);
  for (const hz of [440, 480]) {
    const osc = ctx.createOscillator();
    osc.frequency.value = hz;
    osc.connect(gain);
    osc.start();
  }
  const start = ctx.currentTime + 0.05;
  // Covers the server's 45 s ring timeout with room to spare.
  for (let i = 0; i < 12; i++) {
    gain.gain.setValueAtTime(0.06, start + i * 6);
    gain.gain.setValueAtTime(0, start + i * 6 + 2);
  }
  return () => void ctx.close();
}

/** Asks before a reload or tab close drops a live call. */
function useLeaveWarning(on: boolean) {
  useEffect(() => {
    if (!on) return;
    const warn = (e: BeforeUnloadEvent) => {
      // Reads the live state: this listener stays until React re-renders,
      // which comes too late when a hang-up navigates away at once (logout).
      if (!store.calls.busy) return;
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [on]);
}
