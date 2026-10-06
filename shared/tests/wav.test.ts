import { describe, expect, it } from "vitest";
import { downmixToMono, encodeWav, resampleMono } from "../src/wav";

describe("encodeWav", () => {
  it("writes a valid RIFF/WAVE header", () => {
    const pcm = new Float32Array([0, 0.5, -0.5, 1, -1]);
    const buf = encodeWav(pcm, { sampleRate: 16000 });
    const v = new DataView(buf);
    const ascii = (off: number, len: number) =>
      String.fromCharCode(...new Uint8Array(buf.slice(off, off + len)));
    expect(ascii(0, 4)).toBe("RIFF");
    expect(ascii(8, 4)).toBe("WAVE");
    expect(ascii(12, 4)).toBe("fmt ");
    expect(v.getUint16(20, true)).toBe(1); // PCM
    expect(v.getUint16(22, true)).toBe(1); // mono
    expect(v.getUint32(24, true)).toBe(16000);
    expect(ascii(36, 4)).toBe("data");
    expect(v.getUint32(40, true)).toBe(pcm.length * 2);
    expect(buf.byteLength).toBe(44 + pcm.length * 2);
  });

  it("clamps samples to 16-bit range", () => {
    const buf = encodeWav(new Float32Array([2, -2]), { sampleRate: 8000 });
    const v = new DataView(buf);
    expect(v.getInt16(44, true)).toBe(0x7fff);
    expect(v.getInt16(46, true)).toBe(-0x8000);
  });
});

describe("resampleMono", () => {
  it("halves the sample count for 2x downsampling", () => {
    const pcm = new Float32Array(100).fill(0.5);
    const out = resampleMono(pcm, 48000, 24000);
    expect(out.length).toBe(50);
  });

  it("interpolates between samples", () => {
    const pcm = new Float32Array([0, 1, 0]);
    const out = resampleMono(pcm, 3, 2); // positions 0 and 1.5
    expect(out[0]).toBe(0);
    expect(out[1]).toBeCloseTo(0.5);
  });
});

describe("downmixToMono", () => {
  it("averages channels", () => {
    const stereo = new Float32Array([1, -1, 0.5, 0.5]);
    const mono = downmixToMono(stereo, 2);
    expect(mono[0]).toBe(0);
    expect(mono[1]).toBe(0.5);
  });

  it("passes mono through untouched", () => {
    const mono = new Float32Array([0.3]);
    expect(downmixToMono(mono, 1)).toBe(mono);
  });
});
