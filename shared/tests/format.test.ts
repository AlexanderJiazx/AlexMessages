import { describe, expect, it } from "vitest";
import { dmChannelFor, dmPeerOf, isDM, parseDMChannel } from "../src/dm";
import {
  attachmentKind,
  bodySegments,
  colorFor,
  firstUrl,
  fmtSize,
  hash,
  initialsFor,
  lastMessagePreviewFor,
  nameFor,
  PEER_COLORS,
  shade,
} from "../src/format";
import type { PublicUser } from "../src/types";

const users: Record<number, PublicUser> = {
  1: { id: 1, username: "alice", display_name: "Alice A", bio: "", avatar: "" },
  2: { id: 2, username: "bob", display_name: "", bio: "", avatar: "" },
};

describe("dm helpers", () => {
  it("isDM only matches dm: channels", () => {
    expect(isDM("dm:1:2")).toBe(true);
    expect(isDM("general")).toBe(false);
    expect(isDM(null)).toBe(false);
    expect(isDM(undefined)).toBe(false);
  });

  it("parseDMChannel extracts both ids in order", () => {
    expect(parseDMChannel("dm:3:9")).toEqual([3, 9]);
    expect(parseDMChannel("dm:x:y")).toBeNull();
    expect(parseDMChannel("dm:1")).toBeNull();
    expect(parseDMChannel("dm:1:2:3")).toBeNull();
  });

  it("dmChannelFor sorts the ids", () => {
    expect(dmChannelFor(9, 3)).toBe("dm:3:9");
    expect(dmChannelFor(3, 9)).toBe("dm:3:9");
  });

  it("dmPeerOf returns the other participant", () => {
    expect(dmPeerOf("dm:3:9", 3)).toBe(9);
    expect(dmPeerOf("dm:3:9", 9)).toBe(3);
    expect(dmPeerOf("dm:3:9", 5)).toBe(3); // non-member sees first id
    expect(dmPeerOf("dm:3:9", null)).toBeNull();
  });
});

describe("colors & identity", () => {
  it("hash is stable and non-negative", () => {
    expect(hash("42")).toBe(hash("42"));
    expect(hash("42")).toBeGreaterThanOrEqual(0);
  });

  it("colorFor is deterministic and in-palette", () => {
    const c = colorFor(7);
    expect(PEER_COLORS).toContain(c);
    expect(colorFor(7)).toBe(c);
    expect(colorFor(null)).toBe(colorFor(undefined));
  });

  it("shade lightens and darkens within range", () => {
    expect(shade("#000000", 0.5)).toBe("#808080");
    expect(shade("#ffffff", -0.5)).toBe("#808080");
    expect(shade("#4F7A5E", 1)).toBe("#ffffff");
    expect(shade("#4F7A5E", -1)).toBe("#000000");
  });

  it("nameFor falls back to username then Deleted User", () => {
    expect(nameFor(users, 1)).toBe("Alice A");
    expect(nameFor(users, 2)).toBe("bob");
    expect(nameFor(users, 99)).toBe("Deleted User");
    expect(nameFor(users, null)).toBe("Deleted User");
  });

  it("initialsFor derives from display name parts", () => {
    expect(initialsFor(users, 1)).toBe("AA");
    expect(initialsFor(users, 2)).toBe("BO");
    expect(initialsFor(users, null)).toBe("?");
    expect(initialsFor(users, 99)).toBe("?");
  });
});

describe("attachment kinds", () => {
  it("classifies by mime first", () => {
    expect(attachmentKind({ mime: "image/png", name: "a.png" })).toBe("image");
    expect(attachmentKind({ mime: "image/gif", name: "a.gif" })).toBe("image");
    expect(attachmentKind({ mime: "video/mp4", name: "a" })).toBe("video");
    expect(attachmentKind({ mime: "audio/wav", name: "a" })).toBe("audio");
    expect(attachmentKind({ mime: "application/pdf", name: "a.pdf" })).toBe("file");
  });

  it("falls back to extension for unlabeled media", () => {
    expect(attachmentKind({ mime: "application/octet-stream", name: "clip.mp4" })).toBe("video");
    expect(attachmentKind({ mime: "", name: "note.m4a" })).toBe("audio");
    expect(attachmentKind({ mime: "", name: "voice.wav" })).toBe("audio");
    expect(attachmentKind({ mime: "", name: "doc.txt" })).toBe("file");
  });
});

describe("body segments", () => {
  it("splits code spans, links, and newlines", () => {
    const segs = bodySegments("hey `x=1` see https://a.b/c\nbye");
    expect(segs).toEqual([
      { kind: "text", text: "hey " },
      { kind: "code", text: "x=1" },
      { kind: "text", text: " see " },
      { kind: "link", text: "https://a.b/c", href: "https://a.b/c" },
      { kind: "br" },
      { kind: "text", text: "bye" },
    ]);
  });

  it("does not linkify inside code spans", () => {
    const segs = bodySegments("`https://a.b`");
    expect(segs).toEqual([{ kind: "code", text: "https://a.b" }]);
  });

  it("firstUrl finds only the first URL", () => {
    expect(firstUrl("go https://one.example then https://two.example")).toBe("https://one.example");
    expect(firstUrl("no links")).toBeNull();
  });
});

describe("sidebar previews", () => {
  const mk = (uid: number, text: string, ts: number): import("../src/types").HistoryMessage => ({
    id: String(ts),
    type: "message",
    channel: "dm:1:2",
    user_id: uid,
    text,
    reply_to: null,
    created_at: ts,
    edited_at: null,
    attachments: [],
  });

  it("prefixes own messages with You:", () => {
    expect(lastMessagePreviewFor([mk(1, "yo", 1), mk(2, "hi", 2)], 1)).toBe("hi");
    expect(lastMessagePreviewFor([mk(2, "hi", 1), mk(1, "yo", 2)], 1)).toBe("You: yo");
  });

  it("collapses whitespace and truncates", () => {
    const long = "word ".repeat(40);
    const p = lastMessagePreviewFor([mk(2, long, 1)], 1);
    expect(p.length).toBeLessThanOrEqual(80);
    expect(p).not.toContain("\n");
  });

  it("describes attachments", () => {
    const m = mk(2, "", 1);
    m.attachments = [{ name: "v.mp4", url: "/u", size: 1, mime: "video/mp4", width: 0, height: 0 }];
    expect(lastMessagePreviewFor([m], 1)).toBe("Attachment: video");
  });

  it("fmtSize formats bytes", () => {
    expect(fmtSize(500)).toBe("500 B");
    expect(fmtSize(2048)).toBe("2.0 KB");
    expect(fmtSize(3 * 1024 * 1024)).toBe("3.00 MB");
  });
});
