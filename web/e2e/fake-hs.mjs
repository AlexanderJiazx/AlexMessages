/**
 * Minimal fake Matrix homeserver for the e2e suite.
 *
 * Implements just enough of the Client-Server + Media APIs for the appservice
 * bridge to run against (register / createRoom / join / send / receipt /
 * profile / media upload+download), records every call under /__state, and
 * lets the test push inbound transactions into the app with /__inject.
 *
 * Env:
 *   E2E_HS_PORT  — listen port (default 8796)
 *   AM_BASE      — app server base for /__inject (default http://127.0.0.1:8795)
 *   AM_HS_TOKEN  — hs_token for injected txns (default e2e-hs-token)
 */
import http from "node:http";

const PORT = Number(process.env.E2E_HS_PORT || 8796);
const AM_BASE = process.env.AM_BASE || "http://127.0.0.1:8795";
const AM_HS_TOKEN = process.env.AM_HS_TOKEN || "e2e-hs-token";

const state = {
  registers: [],
  createRooms: [],
  joins: [],
  leaves: [],
  sends: [],
  receipts: [],
  uploads: [],
  queries: [],
};
const media = new Map();
// roomId -> Map(mxid -> membership), so /rooms/{id}/members can answer like
// a real HS. Populated by createRoom/join/leave and seedable via /__inject's
// `room_members` field.
const rooms = new Map();

function setMember(roomId, mxid, membership) {
  if (!rooms.has(roomId)) rooms.set(roomId, new Map());
  rooms.get(roomId).set(mxid, membership);
}
let roomSeq = 0;
let eventSeq = 0;
let mediaSeq = 0;

function readBody(req) {
  return new Promise((resolve) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => resolve(Buffer.concat(chunks)));
  });
}

function json(res, code, obj) {
  res.writeHead(code, { "content-type": "application/json" });
  res.end(JSON.stringify(obj));
}

const server = http.createServer(async (req, res) => {
  const u = new URL(req.url, "http://fake-hs");
  const p = u.pathname;
  const raw = await readBody(req);
  let body = {};
  try {
    body = JSON.parse(raw.toString("utf8") || "{}");
  } catch {
    body = {};
  }

  // ---- test control plane ----
  if (p === "/__state") return json(res, 200, state);
  if (p === "/__reset") {
    for (const k of Object.keys(state)) state[k].length = 0;
    media.clear();
    rooms.clear();
    return json(res, 200, { ok: true });
  }
  if (p === "/__inject" && req.method === "POST") {
    // Seed room membership the AS can observe via /rooms/{id}/members.
    for (const [roomId, members] of Object.entries(body.room_members || {})) {
      for (const [mxid, membership] of Object.entries(members)) setMember(roomId, mxid, membership);
    }
    // Seed downloadable blobs: {media: {"mxc://e2e.test/foo": "<base64>"}}
    for (const [mxc, b64] of Object.entries(body.media || {})) {
      media.set(mxc, Buffer.from(b64, "base64"));
    }
    // Push a transaction into the app's AS endpoint, as a real HS would.
    const txn = body.txn || `e2e-${Date.now()}`;
    const r = await fetch(`${AM_BASE}/_matrix/app/v1/transactions/${txn}`, {
      method: "PUT",
      headers: {
        "content-type": "application/json",
        authorization: `Bearer ${AM_HS_TOKEN}`,
      },
      body: JSON.stringify({ events: body.events || [], ephemeral: body.ephemeral || [] }),
    });
    return json(res, r.status, { txn, status: r.status });
  }

  // ---- client-server API used by the bridge ----
  if (p === "/_matrix/client/v3/register" && req.method === "POST") {
    state.registers.push(body.username);
    return json(res, 200, { user_id: `@${body.username}:e2e.test` });
  }
  if (p === "/_matrix/client/v3/createRoom" && req.method === "POST") {
    const room_id = `!room${++roomSeq}:e2e.test`;
    const creator = u.searchParams.get("user_id");
    state.createRooms.push({ room_id, invite: body.invite || [], as_user: creator });
    setMember(room_id, creator, "join");
    for (const mxid of body.invite || []) setMember(room_id, mxid, "invite");
    return json(res, 200, { room_id });
  }
  if (p.startsWith("/_matrix/client/v3/profile/") && req.method === "GET") {
    const mxid = decodeURIComponent(p.slice("/_matrix/client/v3/profile/".length));
    state.queries.push(`profile ${mxid}`);
    const name = mxid.replace(/^@/, "").split(":")[0];
    return json(res, 200, { displayname: `${name} (Matrix)` });
  }
  // Media: MSC3916 authenticated /_matrix/client/v1/media/* plus the legacy
  // /_matrix/media/v3/* fallback path. Seed blobs via /__inject {media:{mxc:base64}}.
  if ((p === "/_matrix/media/v1/upload" || p === "/_matrix/media/v3/upload") && req.method === "POST") {
    const uri = `mxc://e2e.test/m${++mediaSeq}`;
    media.set(uri, raw);
    state.uploads.push({ path: p, filename: u.searchParams.get("filename"), size: raw.length });
    return json(res, 200, { content_uri: uri });
  }
  if (p.startsWith("/_matrix/client/v1/media/download/") || p.startsWith("/_matrix/media/v3/download/")) {
    const parts = p.split("/download/")[1].split("/");
    const data = media.get(`mxc://${parts[0]}/${parts[1]}`);
    if (!data) return json(res, 404, { errcode: "M_NOT_FOUND" });
    res.writeHead(200, { "content-type": "application/octet-stream" });
    return res.end(data);
  }
  if (p.startsWith("/_matrix/client/v3/rooms/")) {
    const rest = decodeURIComponent(p.slice("/_matrix/client/v3/rooms/".length));
    const segs = rest.split("/");
    const roomId = segs[0];
    if (segs[1] === "join" && req.method === "POST") {
      const who = u.searchParams.get("user_id");
      state.joins.push({ roomId, as_user: who });
      setMember(roomId, who, "join");
      return json(res, 200, { room_id: roomId });
    }
    if (segs[1] === "leave" && req.method === "POST") {
      const who = u.searchParams.get("user_id");
      state.leaves.push({ roomId, as_user: who });
      setMember(roomId, who, "leave");
      return json(res, 200, { room_id: roomId });
    }
    if (segs[1] === "members" && req.method === "GET") {
      const chunk = [];
      for (const [mxid, membership] of rooms.get(roomId) || []) {
        chunk.push({
          type: "m.room.member",
          room_id: roomId,
          sender: mxid,
          state_key: mxid,
          content: { membership },
        });
      }
      return json(res, 200, { chunk });
    }
    if (segs[1] === "send" && req.method === "PUT") {
      const event_id = `$e${++eventSeq}`;
      state.sends.push({
        roomId,
        eventType: segs[2],
        txnId: segs[3],
        as_user: u.searchParams.get("user_id"),
        content: body,
      });
      return json(res, 200, { event_id });
    }
    if (segs[1] === "receipt" && req.method === "POST") {
      state.receipts.push({ roomId, kind: segs[2], eventId: segs[3], as_user: u.searchParams.get("user_id") });
      return json(res, 200, {});
    }
  }
  return json(res, 404, { errcode: "M_NOT_FOUND", path: p });
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`[fake-hs] listening on 127.0.0.1:${PORT}`);
});
