import { expect, test } from "@playwright/test";
import { dismissPushPrompt, login, PASSWORD, USERS } from "./helpers";

async function openSettings(page: import("@playwright/test").Page) {
  await page.getByRole("button", { name: "Settings" }).click();
  await expect(page.getByRole("heading", { name: "Profile" })).toBeVisible();
}

test.describe("settings", () => {
  test("opens with all expected tabs", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await openSettings(page);
    for (const t of ["Profile", "Account", "Notifications", "Data"]) {
      await expect(page.getByRole("button", { name: t, exact: true })).toBeVisible();
    }
    // Alice is not an admin → no Admin tab.
    await expect(page.getByRole("button", { name: "Admin", exact: true })).toHaveCount(0);
  });

  test("admin sees the Admin tab", async ({ page }) => {
    await login(page, USERS.admin);
    await dismissPushPrompt(page);
    await openSettings(page);
    await expect(page.getByRole("button", { name: "Admin", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Admin", exact: true }).click();
    await expect(page.getByRole("link", { name: /admin panel|open admin/i })).toBeVisible();
  });

  test("profile name + bio save persists", async ({ page }) => {
    await login(page, USERS.carol);
    await dismissPushPrompt(page);
    await openSettings(page);
    const name = `Carol ${Date.now() % 10000}`;
    await page.getByPlaceholder("Your name").fill(name);
    await page.getByPlaceholder(/line or two/i).fill("e2e bio");
    await page.getByRole("button", { name: "Save changes" }).click();
    // Reload → the new name shows on the account trigger.
    await page.reload();
    await dismissPushPrompt(page);
    await expect(page.locator(".rail-account").getByText(name)).toBeVisible();
    // Restore.
    await openSettings(page);
    await page.getByPlaceholder("Your name").fill("Carol");
    await page.getByPlaceholder(/line or two/i).fill("");
    await page.getByRole("button", { name: "Save changes" }).click();
  });

  test("password change flow validates + updates", async ({ page }) => {
    await login(page, USERS.carol);
    await dismissPushPrompt(page);
    await openSettings(page);
    await page.getByRole("button", { name: "Account", exact: true }).click();
    // Wrong current password → error.
    await page.getByPlaceholder("Current password").fill("nope-nope-nope");
    await page.getByPlaceholder(/New password/).fill("carol-new-pass-1");
    await page.getByPlaceholder(/Confirm new password/).fill("carol-new-pass-1");
    await page.getByRole("button", { name: "Update password" }).click();
    await expect(page.getByText(/incorrect|wrong|invalid/i).first()).toBeVisible();
    // Correct current → success, then restore the fixture password.
    await page.getByPlaceholder("Current password").fill(PASSWORD);
    await page.getByPlaceholder(/New password/).fill("carol-new-pass-1");
    await page.getByPlaceholder(/Confirm new password/).fill("carol-new-pass-1");
    await page.getByRole("button", { name: "Update password" }).click();
    await expect(page.getByText(/updated|changed|success/i).first()).toBeVisible({
      timeout: 10_000,
    });
    await page.getByPlaceholder("Current password").fill("carol-new-pass-1");
    await page.getByPlaceholder(/New password/).fill(PASSWORD);
    await page.getByPlaceholder(/Confirm new password/).fill(PASSWORD);
    await page.getByRole("button", { name: "Update password" }).click();
    await expect(page.getByText(/updated|changed|success/i).first()).toBeVisible({
      timeout: 10_000,
    });
  });

  test("notifications tab shows the DM-alerts toggle", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await openSettings(page);
    await page.getByRole("button", { name: "Notifications", exact: true }).click();
    await expect(page.getByText("Direct message alerts")).toBeVisible();
  });

  test("data tab links the JSON export", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await openSettings(page);
    await page.getByRole("button", { name: "Data", exact: true }).click();
    const link = page.getByRole("link", { name: /Export/ });
    await expect(link).toBeVisible();
    await expect(link).toHaveAttribute("href", "/api/me/export");
  });

  test("log out returns to the login screen", async ({ page }) => {
    await login(page, USERS.carol);
    await dismissPushPrompt(page);
    await openSettings(page);
    await page.getByRole("button", { name: "Account", exact: true }).click();
    await page.getByRole("button", { name: "Log out" }).click();
    await expect(page.locator("button.submit")).toBeVisible();
  });
});
