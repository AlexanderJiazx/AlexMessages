/**
 * Voice capture for dictation and audio messages.
 *
 * The mic feeds a WebAudio ScriptProcessorNode which hands us raw Float32
 * PCM; we downmix to mono, keep a rolling level meter for the waveform UI,
 * and on stop() encode the buffer as 16 kHz mono WAV — the format the
 * transcription model (Voxtral) accepts and the smallest lossless upload.
 */
import { downmixToMono, encodeWav, resampleMono } from "@shared/wav";

const TARGET_RATE = 16000;

export interface RecordedClip {
  /** WAV file ready for upload (transcription or voice-message send). */
  blob: Blob;
  /** Duration in seconds. */
  duration: number;
  /** The captured mono PCM at the context sample rate (for the UI's wave). */
  peak: number;
}

export class VoiceRecorder {
  private stream: MediaStream | null = null;
  private ctx: AudioContext | null = null;
  private source: MediaStreamAudioSourceNode | null = null;
  private proc: ScriptProcessorNode | null = null;
  private chunks: Float32Array[] = [];
  private samples = 0;
  private level = 0;
  private peak = 0;
  private startTs = 0;
  private destroyed = false;

  /** Current smoothed input level 0..1 for the waveform bars. */
  getLevel(): number {
    return this.level;
  }

  get recording(): boolean {
    return !!this.proc;
  }

  /** Starts capturing. Throws a typed error when permission is denied. */
  async start(): Promise<void> {
    if (this.recording) return;
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
      });
    } catch (e) {
      const err = e as DOMException;
      if (err.name === "NotAllowedError" || err.name === "SecurityError") {
        throw new Error("denied");
      }
      if (err.name === "NotFoundError" || err.name === "OverconstrainedError") {
        throw new Error("no-mic");
      }
      throw err;
    }
    this.stream = stream;
    this.chunks = [];
    this.samples = 0;
    this.level = 0;
    this.peak = 0;
    this.startTs = performance.now();
    this.destroyed = false;

    const ctx = new AudioContext();
    this.ctx = ctx;
    this.source = ctx.createMediaStreamSource(stream);
    // ScriptProcessor is deprecated but still the only API that works
    // everywhere without a separate worklet module file (which Vite would
    // need to bundle specially). 4096 samples ≈ 85 ms per chunk at 48 kHz.
    this.proc = ctx.createScriptProcessor(4096, 2, 1);
    this.proc.onaudioprocess = (ev) => {
      const input = ev.inputBuffer;
      const mono =
        input.numberOfChannels > 1
          ? downmixToMono(interleave(input), input.numberOfChannels)
          : input.getChannelData(0).slice();
      // RMS-ish level for the waveform — smoothed so bars don't jitter.
      let sum = 0;
      for (let i = 0; i < mono.length; i += 4) sum += mono[i] * mono[i];
      const rms = Math.sqrt(sum / Math.max(1, mono.length / 4));
      this.level = this.level * 0.65 + Math.min(1, rms * 3.2) * 0.35;
      if (this.level > this.peak) this.peak = this.level;
      this.chunks.push(mono);
      this.samples += mono.length;
    };
    this.source.connect(this.proc);
    this.proc.connect(ctx.destination);
  }

  /**
   * Stops capture and returns the WAV clip, or null when nothing was
   * recorded (under ~150 ms — almost certainly an accidental tap).
   */
  async stop(): Promise<RecordedClip | null> {
    const ctx = this.ctx;
    const rate = ctx?.sampleRate || 48000;
    this.teardown();
    const pcm = concat(this.chunks, this.samples);
    const duration = this.samples / rate;
    if (!pcm.length || duration < 0.15) return null;
    const resampled = rate === TARGET_RATE ? pcm : resampleMono(pcm, rate, TARGET_RATE);
    const wav = encodeWav(resampled, { sampleRate: TARGET_RATE });
    return {
      blob: new Blob([wav], { type: "audio/wav" }),
      duration: (performance.now() - this.startTs) / 1000,
      peak: this.peak,
    };
  }

  /** Aborts capture and discards everything (the Cancel action). */
  cancel(): void {
    this.chunks = [];
    this.samples = 0;
    this.teardown();
  }

  private teardown(): void {
    if (this.proc) {
      this.proc.onaudioprocess = null;
      try {
        this.proc.disconnect();
      } catch {
        /* already detached */
      }
      this.proc = null;
    }
    if (this.source) {
      try {
        this.source.disconnect();
      } catch {
        /* already detached */
      }
      this.source = null;
    }
    if (this.ctx) {
      this.ctx.close().catch(() => {});
      this.ctx = null;
    }
    if (this.stream) {
      for (const t of this.stream.getTracks()) t.stop();
      this.stream = null;
    }
    this.level = 0;
  }
}

/** Interleaves multi-channel AudioBuffer data for downmixToMono. */
function interleave(buf: AudioBuffer): Float32Array {
  const n = buf.length;
  const ch = buf.numberOfChannels;
  const out = new Float32Array(n * ch);
  for (let c = 0; c < ch; c++) {
    const data = buf.getChannelData(c);
    for (let i = 0; i < n; i++) out[i * ch + c] = data[i];
  }
  return out;
}

function concat(chunks: Float32Array[], total: number): Float32Array {
  const out = new Float32Array(total);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.length;
  }
  return out;
}
