/**
 * react-native-webrtc behind the shared CallManager (shared/src/call.ts) —
 * the native twin of web/src/call-platform.ts. Expo web gets platform.ts
 * instead: no calls there.
 *
 * react-native-webrtc's tracks and streams are native objects that live
 * until released, and its stop() only disables a track — the capturer
 * (camera session, capture thread, buffers) stays allocated. The manager
 * expects browser behavior, so this adapter supplies it:
 *  - stop() on a captured track disables it at once (the camera goes off)
 *    and releases it as soon as no stream from createStream holds it any
 *    more. On Android, releasing a track that a stream still lists makes
 *    that stream's release throw, so streams always let go first.
 *  - the stream getUserMedia returns is released as soon as its tracks are
 *    taken, and the manager releases the ones it shows (releaseStream).
 */
import { mediaDevices, MediaStream, MediaStreamTrack, RTCPeerConnection } from "react-native-webrtc";
import type { CallPeerConnection, CallPlatform, CallStream, CallTrack } from "@alexmessages/shared";

/** How many unreleased createStream streams hold each captured track. */
const holders = new Map<MediaStreamTrack, number>();
/** Captured tracks that were stopped but are still held by a stream. */
const stopped = new Set<MediaStreamTrack>();

function releaseIfFree(track: MediaStreamTrack): void {
  if (!stopped.has(track) || (holders.get(track) ?? 0) > 0) return;
  stopped.delete(track);
  holders.delete(track);
  track.release();
}

/** Gives a captured track the browser's stop(). */
function owned(track: MediaStreamTrack): MediaStreamTrack {
  const disable = track.stop.bind(track);
  track.stop = () => {
    if (stopped.has(track)) return;
    disable();
    stopped.add(track);
    releaseIfFree(track);
  };
  return track;
}

function trackList(tracks: MediaStreamTrack[]): CallStream {
  const all = tracks as unknown as CallTrack[];
  return {
    getTracks: () => all.slice(),
    getAudioTracks: () => all.filter((t) => t.kind === "audio"),
    getVideoTracks: () => all.filter((t) => t.kind === "video"),
  };
}

export const callPlatform: CallPlatform | null = {
  createPeerConnection: ({ iceServers }) =>
    new RTCPeerConnection({ iceServers }) as unknown as CallPeerConnection,

  async getUserMedia(c) {
    const stream = await mediaDevices.getUserMedia({
      audio: c.audio,
      video: c.video ? { facingMode: c.video.facingMode, width: 1280, height: 720, frameRate: 30 } : false,
    });
    const tracks = stream.getTracks().map(owned);
    // Free the native container; its tracks live on.
    stream.release(false);
    // A refused permission doesn't fail the request here (as it does in a
    // browser): that track is just missing. Say so the way a browser would.
    if (c.audio && !tracks.some((t) => t.kind === "audio")) {
      for (const t of tracks) t.stop();
      throw Object.assign(new Error("Microphone permission denied"), { name: "NotAllowedError" });
    }
    return trackList(tracks);
  },

  createStream(tracks) {
    const native = tracks as unknown as MediaStreamTrack[];
    for (const t of native) if (!t.remote) holders.set(t, (holders.get(t) ?? 0) + 1);
    return new MediaStream(native) as unknown as CallStream;
  },

  releaseStream(stream) {
    const s = stream as unknown as MediaStream;
    const tracks = s.getTracks();
    // Takes the tracks out first, so the native release leaves them alone.
    s.release(false);
    for (const t of tracks) {
      if (t.remote) continue;
      holders.set(t, (holders.get(t) ?? 1) - 1);
      releaseIfFree(t);
    }
  },
};
