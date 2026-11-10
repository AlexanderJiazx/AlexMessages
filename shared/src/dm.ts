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

/** Parses `dm:<lo>:<hi>` into its two member ids, or null when malformed or
 * not in canonical form. */
export function parseDMChannel(channel: string): [number, number] | null {
  if (!isDM(channel)) return null;
  const parts = channel.split(":");
  if (parts.length !== 3) return null;
  const a = Number(parts[1]);
  const b = Number(parts[2]);
  // Like the server, accept only the exact spelling dmChannelFor builds.
  if (!Number.isInteger(a) || !Number.isInteger(b) || a <= 0 || a >= b) return null;
  if (dmChannelFor(a, b) !== channel) return null;
  return [a, b];
}

/** Returns the channel's other participant relative to `meId`, or null when
 *  `meId` is not one of its two participants. */
export function dmPeerOf(channel: string | null | undefined, meId: number | null | undefined): number | null {
  if (meId == null) return null;
  const ids = channel ? parseDMChannel(channel) : null;
  if (!ids || (ids[0] !== meId && ids[1] !== meId)) return null;
  return ids[0] === meId ? ids[1] : ids[0];
}

/** Builds the canonical channel id for two users (smaller id first). */
export function dmChannelFor(a: number, b: number): string {
  const lo = Math.min(a, b);
  const hi = Math.max(a, b);
  return `dm:${lo}:${hi}`;
}
