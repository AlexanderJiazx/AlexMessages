/**
 * Matrix user-id helpers shared by the web and mobile clients.
 *
 * A remote user is addressed by a full Matrix ID — `@name:server.org` — typed
 * into the new-chat field. The server resolves it through the appservice
 * bridge; these helpers only decide whether a lookup query is a Matrix ID
 * (send it verbatim) or a local username (strip a leading '@').
 */

/** Loose MXID shape: @localpart:server — server may carry a :port suffix. */
const MXID_RE = /^@([^@\s:/?#]+):([^@\s]+)$/;

export interface ParsedMXID {
  localpart: string;
  server: string;
}

/** parseMatrixID returns the halves of "@name:server" or null. */
export function parseMatrixID(input: string): ParsedMXID | null {
  const m = MXID_RE.exec(input.trim());
  if (!m) return null;
  return { localpart: m[1], server: m[2] };
}

/** isMatrixID reports whether a new-chat query is a full Matrix user id. */
export function isMatrixID(input: string): boolean {
  return parseMatrixID(input) != null;
}

/** isRemoteUser reports whether a PublicUser is a bridged Matrix account. */
export function isRemoteUser(u: { matrix_id?: string | null } | null | undefined): boolean {
  return !!u && typeof u.matrix_id === "string" && u.matrix_id.length > 0;
}
