import { useEffect, useRef, useState } from "react";
import { api, store, toast } from "../client";
import { VoiceRecorder, type RecordedClip } from "../audio/recorder";
import { Icon } from "./icons";

type Phase =
  | { kind: "idle" }
  | { kind: "recording" }
  | { kind: "transcribing" };

const WAVE_BARS = 42;
const VOICE_NAME = "voice-message.wav";

/**
 * The ChatGPT-style dictation bar: [✕ cancel] [live waveform] [■ stop→
 * transcribe] [⇪ send-audio] [⬆ transcribe+send]. It replaces the composer's
 * input row while active.
 *
 * Actions:
 *   cancel        — discard the recording entirely
 *   stop          — end capture, transcribe, put the text in the composer
 *   send-audio    — end capture, upload the WAV as a voice-message attachment
 *   send          — end capture, transcribe, and send the text immediately
 */
export function DictationBar({
  onTranscribed,
  onExit,
}: {
  /** Fills the composer textarea with transcript text (stop action). */
  onTranscribed: (text: string) => void;
  /** Leaves dictation mode entirely (cancel). */
  onExit: () => void;
}) {
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const [elapsed, setElapsed] = useState(0);
  const [levels, setLevels] = useState<number[]>(() => new Array(WAVE_BARS).fill(0));
  const recRef = useRef<VoiceRecorder | null>(null);
  const clipRef = useRef<RecordedClip | null>(null);

  // Start the mic on mount; report permission problems and bail out.
  useEffect(() => {
    const rec = new VoiceRecorder();
    recRef.current = rec;
    rec.start()
      .then(() => setPhase({ kind: "recording" }))
      .catch((e: Error) => {
        toast(
          e.message === "denied"
            ? "Microphone access denied — allow it in your browser settings"
            : e.message === "no-mic"
              ? "No microphone found"
              : "Couldn't start recording",
          true,
        );
        onExit();
      });
    return () => rec.cancel();
  }, [onExit]);

  // Waveform ticker: shifts the rolling level history left and pushes the
  // current smoothed level on the right — the ChatGPT scrolling-wave look.
  useEffect(() => {
    if (phase.kind !== "recording") return;
    const t0 = Date.now();
    const iv = setInterval(() => {
      const rec = recRef.current;
      setElapsed(Math.floor((Date.now() - t0) / 1000));
      const lvl = rec ? rec.getLevel() : 0;
      setLevels((prev) => [...prev.slice(1), lvl]);
    }, 80);
    return () => clearInterval(iv);
  }, [phase.kind]);

  async function finishCapture(): Promise<RecordedClip | null> {
    const rec = recRef.current;
    recRef.current = null;
    if (!rec) return null;
    const clip = await rec.stop().catch(() => null);
    if (!clip) {
      toast("Recording too short", true);
      return null;
    }
    return clip;
  }

  function cancel() {
    recRef.current?.cancel();
    recRef.current = null;
    onExit();
  }

  async function transcribeClip(clip: RecordedClip): Promise<string | null> {
    try {
      const file = new File([clip.blob], VOICE_NAME, { type: "audio/wav" });
      const { text } = await api.transcribe(file);
      return text.trim();
    } catch (e) {
      toast((e as { detail?: string }).detail || "Transcription failed", true);
      return null;
    }
  }

  async function onStopTranscribe() {
    if (phase.kind !== "recording") return;
    setPhase({ kind: "transcribing" });
    const clip = await finishCapture();
    if (!clip) {
      onExit();
      return;
    }
    clipRef.current = clip;
    const text = await transcribeClip(clip);
    if (text == null) {
      onExit();
      return;
    }
    onTranscribed(text);
    onExit();
  }

  async function onSendText() {
    if (phase.kind !== "recording") return;
    setPhase({ kind: "transcribing" });
    const clip = await finishCapture();
    if (!clip) {
      onExit();
      return;
    }
    const text = await transcribeClip(clip);
    if (text == null) {
      onExit();
      return;
    }
    store.sendMessage(text);
    onExit();
  }

  async function onSendAudio() {
    if (phase.kind !== "recording") return;
    setPhase({ kind: "transcribing" });
    const clip = await finishCapture();
    if (!clip) {
      onExit();
      return;
    }
    try {
      const file = new File([clip.blob], VOICE_NAME, { type: "audio/wav" });
      const att = await api.upload(file);
      store.sendMessage("", [
        {
          name: att.name || VOICE_NAME,
          url: att.url,
          size: att.size,
          mime: att.mime || "audio/wav",
          width: 0,
          height: 0,
        },
      ]);
    } catch {
      toast("Couldn't send voice message", true);
    }
    onExit();
  }

  const recording = phase.kind === "recording";
  const busy = phase.kind === "transcribing";

  return (
    <div className="dictation-bar" role="group" aria-label="Voice dictation">
      <button
        className="dict-btn dict-cancel"
        onClick={cancel}
        aria-label="Cancel recording"
        title="Cancel"
        disabled={busy}
      >
        <Icon name="x" size={17} sw={2.2} />
      </button>

      <div className="dict-wave" aria-hidden="true">
        {busy ? (
          <span className="dict-status">
            <span className="spinner" /> Transcribing…
          </span>
        ) : (
          levels.map((l, i) => (
            <span
              key={i}
              className="dict-bar"
              style={{ height: `${Math.max(3, Math.round(3 + l * 22))}px` }}
            />
          ))
        )}
      </div>
      <span className="dict-time">{formatElapsed(elapsed)}</span>

      <button
        className="dict-btn dict-stop"
        onClick={onStopTranscribe}
        aria-label="Stop and transcribe"
        title="Stop & transcribe"
        disabled={busy}
      >
        <Icon name="stop" size={14} sw={2} />
      </button>
      <button
        className="dict-btn dict-audio"
        onClick={onSendAudio}
        aria-label="Send as voice message"
        title="Send voice message"
        disabled={busy}
      >
        <Icon name="wave" size={16} sw={1.9} />
      </button>
      <button
        className="dict-btn dict-send"
        onClick={onSendText}
        aria-label="Transcribe and send"
        title="Transcribe & send"
        disabled={busy || !recording}
      >
        <Icon name="send" size={17} sw={2.2} />
      </button>
    </div>
  );
}

function formatElapsed(sec: number): string {
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}
