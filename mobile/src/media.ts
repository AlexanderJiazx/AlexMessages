/**
 * In-app media versus calls. While a call is live on this device, voice
 * messages, audio files, and videos don't play and nothing records: those
 * players reconfigure the audio session a call runs on. On iOS, expo-audio
 * deactivates the session ~100 ms after a pause (AVAudioSession stops all
 * running audio, the call's included), enablePlayback() and expo-video
 * switch it to the playback category (no microphone), and the recorder
 * deactivates it when it stops. On Android a recording would hold the
 * microphone the call needs.
 *
 * Players and the recorder register a stopper here; CallHost stops them all
 * when a call starts, before its own audio begins.
 */

type Stopper = () => boolean;

const stoppers = new Set<Stopper>();
let callLive = false;

/** `stop` halts the media if it is running and says whether it was. */
export function registerMedia(stop: Stopper): () => void {
  stoppers.add(stop);
  return () => {
    stoppers.delete(stop);
  };
}

/** Halts all in-app playback and recording; true if anything was running. */
export function stopAllMedia(): boolean {
  let any = false;
  for (const stop of [...stoppers]) {
    try {
      if (stop()) any = true;
    } catch {
      /* a player already gone */
    }
  }
  return any;
}

/**
 * How long a call's audio waits after in-app media was stopped for it:
 * expo-audio deactivates the iOS audio session 100 ms after a pause.
 */
export const MEDIA_HANDOFF_MS = 250;

let handoff: { key: number; readyAt: number } | null = null;

/**
 * Stops in-app media for the call with this key (CallSession.key), once per
 * call, and returns when the call's own audio may start (Date.now() ms).
 * CallHost and the system call bridge (src/call/system.ios.ts) both ask.
 */
export function handOffMedia(key: number): number {
  if (handoff?.key !== key) {
    handoff = { key, readyAt: Date.now() + (stopAllMedia() ? MEDIA_HANDOFF_MS : 0) };
  }
  return handoff.readyAt;
}

export function setCallLive(live: boolean): void {
  callLive = live;
  if (!live) handoff = null;
}

/** Whether a call is live on this device (ringing included). */
export function isCallLive(): boolean {
  return callLive;
}

/** What the media controls say while a call is live. */
export const CALL_BLOCKS_MEDIA = "Not available during a call";
