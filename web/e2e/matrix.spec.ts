import { expect, test } from "@playwright/test";
import { dismissPushPrompt, login, USERS, msgBody } from "./helpers";

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
});
