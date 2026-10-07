/**
 * Minimal PCM → WAV encoder used by voice dictation.
 *
 * The transcription model (Voxtral via OpenRouter) accepts wav/mp3, so the
 * clients capture raw PCM through the audio stack and package it as a
 * standard 16-bit PCM WAV — no server-side transcoding needed.
 */

export interface WavEncodeOptions {
  /** PCM sample rate of `samples` (e.g. 16000, 48000). */
  sampleRate: number;
  /** Channel count; dictation always captures mono (1). */
  channels?: number;
}

/**
 * Encodes float PCM samples (-1..1) into a 16-bit PCM WAV blob.
 * Samples for multiple channels are interleaved.
 */
export function encodeWav(samples: Float32Array, opts: WavEncodeOptions): ArrayBuffer {
  const channels = opts.channels ?? 1;
  const sampleRate = opts.sampleRate;
  const bytesPerSample = 2;
  const blockAlign = channels * bytesPerSample;
  const dataLen = samples.length * bytesPerSample;
  const buffer = new ArrayBuffer(44 + dataLen);
  const view = new DataView(buffer);

  writeAscii(view, 0, "RIFF");
  view.setUint32(4, 36 + dataLen, true);
  writeAscii(view, 8, "WAVE");
  writeAscii(view, 12, "fmt ");
  view.setUint32(16, 16, true); // PCM header size
  view.setUint16(20, 1, true); // PCM format tag
  view.setUint16(22, channels, true);
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * blockAlign, true);
  view.setUint16(32, blockAlign, true);
  view.setUint16(34, 16, true); // bits per sample
  writeAscii(view, 36, "data");
  view.setUint32(40, dataLen, true);

  let offset = 44;
  for (let i = 0; i < samples.length; i++) {
    const s = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(offset, s < 0 ? s * 0x8000 : s * 0x7fff, true);
    offset += 2;
  }
  return buffer;
}

/** Resamples interleaved mono PCM to a target rate (linear interpolation —
 *  plenty for speech; keeps the encoder dependency-free). */
export function resampleMono(samples: Float32Array, fromRate: number, toRate: number): Float32Array {
  if (fromRate === toRate || samples.length === 0) return samples;
  const ratio = fromRate / toRate;
  const outLen = Math.floor(samples.length / ratio);
  const out = new Float32Array(outLen);
  for (let i = 0; i < outLen; i++) {
    const pos = i * ratio;
    const idx = Math.floor(pos);
    const frac = pos - idx;
    const a = samples[idx];
    const b = idx + 1 < samples.length ? samples[idx + 1] : a;
    out[i] = a + (b - a) * frac;
  }
  return out;
}

/** Downmixes interleaved multi-channel PCM to mono by averaging. */
export function downmixToMono(interleaved: Float32Array, channels: number): Float32Array {
  if (channels <= 1) return interleaved;
  const frames = Math.floor(interleaved.length / channels);
  const out = new Float32Array(frames);
  for (let i = 0; i < frames; i++) {
    let sum = 0;
    for (let c = 0; c < channels; c++) sum += interleaved[i * channels + c];
    out[i] = sum / channels;
  }
  return out;
}

function writeAscii(view: DataView, offset: number, text: string): void {
  for (let i = 0; i < text.length; i++) view.setUint8(offset + i, text.charCodeAt(i));
}

/**
 * RMS envelope of a 16-bit PCM WAV, bucketed into `bars` values normalized
 * to 0..1 (floor 0.08 so silence still draws a bar), plus the clip duration.
 * Walks the RIFF chunks, so extra chunks (`LIST`, `FLLR` from iOS) are fine.
 * Returns null for anything that isn't 16-bit PCM WAV — callers fall back to
 * placeholder bars (e.g. Android's AAC `.m4a` voice messages).
 */
export function wavPeaks(buf: ArrayBuffer, bars: number): { peaks: number[]; duration: number } | null {
  if (buf.byteLength < 12 || bars < 1) return null;
  const v = new DataView(buf);
  const tag = (off: number) =>
    String.fromCharCode(v.getUint8(off), v.getUint8(off + 1), v.getUint8(off + 2), v.getUint8(off + 3));
  if (tag(0) !== "RIFF" || tag(8) !== "WAVE") return null;
  let channels = 0;
  let rate = 0;
  let bits = 0;
  let off = 12;
  while (off + 8 <= buf.byteLength) {
    const id = tag(off);
    const len = v.getUint32(off + 4, true);
    const body = off + 8;
    if (id === "fmt " && len >= 16) {
      if (v.getUint16(body, true) !== 1) return null; // PCM only
      channels = v.getUint16(body + 2, true);
      rate = v.getUint32(body + 4, true);
      bits = v.getUint16(body + 14, true);
    } else if (id === "data") {
      if (bits !== 16 || channels < 1 || rate < 1) return null;
      const frameBytes = 2 * channels;
      const end = Math.min(buf.byteLength, body + len);
      const frames = Math.floor((end - body) / frameBytes);
      if (frames < 1) return null;
      const per = Math.max(1, Math.floor(frames / bars));
      const raw: number[] = [];
      for (let b = 0; b < bars; b++) {
        const f0 = b * per;
        const f1 = Math.min(frames, f0 + per);
        let sum = 0;
        for (let f = f0; f < f1; f++) {
          const s = v.getInt16(body + f * frameBytes, true) / 0x8000;
          sum += s * s;
        }
        raw.push(f1 > f0 ? Math.sqrt(sum / (f1 - f0)) : 0);
      }
      const max = Math.max(...raw, 1e-4);
      return {
        peaks: raw.map((x) => Math.max(0.08, Math.min(1, x / max))),
        duration: frames / rate,
      };
    }
    off = body + len + (len & 1); // chunks are word-aligned
  }
  return null;
}
