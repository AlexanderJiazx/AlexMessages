/**
 * Call sounds and audio routing through react-native-incall-manager, the
 * companion library react-native-webrtc apps use for this:
 *  - the ringtone (assets/sounds/incallmanager_ringtone.mp3, the web's
 *    RingTone.mp3) with vibration; on iOS it obeys the ring/silent switch,
 *  - the in-call audio session (iOS: playAndRecord, voiceChat for voice
 *    calls — earpiece + proximity sensor — and videoChat for video calls —
 *    loudspeaker; Android: MODE_IN_COMMUNICATION, earpiece for voice and
 *    loudspeaker for video, a wired headset when one is plugged in — not
 *    Bluetooth, which needs the BLUETOOTH_CONNECT permission the app does
 *    not ask for), plus keep-screen-on,
 *  - the ringback a caller hears (assets/sounds/incallmanager_ringback.mp3:
 *    440 + 480 Hz, 2 s on, 4 s off),
 *  - the loudspeaker override.
 * The sound files reach the app bundle (iOS) and res/raw (Android) through
 * the expo-asset config plugin in app.json; "_BUNDLE_" plays them.
 */
import InCallManager from "react-native-incall-manager";
import type { CallMedia } from "@alexmessages/shared";

/** Android: wait 0, then 1 s on / 1 s off for the server's 45 s of ringing.
 *  iOS vibrates ~0.4 s at a time and reads every entry as a pause. */
const RING_VIBRATION = [0, ...Array.from({ length: 45 }, () => 1000)];

export const callAudio = {
  startRingtone(): void {
    // "" = iOS's soloAmbient category, which the silent switch mutes.
    InCallManager.startRingtone("_BUNDLE_", RING_VIBRATION, "", -1);
  },
  stopRingtone(): void {
    InCallManager.stopRingtone();
  },
  /** The in-call audio session. Safe to call again while it runs. */
  start(media: CallMedia): void {
    InCallManager.start({ media });
  },
  stop(): void {
    InCallManager.stop();
  },
  startRingback(): void {
    InCallManager.startRingback("_BUNDLE_");
  },
  stopRingback(): void {
    InCallManager.stopRingback();
  },
  /** true: the loudspeaker; false: the earpiece (a wired headset when one is
   *  plugged in), also for a video call. */
  setSpeaker(on: boolean): void {
    InCallManager.setForceSpeakerphoneOn(on);
  },
  /** Keeps the screen on (start() also does, until stop()). */
  keepScreenOn(on: boolean): void {
    InCallManager.setKeepScreenOn(on);
  },
};
