/**
 * Voice recording — wraps expo-audio's recorder for the DictationBar.
 *
 * iOS records true WAV (LinearPCM, 16 kHz mono) → the backend routes wav/mp3
 * to Voxtral, the strongest OpenRouter transcription model. Android records
 * AAC .m4a → the backend falls back to Gemini audio ingestion. Metering feeds
 * the live waveform.
 */
import { Platform } from "react-native";
import {
  AudioModule,
  AudioQuality,
  IOSOutputFormat,
  RecordingPresets,
  setAudioModeAsync,
  useAudioRecorder,
  useAudioRecorderState,
  type RecordingOptions,
} from "expo-audio";
import { useEffect, useRef, useState } from "react";
import { isCallLive } from "./media";

const RECORDING_OPTIONS: RecordingOptions = {
  ...RecordingPresets.HIGH_QUALITY,
  isMeteringEnabled: true,
  ...(Platform.OS === "ios"
    ? {
        extension: ".wav",
        ios: {
          audioQuality: AudioQuality.MAX,
          outputFormat: IOSOutputFormat.LINEARPCM,
          linearPCMBitDepth: 16,
          linearPCMIsBigEndian: false,
          linearPCMIsFloat: false,
          sampleRate: 16000,
        },
      }
    : {
        extension: ".m4a",
        android: {
          extension: ".m4a",
          outputFormat: "mpeg4",
          audioEncoder: "aac",
          sampleRate: 16000,
        },
      }),
};

/**
 * Session for playing voice messages and audio attachments. Every field of
 * expo-audio's native AudioMode has a default, so a partial
 * setAudioModeAsync() resets the rest — notably `playsInSilentMode: false`,
 * which on iOS selects the `.soloAmbient` category and the ring/silent
 * switch mutes all playback. Always set the full playback mode.
 */
const PLAYBACK_MODE = {
  playsInSilentMode: true,
  allowsRecording: false,
  interruptionMode: "duckOthers",
  shouldPlayInBackground: false,
} as const;

/** Puts the audio session in playback mode (audible on silent, loudspeaker).
 *  Never during a call: the playback category has no microphone. */
export async function enablePlayback(): Promise<void> {
  if (isCallLive()) return;
  try {
    await setAudioModeAsync(PLAYBACK_MODE);
  } catch {
    /* noop — playback still works, just obeys the silent switch */
  }
}

export interface VoiceRecorder {
  isRecording: boolean;
  durationMillis: number;
  /** Normalized level 0..1 from the most recent metering sample. */
  level: number;
  /**
   * "started"; "failed" (no permission, or the recorder failed); or
   * "cancelled" when cancel() came while it was starting — then nothing
   * records and the microphone is released.
   */
  start(): Promise<"started" | "failed" | "cancelled">;
  /** Stops and returns { uri, mime, ext } or null on failure/too-short. */
  stop(): Promise<{ uri: string; mime: string; ext: string } | null>;
  cancel(): Promise<void>;
}

export function useVoiceRecorder(): VoiceRecorder {
  const recorder = useAudioRecorder(RECORDING_OPTIONS);
  const state = useAudioRecorderState(recorder, 100);
  const startedRef = useRef(0);
  const [level, setLevel] = useState(0);

  // Metering arrives as dBFS (-160..0); normalize to a 0..1 level that looks
  // alive for speech without overreacting to silence.
  const db = state.metering;
  useEffect(() => {
    if (db == null || !state.isRecording) {
      setLevel(0);
      return;
    }
    const norm = Math.min(1, Math.max(0, (db + 60) / 50));
    setLevel(norm);
  }, [db, state.isRecording]);

  // Bumped by cancel(): a start() that was waiting (on the permission
  // prompt, the audio session, the recorder) when the user cancelled must
  // not go on to open the microphone.
  const attempt = useRef(0);

  const start = async (): Promise<"started" | "failed" | "cancelled"> => {
    const mine = ++attempt.current;
    const cancelled = () => attempt.current !== mine;
    try {
      const perm = await AudioModule.requestRecordingPermissionsAsync();
      if (cancelled()) return "cancelled";
      if (!perm.granted) return "failed";
      await setAudioModeAsync({ allowsRecording: true, playsInSilentMode: true });
      if (cancelled()) {
        await enablePlayback();
        return "cancelled";
      }
      await recorder.prepareToRecordAsync();
      if (cancelled()) {
        await enablePlayback();
        return "cancelled";
      }
      recorder.record();
      startedRef.current = Date.now();
      return "started";
    } catch {
      return cancelled() ? "cancelled" : "failed";
    }
  };

  const stop = async (): Promise<{ uri: string; mime: string; ext: string } | null> => {
    try {
      if (!state.isRecording) return null;
      await recorder.stop();
      await enablePlayback();
      const uri = recorder.uri;
      if (!uri || Date.now() - startedRef.current < 400) return null;
      if (Platform.OS === "ios") return { uri, mime: "audio/wav", ext: "wav" };
      return { uri, mime: "audio/mp4", ext: "m4a" };
    } catch {
      return null;
    }
  };

  const cancel = async (): Promise<void> => {
    attempt.current++;
    try {
      // The recorder's own flag, not the last render's state: a recording
      // that began after that render is stopped too.
      if (recorder.isRecording || state.isRecording) await recorder.stop();
      await enablePlayback();
    } catch {
      /* noop */
    }
  };

  return {
    isRecording: state.isRecording,
    durationMillis: state.durationMillis,
    level,
    start,
    stop,
    cancel,
  };
}
