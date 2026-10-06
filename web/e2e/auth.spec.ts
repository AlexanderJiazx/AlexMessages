import { expect, test } from "@playwright/test";
import { login, PASSWORD, USERS } from "./helpers";

test.describe("auth", () => {
  test("login page renders the brand + form", async ({ page }) => {
    await page.goto("/login");
    await expect(page.getByRole("heading", { name: "Alex Messages" })).toBeVisible();
    await expect(page.getByLabel("Username")).toBeVisible();
    await expect(page.getByLabel("Password")).toBeVisible();
    await expect(page.locator("button.submit")).toBeVisible();
  });

  test("bad credentials show an error and stay on the form", async ({ page }) => {
    await page.goto("/login");
    await page.getByLabel("Username").fill(USERS.alice);
    await page.getByLabel("Password").fill("wrong-password");
    await page.locator("button.submit").click();
    await expect(page.getByText(/invalid username or password/i)).toBeVisible();
    await expect(page.getByLabel("Username")).toBeVisible();
  });

  test("login transitions into the chat shell", async ({ page }) => {
    await login(page, USERS.alice);
    await expect(page.getByRole("heading", { name: "Messages" }).first()).toBeVisible();
    // Bob + Carol are seeded contacts in the rail.
    await expect(page.locator(".dm-row").filter({ hasText: "Bob" })).toBeVisible();
    await expect(page.locator(".dm-row").filter({ hasText: "Carol" })).toBeVisible();
  });

  test("unauthenticated / boots to the login form", async ({ page }) => {
    await page.goto("/");
    await expect(page.locator("button.submit")).toBeVisible();
  });

  test("register validates and reports pending approval", async ({ page }) => {
    await page.goto("/login");
    await page.getByRole("button", { name: "Register" }).click();
    const name = `e2e_n${Date.now() % 1000000}`;
    await page.getByLabel("Username").fill(name);
    await page.getByLabel("Display name").fill("Newbie");
    await page.getByLabel("Password").fill("newbie-password-1");
    await page.getByRole("button", { name: "Request account" }).click();
    await expect(page.getByText(/approv/i)).toBeVisible({ timeout: 10_000 });
  });
});
