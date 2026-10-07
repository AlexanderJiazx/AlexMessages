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
  sends: [],
  receipts: [],
  uploads: [],
  queries: [],
};
const media = new Map();
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
    return json(res, 200, { ok: true });
  }
  if (p === "/__inject" && req.method === "POST") {
    // Push a transaction into the app's AS endpoint, as a real HS would.
    const txn = `e2e-${Date.now()}`;
    const r = await fetch(`${AM_BASE}/_matrix/app/v1/transactions/${txn}`, {
      method: "PUT",
      headers: {
        "content-type": "application/json",
        authorization: `Bearer ${AM_HS_TOKEN}`,
      },
      body: JSON.stringify({ events: body.events || [] }),
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
    state.createRooms.push({ room_id, invite: body.invite || [], as_user: u.searchParams.get("user_id") });
    return json(res, 200, { room_id });
  }
  if (p.startsWith("/_matrix/client/v3/profile/") && req.method === "GET") {
    const mxid = decodeURIComponent(p.slice("/_matrix/client/v3/profile/".length));
    state.queries.push(`profile ${mxid}`);
    const name = mxid.replace(/^@/, "").split(":")[0];
    return json(res, 200, { displayname: `${name} (Matrix)` });
  }
  if (p === "/_matrix/media/v3/upload" && req.method === "POST") {
    const uri = `mxc://e2e.test/m${++mediaSeq}`;
    media.set(uri, raw);
    state.uploads.push({ filename: u.searchParams.get("filename"), size: raw.length });
    return json(res, 200, { content_uri: uri });
  }
  if (p.startsWith("/_matrix/media/v3/download/") && req.method === "GET") {
    const parts = p.slice("/_matrix/media/v3/download/".length).split("/");
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
      state.joins.push({ roomId, as_user: u.searchParams.get("user_id") });
      return json(res, 200, { room_id: roomId });
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
