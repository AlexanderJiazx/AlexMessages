import { useEffect, useRef, useState } from "react";
import type { Attachment } from "@shared/types";
import { hash } from "@shared/format";
import { Icon } from "./icons";

const BARS = 36;

interface Decoded {
  peaks: number[];
  duration: number;
}

/** Decoded waveform per URL — voice clips never change once sent. */
const decodeCache = new Map<string, Promise<Decoded | null>>();

/** The voice message currently playing; starting another pauses it. */
let current: HTMLAudioElement | null = null;

/**
 * Peak envelope of a clip, normalized to 0..1, decoded with an
 * OfflineAudioContext (no autoplay gate, no audible output). Null when the
 * browser can't decode the container — callers fall back to a stable
 * pseudo-waveform.
 */
function decode(url: string): Promise<Decoded | null> {
  let p = decodeCache.get(url);
  if (!p) {
    p = (async () => {
      try {
        const Ctx = window.OfflineAudioContext || (window as any).webkitOfflineAudioContext;
        if (!Ctx) return null;
        const buf = await (await fetch(url)).arrayBuffer();
        const audio = await new Ctx(1, 1, 44100).decodeAudioData(buf);
        const data = audio.getChannelData(0);
        const step = Math.max(1, Math.floor(data.length / BARS));
        const peaks: number[] = [];
        for (let i = 0; i < BARS; i++) {
          let sum = 0;
          const end = Math.min(data.length, (i + 1) * step);
          for (let j = i * step; j < end; j++) sum += data[j] * data[j];
          peaks.push(Math.sqrt(sum / Math.max(1, end - i * step)));
        }
        const max = Math.max(...peaks, 1e-4);
        return {
          peaks: peaks.map((v) => Math.max(0.08, Math.min(1, v / max))),
          duration: audio.duration,
        };
      } catch {
        return null;
      }
    })();
    decodeCache.set(url, p);
  }
  return p;
}

/** Deterministic stand-in bars, so an undecodable clip still looks like speech. */
function fallbackPeaks(url: string): number[] {
  let h = hash(url) || 1;
  const out: number[] = [];
  for (let i = 0; i < BARS; i++) {
    h = (h * 1103515245 + 12345) & 0x7fffffff;
    out.push(0.2 + ((h >> 8) % 1000) / 1250);
  }
  return out;
}

function fmtClock(sec: number): string {
  const s = Math.max(0, Math.round(sec));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

/**
 * A recorded voice message: round play/pause, its waveform (tinted up to
 * the playhead, click to seek), and the remaining/total duration. Uploaded
 * audio files use the named audio card instead (see AttachmentView).
 */
export function VoiceMessage({ a }: { a: Attachment }) {
  const audioRef = useRef<HTMLAudioElement>(null);
  const [peaks, setPeaks] = useState<number[]>(() => fallbackPeaks(a.url));
  const [playing, setPlaying] = useState(false);
  const [time, setTime] = useState(0);
  const [duration, setDuration] = useState(0);

  useEffect(() => {
    let live = true;
    void decode(a.url).then((d) => {
      if (!live || !d) return;
      setPeaks(d.peaks);
      setDuration((cur) => cur || d.duration);
    });
    return () => {
      live = false;
    };
  }, [a.url]);

  useEffect(() => {
    const el = audioRef.current;
    return () => {
      if (el && current === el) current = null;
    };
  }, []);

  const toggle = () => {
    const el = audioRef.current;
    if (!el) return;
    if (!el.paused) {
      el.pause();
      return;
    }
    if (current && current !== el) current.pause();
    current = el;
    if (el.ended) el.currentTime = 0;
    void el.play().catch(() => setPlaying(false));
  };

  const seek = (e: React.MouseEvent<HTMLDivElement>) => {
    const el = audioRef.current;
    if (!el || !duration) return;
    const r = e.currentTarget.getBoundingClientRect();
    el.currentTime = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)) * duration;
    setTime(el.currentTime);
  };

  // WebAudio-recorded WAVs report Infinity until fully buffered.
  const onMeta = () => {
    const d = audioRef.current?.duration ?? 0;
    if (Number.isFinite(d) && d > 0) setDuration(d);
  };

  const progress = duration > 0 ? time / duration : 0;
  const shown = playing || time > 0 ? duration - time : duration;

  return (
    <div className={`voice-msg${playing ? " playing" : ""}`}>
      <button
        type="button"
        className="voice-play"
        onClick={toggle}
        aria-label={playing ? "Pause voice message" : "Play voice message"}
      >
        <Icon name={playing ? "pauseFill" : "playFill"} size={14} />
      </button>
      <div
        className="voice-wave"
        onClick={seek}
        role="slider"
        aria-label="Voice message position"
        aria-valuemin={0}
        aria-valuemax={Math.round(duration)}
        aria-valuenow={Math.round(time)}
      >
        {peaks.map((v, i) => (
          <span
            key={i}
            className={(i + 0.5) / peaks.length <= progress ? "on" : undefined}
            style={{ height: `${Math.round(v * 100)}%` }}
          />
        ))}
      </div>
      <span className="voice-time">{duration ? fmtClock(shown) : "0:00"}</span>
      <audio
        ref={audioRef}
        src={a.url}
        preload="metadata"
        onLoadedMetadata={onMeta}
        onDurationChange={onMeta}
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onEnded={() => {
          setPlaying(false);
          setTime(0);
        }}
        onTimeUpdate={(e) => setTime(e.currentTarget.currentTime)}
      />
    </div>
  );
}
