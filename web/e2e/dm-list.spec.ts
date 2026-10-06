import { expect, test } from "@playwright/test";
import { dismissPushPrompt, login, loginAs, openDM, sendText, USERS, msgBody } from "./helpers";

test.describe("dm list", () => {
  test("pin moves the thread to the Pinned section, unpin restores", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);

    const bobRow = page.locator(".dm-row").filter({ hasText: "Bob" }).first();
    const menuBtn = () => bobRow.getByLabel("Conversation actions");

    // Normalize: if Bob is already pinned, unpin first so the flow is deterministic.
    await menuBtn().click();
    const unpin = page.getByRole("button", { name: "Unpin conversation" });
    if (await unpin.isVisible().catch(() => false)) {
      await unpin.click();
      await expect(page.locator("#leftPane").getByText("Pinned")).toHaveCount(0);
    } else {
      await page.keyboard.press("Escape");
    }

    await menuBtn().click();
    await page.getByRole("button", { name: "Pin conversation" }).click();
    await expect(page.locator("#leftPane").getByText("Pinned")).toBeVisible();

    await menuBtn().click();
    await page.getByRole("button", { name: "Unpin conversation" }).click();
    await expect(page.locator("#leftPane").getByText("Pinned")).toHaveCount(0);
  });

  test("incoming message raises the unread dot; mark read clears it", async ({ browser }) => {
    const a = await loginAs(browser, USERS.alice);
    const c = await loginAs(browser, USERS.carol);
    try {
      await openDM(c.page, "Alice");
      const text = `unread ${Date.now()}`;
      await sendText(c.page, "Alice", text);

      const aliceRow = a.page.locator(".dm-row").filter({ hasText: "Carol" }).first();
      await expect(aliceRow).toHaveClass(/unread/, { timeout: 10_000 });

      await aliceRow.getByLabel("Conversation actions").click();
      await a.page.getByRole("button", { name: /Mark as read/ }).click();
      await expect(aliceRow).not.toHaveClass(/unread/);
    } finally {
      await a.ctx.close();
      await c.ctx.close();
    }
  });

  test("mark as unread flags the thread", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    const row = page.locator(".dm-row").filter({ hasText: "Carol" }).first();
    await row.getByLabel("Conversation actions").click();
    await page.getByRole("button", { name: /Mark as unread/ }).click();
    await expect(row).toHaveClass(/unread/);
    // Restore.
    await row.getByLabel("Conversation actions").click();
    await page.getByRole("button", { name: /Mark as read/ }).click();
    await expect(row).not.toHaveClass(/unread/);
  });

  test("new conversation by username opens a DM", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await page.getByRole("button", { name: "New conversation" }).click();
    await expect(page.getByText("Start a new chat")).toBeVisible();
    await page.getByPlaceholder("username").fill(USERS.bob);
    await page.getByRole("button", { name: "Start chat" }).click();
    await expect(page.getByPlaceholder("Message Bob")).toBeVisible();
  });

  test("delete for me clears the thread history", async ({ page }) => {
    await login(page, USERS.carol);
    await dismissPushPrompt(page);
    await openDM(page, "Alice");
    const text = `delete-me ${Date.now()}`;
    await sendText(page, "Alice", text);
    await expect(msgBody(page, text)).toBeVisible();

    const row = page.locator(".dm-row").filter({ hasText: "Alice" }).first();
    await row.getByLabel("Conversation actions").click();
    await page.getByRole("button", { name: /Delete for me/ }).click();
    // Deleting the active thread resets the main pane.
    await expect(page.getByText("No conversation selected")).toBeVisible();

    // After reload the contact row is back but the cleared history stays hidden.
    await page.reload();
    await dismissPushPrompt(page);
    await openDM(page, "Alice");
    await expect(msgBody(page, text)).toHaveCount(0);
  });
});
