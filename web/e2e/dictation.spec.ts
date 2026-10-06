import { expect, test } from "@playwright/test";
import { dismissPushPrompt, login, openDM, USERS, msgBody } from "./helpers";

const STUB_TEXT = "stubbed dictation transcript";

function stubTranscribe(page: import("@playwright/test").Page) {
  return page.route("**/api/transcribe", async (route) => {
    const req = route.request();
    // The upload must be multipart audio.
    expect(req.headers()["content-type"]).toContain("multipart/form-data");
    await route.fulfill({ json: { text: STUB_TEXT } });
  });
}

test.describe("voice dictation", () => {
  test.beforeEach(async ({ page }) => {
    await stubTranscribe(page);
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await openDM(page, "Bob");
  });

  test("mic button opens the dictation bar with all controls", async ({ page }) => {
    await page.getByRole("button", { name: "Dictate" }).click();
    const bar = page.getByRole("group", { name: "Voice dictation" });
    await expect(bar).toBeVisible();
    await expect(bar.getByRole("button", { name: "Cancel recording" })).toBeVisible();
    await expect(bar.getByRole("button", { name: "Stop and transcribe" })).toBeVisible();
    await expect(bar.getByRole("button", { name: "Send as voice message" })).toBeVisible();
    await expect(bar.getByRole("button", { name: "Transcribe and send" })).toBeVisible();
    // Live waveform bars + a running timer.
    await expect(page.locator(".dict-bar").first()).toBeVisible();
    await expect(page.locator(".dict-time")).toContainText(/^\d+:\d{2}$/);
  });

  test("cancel discards the recording and restores the composer", async ({ page }) => {
    await page.getByRole("button", { name: "Dictate" }).click();
    await expect(page.getByRole("group", { name: "Voice dictation" })).toBeVisible();
    await page.getByRole("button", { name: "Cancel recording" }).click();
    await expect(page.getByRole("group", { name: "Voice dictation" })).toHaveCount(0);
    await expect(page.getByPlaceholder("Message Bob")).toBeVisible();
  });

  test("stop → transcribe fills the composer with the transcript", async ({ page }) => {
    await page.getByRole("button", { name: "Dictate" }).click();
    await expect(page.getByRole("group", { name: "Voice dictation" })).toBeVisible();
    await page.waitForTimeout(1200); // record a beat so the clip isn't empty
    await page.getByRole("button", { name: "Stop and transcribe" }).click();
    await expect(page.getByText("Transcribing…")).toBeVisible();
    await expect(page.getByPlaceholder("Message Bob")).toHaveValue(STUB_TEXT);
  });

  test("transcribe & send posts the transcript as a message", async ({ page }) => {
    await page.getByRole("button", { name: "Dictate" }).click();
    await expect(page.getByRole("group", { name: "Voice dictation" })).toBeVisible();
    await page.waitForTimeout(1200);
    await page.getByRole("button", { name: "Transcribe and send" }).click();
    // .last(): the fixed stub text may also exist in history from a prior run.
    await expect(msgBody(page, STUB_TEXT).last()).toBeVisible();
    await expect(page.locator(".send-status").last()).toHaveText(/Delivered|Read/);
  });

  test("send as voice message attaches the WAV recording", async ({ page }) => {
    await page.getByRole("button", { name: "Dictate" }).click();
    await expect(page.getByRole("group", { name: "Voice dictation" })).toBeVisible();
    await page.waitForTimeout(1200);
    await page.getByRole("button", { name: "Send as voice message" }).click();
    // An audio player lands in the stream for the sent clip.
    await expect(page.locator("audio.att-audio").last()).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText(/voice-message\.wav/).last()).toBeVisible();
  });

  test("waveform animates while recording", async ({ page }) => {
    await page.getByRole("button", { name: "Dictate" }).click();
    const bar = page.getByRole("group", { name: "Voice dictation" });
    await expect(bar).toBeVisible();
    await page.waitForTimeout(1500); // let the fake mic feed the meter
    // At least one bar should have grown above the idle floor (3px).
    const heights = await page.locator(".dict-bar").evaluateAll((els) =>
      els.map((e) => (e as HTMLElement).style.height),
    );
    expect(heights.some((h) => parseInt(h) > 4)).toBeTruthy();
  });
});
