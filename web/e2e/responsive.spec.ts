import { expect, test } from "@playwright/test";
import { dismissPushPrompt, login, sendText, USERS } from "./helpers";

test.describe("responsive layout", () => {
  test.use({ viewport: { width: 390, height: 844 } }); // iPhone 14-ish

  test("phone: hamburger opens the rail drawer, picking a DM closes it", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    // Rail starts off-canvas on mobile.
    await expect(page.locator(".pane.left")).not.toHaveClass(/open/);
    await page.getByRole("button", { name: "Open menu" }).click();
    await expect(page.locator(".pane.left.open")).toBeVisible();
    await page.locator(".dm-row").filter({ hasText: "Bob" }).first().click();
    await expect(page.locator(".pane.left")).not.toHaveClass(/open/);
    await expect(page.getByPlaceholder("Message Bob")).toBeVisible();
  });

  test("phone: backdrop tap closes the rail drawer", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await page.getByRole("button", { name: "Open menu" }).click();
    await expect(page.locator(".pane.left.open")).toBeVisible();
    await page.locator(".backdrop.on").click({ position: { x: 370, y: 400 } });
    await expect(page.locator(".pane.left")).not.toHaveClass(/open/);
  });

  test("phone: send + dictation controls fit the composer", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await page.getByRole("button", { name: "Open menu" }).click();
    await page.locator(".dm-row").filter({ hasText: "Bob" }).first().click();
    await sendText(page, "Bob", `mobile ${Date.now()}`);
    await expect(page.locator(".send-status").last()).toHaveText(/Delivered|Read/);
    await expect(page.getByRole("button", { name: "Dictate" })).toBeVisible();
    await page.getByRole("button", { name: "Dictate" }).click();
    await expect(page.getByRole("group", { name: "Voice dictation" })).toBeVisible();
  });
});
