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

export interface VoiceRecorder {
  isRecording: boolean;
  durationMillis: number;
  /** Normalized level 0..1 from the most recent metering sample. */
  level: number;
  start(): Promise<boolean>;
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

  const start = async (): Promise<boolean> => {
    try {
      const perm = await AudioModule.requestRecordingPermissionsAsync();
      if (!perm.granted) return false;
      await setAudioModeAsync({ allowsRecording: true, playsInSilentMode: true });
      await recorder.prepareToRecordAsync();
      recorder.record();
      startedRef.current = Date.now();
      return true;
    } catch {
      return false;
    }
  };

  const stop = async (): Promise<{ uri: string; mime: string; ext: string } | null> => {
    try {
      if (!state.isRecording) return null;
      await recorder.stop();
      await setAudioModeAsync({ allowsRecording: false });
      const uri = recorder.uri;
      if (!uri || Date.now() - startedRef.current < 400) return null;
      if (Platform.OS === "ios") return { uri, mime: "audio/wav", ext: "wav" };
      return { uri, mime: "audio/mp4", ext: "m4a" };
    } catch {
      return null;
    }
  };

  const cancel = async (): Promise<void> => {
    try {
      if (state.isRecording) await recorder.stop();
      await setAudioModeAsync({ allowsRecording: false });
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
