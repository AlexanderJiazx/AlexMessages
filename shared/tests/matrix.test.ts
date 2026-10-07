import { describe, expect, it } from "vitest";
import { isMatrixID, isRemoteUser, parseMatrixID } from "../src/matrix";

describe("parseMatrixID", () => {
  it("parses full Matrix user ids", () => {
    expect(parseMatrixID("@alice:matrix.org")).toEqual({ localpart: "alice", server: "matrix.org" });
    expect(parseMatrixID("@am_bob:hs.local")).toEqual({ localpart: "am_bob", server: "hs.local" });
    expect(parseMatrixID("@u:s:8448")).toEqual({ localpart: "u", server: "s:8448" });
    expect(parseMatrixID("  @alice:matrix.org  ")).toEqual({ localpart: "alice", server: "matrix.org" });
  });

  it("rejects non-ids", () => {
    for (const s of ["alice", "@alice", "alice:matrix.org", "@:srv", "@lp:", "", "@a b:c.d", "@a/b:c.d"]) {
      expect(parseMatrixID(s)).toBeNull();
    }
  });
});

describe("isMatrixID", () => {
  it("distinguishes matrix ids from plain and @-prefixed usernames", () => {
    expect(isMatrixID("@alice:matrix.org")).toBe(true);
    expect(isMatrixID("@bob")).toBe(false); // legacy @username still strips to local
    expect(isMatrixID("bob")).toBe(false);
  });
});

describe("isRemoteUser", () => {
  it("reads matrix_id presence", () => {
    expect(isRemoteUser({ matrix_id: "@alice:matrix.org" })).toBe(true);
    expect(isRemoteUser({ matrix_id: null })).toBe(false);
    expect(isRemoteUser({})).toBe(false); // older servers omit the key
    expect(isRemoteUser(null)).toBe(false);
  });
});
