import { expect, test } from "@playwright/test";
import { dismissPushPrompt, login, USERS, msgBody } from "./helpers";
import fs from "node:fs";
import path from "node:path";

/**
 * Matrix appservice bridge e2e: the app server runs bridged against
 * web/e2e/fake-hs.mjs (started by start-server.sh). The spec drives the real
 * UI — open a chat by full Matrix ID, send, receive — and asserts the HS side
 * saw the right calls.
 */

const HS = `http://127.0.0.1:${process.env.E2E_HS_PORT || "8796"}`;

async function hsState(): Promise<any> {
  const r = await fetch(`${HS}/__state`);
  return r.json();
}

async function injectEvents(events: any[], extra: any = {}) {
  const r = await fetch(`${HS}/__inject`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ events, ...extra }),
  });
  expect(r.ok).toBeTruthy();
}

test.describe("matrix bridge", () => {
  test("start a chat by Matrix ID and exchange messages both ways", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);

    // New Message → type a full Matrix ID → the bridge offer shows.
    await page.getByRole("button", { name: "New conversation" }).click();
    await expect(page.getByText("Start a new chat")).toBeVisible();
    // A name that can't collide with the seeded local users (Alice/Bob/Carol).
    const mxid = "@mx_dave:e2e.test";
    await page.locator(".card input[type='text']").fill(mxid);
    await expect(page.locator(".mx-offer")).toContainText(mxid);
    await page.getByRole("button", { name: "Start chat" }).click();

    // Chat opens with the Matrix badge in the topbar.
    await expect(page.locator(".topbar .mx-badge")).toHaveText("Matrix");

    // The bridge registered the puppet and created a DM room.
    await expect
      .poll(async () => (await hsState()).createRooms.map((r: any) => r.invite.join(",")))
      .toContain(mxid);
    const st = await hsState();
    const roomId = st.createRooms[0].room_id;
    expect(st.registers).toContain("am_e2e_alice");

    // Outbound: a sent message lands on the HS as the masqueraded puppet.
    const text = `hello matrix ${Date.now()}`;
    await page.locator("textarea").fill(text);
    await page.locator("textarea").press("Enter");
    await expect
      .poll(async () => {
        const s = await hsState();
        const hit = s.sends.find((x: any) => x.content?.body === text);
        return hit ? hit.as_user : null;
      })
      .toBe("@am_e2e_alice:e2e.test");

    // Inbound: a transaction from the HS renders in the open thread.
    const inbound = `from matrix ${Date.now()}`;
    await injectEvents([
      {
        type: "m.room.message",
        sender: mxid,
        room_id: roomId,
        event_id: `$in${Date.now()}`,
        origin_server_ts: Date.now(),
        content: { msgtype: "m.text", body: inbound },
      },
    ]);
    await expect(msgBody(page, inbound)).toBeVisible();
  });

  test("a 1:1 invite becomes a DM; a group invite is declined", async ({ page }) => {
    await login(page, USERS.bob);
    await dismissPushPrompt(page);

    // Inbound invite for a clean 1:1 room — fake-HS reports sender+puppet only.
    const dmRoom = "!dm-room:e2e.test";
    const dmSender = "@mx_erin:e2e.test";
    await injectEvents(
      [
        {
          type: "m.room.member",
          sender: dmSender,
          room_id: dmRoom,
          state_key: "@am_e2e_bob:e2e.test",
          event_id: `$inv-dm-${Date.now()}`,
          content: { membership: "invite", displayname: "Erin" },
        },
        {
          type: "m.room.message",
          sender: dmSender,
          room_id: dmRoom,
          event_id: `$inv-msg-${Date.now()}`,
          origin_server_ts: Date.now(),
          content: { msgtype: "m.text", body: "hi bob, it is erin" },
        },
      ],
      { room_members: { [dmRoom]: { [dmSender]: "join" } } },
    );

    // The new DM shows up in bob's list (display name syncs to the HS
    // profile asynchronously, so assert on the localpart).
    const erinRow = page.locator(".dm-row", { hasText: "mx_erin" });
    await expect(erinRow).toBeVisible();
    await expect(erinRow.locator(".mx-badge")).toHaveText("Matrix");

    // Group invite: the room already contains a third participant — the
    // puppet must join+leave (to verify) and never map the room to a DM.
    const gRoom = "!grp-room:e2e.test";
    const gSender = "@mx_mallory:e2e.test";
    await injectEvents(
      [
        {
          type: "m.room.member",
          sender: gSender,
          room_id: gRoom,
          state_key: "@am_e2e_bob:e2e.test",
          event_id: `$inv-g-${Date.now()}`,
          content: { membership: "invite", displayname: "Mallory" },
        },
      ],
      {
        room_members: {
          [gRoom]: { [gSender]: "join", "@mx_walter:e2e.test": "join" },
        },
      },
    );

    await expect
      .poll(async () => (await hsState()).leaves.map((l: any) => l.roomId))
      .toContain(gRoom);
    // The group sender must NOT appear as a DM in the rail.
    await expect(page.locator(".dm-row", { hasText: "mallory" })).toHaveCount(0);
  });

  // Regression: /uploads/<uid>/../../file attachments over an authenticated
  // WebSocket must be stripped — previously the bridge read and uploaded a
  // file outside data/uploads to the homeserver.
  test("attachment path traversal over WebSocket never reaches the HS", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);

    // Open a real bridged DM so outbound relays to the fake HS.
    const mxid = "@mx_traversal:e2e.test";
    await page.getByRole("button", { name: "New conversation" }).click();
    await page.locator(".card input[type='text']").fill(mxid);
    await page.getByRole("button", { name: "Start chat" }).click();
    await expect
      .poll(async () => (await hsState()).createRooms.map((r: any) => r.room_id))
      .not.toHaveLength(0);

    // One normal text message so the remote user is a DM partner (visible in
    // the WS init payload) and the outbound path is proven working.
    const hello = `ws-harness-check ${Date.now()}`;
    await page.locator("textarea").fill(hello);
    await page.locator("textarea").press("Enter");
    await expect
      .poll(async () => (await hsState()).sends.some((s: any) => s.content?.body === hello))
      .toBe(true);

    // Plant the file the attack targets: inside data/, outside data/uploads.
    const runDir = process.env.E2E_RUN_DIR || "/tmp/am-e2e";
    fs.mkdirSync(path.join(runDir, "data"), { recursive: true });
    fs.writeFileSync(path.join(runDir, "data", "review-marker.txt"), "e2e-marker-content");

    // Authenticated raw WebSocket: same session cookie as the page.
    const cookies = await page.context().cookies();
    const session = cookies.find((c) => c.name === "am_session");
    expect(session).toBeTruthy();
    const ws = new WebSocket(`ws://127.0.0.1:${process.env.E2E_PORT || "8795"}/ws?token=${session!.value}`);
    const init = await new Promise<any>((resolve, reject) => {
      ws.onmessage = (e) => resolve(JSON.parse(String(e.data)));
      ws.onerror = reject;
      setTimeout(() => reject(new Error("ws init timeout")), 5000);
    });
    expect(init.type).toBe("init");
    const me = init.me.id;
    const remote = init.users.find((u: any) => u.matrix_id === mxid);
    expect(remote).toBeTruthy();
    const ch = `dm:${Math.min(me, remote.id)}:${Math.max(me, remote.id)}`;

    // Send a message whose only attachment is a traversal escape.
    ws.send(JSON.stringify({
      type: "message",
      channel: ch,
      text: "",
      attachments: [{
        url: `/uploads/${me}/../../review-marker.txt`,
        name: "review-marker.txt",
        size: 38,
        mime: "text/plain",
      }],
      client_id: "traversal-probe",
    }));
    await page.waitForTimeout(500); // give a bad implementation time to leak

    const st = await hsState();
    expect(st.uploads.filter((u: any) => u.filename === "review-marker.txt")).toHaveLength(0);
    expect(
      st.sends.filter(
        (s: any) => s.content?.filename === "review-marker.txt" || typeof s.content?.url === "string",
      ),
    ).toHaveLength(0);
    ws.close();
  });
});
