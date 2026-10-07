import { expect, test } from "@playwright/test";
import { dismissPushPrompt, GIF, login, MP4, openDM, PNG, USERS, wavTone } from "./helpers";

async function attach(page: import("@playwright/test").Page, name: string, mimeType: string, buffer: Buffer) {
  await page.locator('input[type="file"]').setInputFiles({ name, mimeType, buffer });
}

test.describe("media attachments", () => {
  test.beforeEach(async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await openDM(page, "Bob");
  });

  test("image uploads, renders inline, and opens the lightbox", async ({ page }) => {
    await attach(page, "red.png", "image/png", PNG);
    // Staged thumbnail in the composer.
    await expect(page.locator(".pending-thumb img")).toBeVisible();
    await page.getByRole("button", { name: "Send" }).click();
    // The sent message shows the image inline.
    const img = page.locator(".msg .attachments img").last();
    await expect(img).toBeVisible();
    await img.click();
    await expect(page.locator(".lightbox.on")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.locator(".lightbox.on")).toHaveCount(0);
  });

  test("GIF renders inline as an image", async ({ page }) => {
    await attach(page, "dance.gif", "image/gif", GIF);
    await page.getByRole("button", { name: "Send" }).click();
    await expect(page.locator('.msg img[alt="dance.gif"]').last()).toBeVisible();
  });

  test("video attachment renders a player with a name link", async ({ page }) => {
    await attach(page, "clip.mp4", "video/mp4", MP4);
    await page.getByRole("button", { name: "Send" }).click();
    await expect(page.locator("video.att-video").last()).toBeVisible();
    await expect(
      page.locator("a.att-media-name").filter({ hasText: "clip.mp4" }).last(),
    ).toBeVisible();
  });

  test("audio attachment renders an audio player", async ({ page }) => {
    await attach(page, "tone.wav", "audio/wav", wavTone());
    await page.getByRole("button", { name: "Send" }).click();
    await expect(page.locator("audio.att-audio").last()).toBeVisible();
    await expect(page.getByText(/tone\.wav/).last()).toBeVisible();
    // An uploaded audio file is not a voice message.
    const msg = page.locator(".msg").filter({ has: page.locator("audio.att-audio") }).last();
    await expect(msg.locator(".voice-msg")).toHaveCount(0);
  });

  test("generic file renders a download card", async ({ page }) => {
    await attach(page, "notes.txt", "text/plain", Buffer.from("hello e2e"));
    await page.getByRole("button", { name: "Send" }).click();
    await expect(page.locator("a.att-file").filter({ hasText: "notes.txt" }).last()).toBeVisible();
  });

  test("pending attachment can be removed before sending", async ({ page }) => {
    await attach(page, "red.png", "image/png", PNG);
    await expect(page.locator(".pending-thumb")).toHaveCount(1);
    await page.locator(".pending-thumb").getByRole("button", { name: "Remove" }).click();
    await expect(page.locator(".pending-thumb")).toHaveCount(0);
  });

  test("multiple files upload as one message", async ({ page }) => {
    await page.locator('input[type="file"]').setInputFiles([
      { name: "a.png", mimeType: "image/png", buffer: PNG },
      { name: "b.gif", mimeType: "image/gif", buffer: GIF },
    ]);
    await expect(page.locator(".pending-thumb")).toHaveCount(2);
    await page.getByRole("button", { name: "Send" }).click();
    // Both thumbnails land inside a single message's attachments.
    const lastMsg = page.locator(".msg").last();
    await expect(lastMsg.locator(".attachments img")).toHaveCount(2);
  });
});
