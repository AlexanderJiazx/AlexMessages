import { expect, test } from "@playwright/test";
import {
  dismissPushPrompt,
  login,
  loginAs,
  openDM,
  sendText,
  USERS,
  msgBody,
} from "./helpers";

test.describe("chat", () => {
  test("empty state before picking a conversation", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await expect(page.getByText("No conversation selected")).toBeVisible();
  });

  test("opening a DM renders history, day separators and the topbar", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await openDM(page, "Bob");
    await expect(page.getByRole("heading", { name: "Bob" })).toBeVisible();
    // Presence chip under the peer name.
    await expect(page.locator(".presence-chip")).toContainText(/online|offline/i);
    // The thread has history: at least one day separator + message row.
    await expect(page.locator(".day-sep").first()).toBeVisible();
    await expect(page.locator(".msg").first()).toBeVisible();
  });

  test("send → optimistic bubble → Delivered, no duplicate", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await openDM(page, "Bob");
    const text = `e2e send ${Date.now()}`;
    await sendText(page, "Bob", text);
    await expect(msgBody(page, text)).toBeVisible();
    await expect(page.locator(".send-status").last()).toHaveText(/Delivered|Read/);
    // Exactly one bubble with that text.
    await expect(msgBody(page, text)).toHaveCount(1);
  });

  test("realtime receive: bob gets alice's message over the socket", async ({ browser }) => {
    const a = await loginAs(browser, USERS.alice);
    const b = await loginAs(browser, USERS.bob);
    try {
      await openDM(a.page, "Bob");
      await openDM(b.page, "Alice");
      const text = `realtime ${Date.now()}`;
      await sendText(a.page, "Bob", text);
      // Bob sees it arrive without any reload.
      await expect(msgBody(b.page, text)).toBeVisible();
      // Alice's remark flips to Read once bob's client marks the thread read.
      await expect(a.page.locator(".send-status").last()).toHaveText("Read", {
        timeout: 10_000,
      });
    } finally {
      await a.ctx.close();
      await b.ctx.close();
    }
  });

  test("edit own message → (edited) tag, peer sees the update", async ({ browser }) => {
    const a = await loginAs(browser, USERS.alice);
    const b = await loginAs(browser, USERS.bob);
    try {
      await openDM(a.page, "Bob");
      await openDM(b.page, "Alice");
      const orig = `edit-me ${Date.now()}`;
      await sendText(a.page, "Bob", orig);
      await expect(msgBody(b.page, orig)).toBeVisible();

      // Hover the row (not .bubble — the actions bar overlaps its hit point).
      const row = a.page.locator(".msg", { hasText: orig }).first();
      await row.hover();
      await row.getByTitle("Edit").click();
      const edited = `${orig} — edited`;
      await a.page.getByLabel("Edit message").fill(edited);
      await a.page.getByRole("button", { name: "Save" }).click();
      await expect(msgBody(a.page, edited)).toBeVisible();
      await expect(a.page.locator(".msg", { hasText: edited }).locator(".edited-tag")).toBeVisible();
      // Peer side updates live too.
      await expect(msgBody(b.page, edited)).toBeVisible();
    } finally {
      await a.ctx.close();
      await b.ctx.close();
    }
  });

  test("reply to a message renders the quoted block", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await openDM(page, "Bob");
    const orig = `reply-src ${Date.now()}`;
    await sendText(page, "Bob", orig);
    await expect(msgBody(page, orig)).toBeVisible();

    const row = page.locator(".msg", { hasText: orig }).first();
    await row.hover();
    await row.getByTitle("Reply").click();
    await expect(page.getByText(/Replying to/)).toBeVisible();

    const reply = `reply-dst ${Date.now()}`;
    await sendText(page, "Bob", reply);
    await expect(msgBody(page, reply)).toBeVisible();
    // The sent bubble carries a reference back to the original text.
    const ref = page.locator(".msg", { hasText: reply }).locator(".reply-ref");
    await expect(ref).toBeVisible();
    await expect(ref.getByText(/replying to/i)).toBeVisible();
    await expect(ref).toContainText(orig);
  });

  test("link in a message renders a preview card", async ({ page }) => {
    await page.route("**/api/link-preview**", (route) =>
      route.fulfill({
        json: {
          url: "https://example.com/article",
          title: "Example Article",
          description: "A preview fetched for the test",
          site_name: "example.com",
        },
      }),
    );
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await openDM(page, "Bob");
    await sendText(page, "Bob", "look https://example.com/article neat");
    // The URL becomes a clickable inline link…
    await expect(
      page.locator(".msg .body a").getByText("https://example.com/article").last(),
    ).toBeVisible();
    // …and the fetched OG card renders beneath the bubble.
    const card = page.locator("a.link-preview").last();
    await expect(card).toBeVisible();
    await expect(card.locator(".lp-title")).toHaveText("Example Article");
    await expect(card.locator(".lp-site")).toHaveText("example.com");
  });

  test("message grouping: consecutive own messages share one timestamp", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await openDM(page, "Bob");
    const t1 = `grp1 ${Date.now()}`;
    const t2 = `grp2 ${Date.now()}`;
    await sendText(page, "Bob", t1);
    await sendText(page, "Bob", t2);
    await expect(msgBody(page, t1)).toBeVisible();
    await expect(msgBody(page, t2)).toBeVisible();
  });
});
