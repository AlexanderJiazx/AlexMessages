/**
 * DM channel helpers.
 *
 * Every conversation is a synthetic `dm:<lo>:<hi>` channel — the two user ids,
 * smaller first. These mirror db.DMChannelID / db.ParseDMChannel on the Go
 * server and must stay byte-compatible.
 */

export function isDM(channel: string | null | undefined): channel is string {
  return typeof channel === "string" && channel.startsWith("dm:");
}

/** Parses `dm:<a>:<b>` into its two member ids, or null when malformed. */
export function parseDMChannel(channel: string): [number, number] | null {
  if (!isDM(channel)) return null;
  const parts = channel.split(":");
  if (parts.length !== 3) return null;
  const a = parseInt(parts[1], 10);
  const b = parseInt(parts[2], 10);
  if (!Number.isFinite(a) || !Number.isFinite(b)) return null;
  return [a, b];
}

/** Returns the channel's other participant relative to `meId`. */
export function dmPeerOf(channel: string | null | undefined, meId: number | null | undefined): number | null {
  if (meId == null) return null;
  const ids = channel ? parseDMChannel(channel) : null;
  if (!ids) return null;
  return ids[0] === meId ? ids[1] : ids[0];
}

/** Builds the canonical channel id for two users (smaller id first). */
export function dmChannelFor(a: number, b: number): string {
  const lo = Math.min(a, b);
  const hi = Math.max(a, b);
  return `dm:${lo}:${hi}`;
}
