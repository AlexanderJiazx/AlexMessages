import { chromium, expect, test, type Browser, type Page } from "@playwright/test";
import { dismissPushPrompt, login, loginAs, msgBody, openDM, sendText, USERS } from "./helpers";

// Chromium's fake devices (playwright.config.ts) give every context a test
// camera and a beeping microphone, so these calls carry real WebRTC media
// between two browser contexts over localhost.
const CALL_PERMISSIONS = ["microphone", "camera"];

/** Whether the peer's audio track on the call screen is receiving packets. */
function remoteAudioFlowing(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const video = document.querySelector<HTMLVideoElement>(".call-remote");
    const track = (video?.srcObject as MediaStream | null)?.getAudioTracks()[0];
    return !!track && track.readyState === "live" && !track.muted;
  });
}

/** A browser of the test's own, for launch options the suite's browser
 *  lacks: more than one fake camera, or full Chromium ("chromium" channel),
 *  which shows notifications where the default headless shell denies them. */
function launchBrowser(opts: { cameras?: number; channel?: "chromium" }): Promise<Browser> {
  const fakeDevices = "--use-fake-device-for-media-stream" + (opts.cameras ? `=device-count=${opts.cameras}` : "");
  return chromium.launch({
    channel: opts.channel,
    args: [fakeDevices, "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"],
  });
}

/** The deviceId of the camera behind the self view. */
function selfCamera(page: Page): Promise<string | undefined> {
  return page.evaluate(() => {
    const video = document.querySelector<HTMLVideoElement>(".call-self");
    return (video?.srcObject as MediaStream | null)?.getVideoTracks()[0]?.getSettings().deviceId;
  });
}

/** The tags of the notifications the page's service worker shows. */
function notificationTags(page: Page): Promise<string[]> {
  return page.evaluate(async () => {
    const reg = await navigator.serviceWorker.ready;
    return (await reg.getNotifications()).map((n) => n.tag).sort();
  });
}

test.describe("calls", () => {
  test("video call: ring, answer, media both ways, controls, hang up, thread entry", async ({ browser }) => {
    const a = await loginAs(browser, USERS.alice, CALL_PERMISSIONS);
    const b = await loginAs(browser, USERS.bob, CALL_PERMISSIONS);
    try {
      await openDM(a.page, "Bob");
      await openDM(b.page, "Alice");

      await a.page.getByRole("button", { name: "Start a video call" }).click();
      const callA = a.page.getByRole("dialog", { name: "Call with Bob" });
      await expect(callA.locator(".call-status")).toHaveText("Ringing…");

      const ring = b.page.getByRole("dialog", { name: "Incoming call from Alice" });
      await expect(ring).toContainText("Incoming video call");
      await ring.getByRole("button", { name: "Accept" }).click();
      const callB = b.page.getByRole("dialog", { name: "Call with Alice" });

      // Each side plays the other's camera and microphone.
      await expect(callA).toHaveClass(/has-video/, { timeout: 15_000 });
      await expect(callB).toHaveClass(/has-video/, { timeout: 15_000 });
      await expect.poll(() => remoteAudioFlowing(a.page)).toBe(true);
      await expect.poll(() => remoteAudioFlowing(b.page)).toBe(true);
      await expect(callA.locator(".call-status")).toHaveText(/^0:\d\d$/);
      await expect(callA.locator(".call-self")).toBeVisible();
      await a.page.screenshot({ path: test.info().outputPath("video-call-alice.png") });

      // Muting tells the peer.
      await callA.getByRole("button", { name: "Mute" }).click();
      await expect(callB.getByText("Alice is muted")).toBeVisible();
      await callA.getByRole("button", { name: "Unmute" }).click();
      await expect(callB.getByText("Alice is muted")).toHaveCount(0);

      // With her camera off, Bob sees Alice's avatar instead of her video.
      await callA.getByRole("button", { name: "Turn camera off" }).click();
      await expect(callB).not.toHaveClass(/has-video/);
      await expect(callB.locator(".call-peer-name")).toHaveText("Alice");
      await expect(callB.locator(".call-remote")).toHaveCSS("opacity", "0");
      await expect(callA.locator(".call-self")).toHaveCount(0);
      await b.page.screenshot({ path: test.info().outputPath("camera-off-bob.png") });
      await callA.getByRole("button", { name: "Turn camera on" }).click();
      await expect(callB).toHaveClass(/has-video/, { timeout: 10_000 });

      // Minimized, the call floats over the chat; the composer stays usable.
      await callB.getByRole("button", { name: "Minimize call" }).click();
      await expect(callB).toHaveClass(/mini/);
      await expect(b.page.getByPlaceholder("Message Alice")).toBeVisible();
      await b.page.screenshot({ path: test.info().outputPath("minimized-bob.png") });
      await callB.getByRole("button", { name: "Expand call" }).click();
      await expect(callB).not.toHaveClass(/mini/);

      // The side that hangs up closes at once; the other says why, then closes.
      await callA.getByRole("button", { name: "End call" }).click();
      await expect(callA).toHaveCount(0);
      await expect(callB.locator(".call-status")).toHaveText("Call ended");
      await expect(callB).toHaveCount(0, { timeout: 5_000 });

      // Both threads log the call with its duration, on the caller's side.
      for (const page of [a.page, b.page]) {
        const entry = page.locator(".call-entry").last();
        await expect(entry.locator("b")).toHaveText("Video call");
        await expect(entry.locator(".call-entry-text span")).toHaveText(/^0:\d\d · /);
      }
      await expect(a.page.locator(".call-entry").last()).toHaveClass(/\bme\b/);
      await expect(b.page.locator(".call-entry").last()).toHaveClass(/\bthem\b/);
    } finally {
      await a.ctx.close();
      await b.ctx.close();
    }
  });

  test("declined voice call: the caller sees Declined and both threads log it", async ({ browser }) => {
    const a = await loginAs(browser, USERS.alice, CALL_PERMISSIONS);
    const b = await loginAs(browser, USERS.bob, CALL_PERMISSIONS);
    try {
      await openDM(a.page, "Bob");

      await a.page.getByRole("button", { name: "Start a voice call" }).click();
      const ring = b.page.getByRole("dialog", { name: "Incoming call from Alice" });
      await expect(ring).toContainText("Incoming voice call");
      await b.page.screenshot({ path: test.info().outputPath("incoming-bob.png") });
      await ring.getByRole("button", { name: "Decline" }).click();
      await expect(ring).toHaveCount(0);

      const callA = a.page.getByRole("dialog", { name: "Call with Bob" });
      await expect(callA.locator(".call-status")).toHaveText("Declined");
      await expect(callA).toHaveCount(0, { timeout: 5_000 });

      const entryA = a.page.locator(".call-entry").last();
      await expect(entryA.locator("b")).toHaveText("Voice call");
      await expect(entryA).toContainText("Declined");
      await openDM(b.page, "Alice");
      const entryB = b.page.locator(".call-entry").last();
      await expect(entryB.locator("b")).toHaveText("Voice call");
      await expect(entryB).toContainText("Declined");
      await expect(entryB).not.toHaveClass(/missed/);
    } finally {
      await a.ctx.close();
      await b.ctx.close();
    }
  });

  test("a call the caller cancels is missed: unread row, red entry, call back from it", async ({ browser }) => {
    const a = await loginAs(browser, USERS.alice, CALL_PERMISSIONS);
    const b = await loginAs(browser, USERS.bob, CALL_PERMISSIONS);
    try {
      await openDM(a.page, "Bob");

      await a.page.getByRole("button", { name: "Start a video call" }).click();
      const ring = b.page.getByRole("dialog", { name: "Incoming call from Alice" });
      await expect(ring).toBeVisible();
      await a.page.getByRole("dialog", { name: "Call with Bob" }).getByRole("button", { name: "End call" }).click();
      await expect(ring).toHaveCount(0);

      // Bob was looking at no thread, so Alice's row turns unread.
      const row = b.page.locator(".dm-row").filter({ hasText: "Alice" }).first();
      await expect(row).toHaveClass(/unread/);
      await expect(row).toContainText("Missed video call");
      await expect(a.page.locator(".call-entry").last()).toContainText("Cancelled");

      await openDM(b.page, "Alice");
      const missed = b.page.locator(".call-entry").last();
      await expect(missed).toHaveClass(/missed/);
      await expect(missed.locator("b")).toHaveText("Missed video call");
      await b.page.screenshot({ path: test.info().outputPath("missed-entry-bob.png") });

      // Calling back from the entry rings Alice, who declines.
      await missed.getByRole("button", { name: "Call back" }).click();
      const back = a.page.getByRole("dialog", { name: "Incoming call from Bob" });
      await expect(back).toContainText("Incoming video call");
      await back.getByRole("button", { name: "Decline" }).click();
      await expect(b.page.getByRole("dialog", { name: "Call with Alice" })).toHaveCount(0, { timeout: 5_000 });
      await expect(b.page.locator(".call-entry").last()).toContainText("Declined");
    } finally {
      await a.ctx.close();
      await b.ctx.close();
    }
  });

  test("calling someone with no device online ends as Unavailable", async ({ browser }) => {
    const a = await loginAs(browser, USERS.alice, CALL_PERMISSIONS);
    try {
      await openDM(a.page, "Carol");
      await a.page.getByRole("button", { name: "Start a voice call" }).click();
      const call = a.page.getByRole("dialog", { name: "Call with Carol" });
      await expect(call.locator(".call-status")).toHaveText("Unavailable");
      await expect(call).toHaveCount(0, { timeout: 5_000 });
      const entry = a.page.locator(".call-entry").last();
      await expect(entry.locator("b")).toHaveText("Voice call");
      await expect(entry).toContainText("Unavailable");
    } finally {
      await a.ctx.close();
    }
  });

  test("the profile sheet places calls, and its call buttons wait while a call is live", async ({ browser }) => {
    const a = await loginAs(browser, USERS.alice, CALL_PERMISSIONS);
    const b = await loginAs(browser, USERS.bob, CALL_PERMISSIONS);
    try {
      await openDM(a.page, "Bob");
      await openDM(b.page, "Alice");
      // The profile sheet opens from Bob's avatar on one of his messages, so
      // the newest page of the thread needs one; earlier runs on the same
      // scratch database can push the seeded one out of it.
      const hello = `profile sheet check ${Date.now()}`;
      await sendText(b.page, "Alice", hello);
      await expect(msgBody(a.page, hello)).toBeVisible();
      const sheet = a.page.locator(".sheet-card");
      const openProfile = () => a.page.locator(".msg.them:not(.cont) .av-slot").last().click();

      // Like Message, a call closes the sheet.
      await openProfile();
      await sheet.getByRole("button", { name: "Video call" }).click();
      await expect(sheet).toHaveCount(0);
      const ring = b.page.getByRole("dialog", { name: "Incoming call from Alice" });
      await expect(ring).toContainText("Incoming video call");

      const callA = a.page.getByRole("dialog", { name: "Call with Bob" });
      await callA.getByRole("button", { name: "Minimize call" }).click();
      await openProfile();
      await expect(sheet.getByRole("button", { name: "Voice call" })).toBeDisabled();
      await expect(sheet.getByRole("button", { name: "Video call" })).toBeDisabled();
      await a.page.screenshot({ path: test.info().outputPath("profile-in-call-alice.png") });
      await sheet.getByRole("button", { name: "Close" }).click();

      await ring.getByRole("button", { name: "Decline" }).click();
      await expect(callA).toHaveCount(0, { timeout: 5_000 });
      const entry = a.page.locator(".call-entry").last();
      await expect(entry.locator("b")).toHaveText("Video call");
      await expect(entry).toContainText("Declined");

      await openProfile();
      await expect(sheet.getByRole("button", { name: "Voice call" })).toBeEnabled();
      await sheet.getByRole("button", { name: "Voice call" }).click();
      await expect(sheet).toHaveCount(0);
      await expect(ring).toContainText("Incoming voice call");
      await ring.getByRole("button", { name: "Decline" }).click();
      await expect(callA).toHaveCount(0, { timeout: 5_000 });
    } finally {
      await a.ctx.close();
      await b.ctx.close();
    }
  });

  test("Switch camera moves between two webcams that report no facing mode", async ({ browser }) => {
    // Like desktop webcams, Chromium's fake cameras report no facing mode,
    // so asking for the other facing alone would reopen the same camera.
    const twoCameras = await launchBrowser({ cameras: 2 });
    const a = await loginAs(twoCameras, USERS.alice, CALL_PERMISSIONS);
    const b = await loginAs(browser, USERS.bob, CALL_PERMISSIONS);
    try {
      await openDM(a.page, "Bob");
      await a.page.getByRole("button", { name: "Start a video call" }).click();
      await b.page.getByRole("dialog", { name: "Incoming call from Alice" }).getByRole("button", { name: "Accept" }).click();
      const callA = a.page.getByRole("dialog", { name: "Call with Bob" });
      const callB = b.page.getByRole("dialog", { name: "Call with Alice" });
      await expect(callB).toHaveClass(/has-video/, { timeout: 15_000 });
      const self = callA.locator(".call-self");
      const first = await selfCamera(a.page);
      expect(first).toBeTruthy();

      await callA.getByRole("button", { name: "Switch camera" }).click();
      await expect.poll(() => selfCamera(a.page)).not.toBe(first);
      const second = await selfCamera(a.page);
      expect(second).toBeTruthy();
      // Neither camera faces away from the user, so the self view stays a mirror.
      await expect(self).toHaveClass(/mirrored/);
      await expect(callB).toHaveClass(/has-video/);

      // Turning the camera off and on keeps the camera chosen.
      await callA.getByRole("button", { name: "Turn camera off" }).click();
      await expect(self).toHaveCount(0);
      await callA.getByRole("button", { name: "Turn camera on" }).click();
      await expect.poll(() => selfCamera(a.page)).toBe(second);

      // Switching again comes back around to the first camera.
      await callA.getByRole("button", { name: "Switch camera" }).click();
      await expect.poll(() => selfCamera(a.page)).toBe(first);
      await expect(self).toHaveClass(/mirrored/);

      await callA.getByRole("button", { name: "End call" }).click();
      await expect(callB).toHaveCount(0, { timeout: 5_000 });
    } finally {
      await a.ctx.close();
      await b.ctx.close();
      await twoCameras.close();
    }
  });

  test("logging out during a call ends it at once, with no leave-page prompt", async ({ browser }) => {
    const a = await loginAs(browser, USERS.alice, CALL_PERMISSIONS);
    const b = await loginAs(browser, USERS.bob, CALL_PERMISSIONS);
    try {
      await openDM(a.page, "Bob");
      await openDM(b.page, "Alice");
      await a.page.getByRole("button", { name: "Start a voice call" }).click();
      await b.page.getByRole("dialog", { name: "Incoming call from Alice" }).getByRole("button", { name: "Accept" }).click();
      const callA = a.page.getByRole("dialog", { name: "Call with Bob" });
      const callB = b.page.getByRole("dialog", { name: "Call with Alice" });
      await expect(callA.locator(".call-status")).toHaveText(/^0:\d\d$/, { timeout: 15_000 });

      // "Leave site?" is for a tab closed mid-call, not for logging out.
      const dialogs: string[] = [];
      a.page.on("dialog", (d) => {
        dialogs.push(d.type());
        void d.accept();
      });
      await callA.getByRole("button", { name: "Minimize call" }).click();
      await a.page.getByRole("button", { name: "Settings" }).click();
      await a.page.getByRole("button", { name: "Account", exact: true }).click();
      await a.page.getByRole("button", { name: "Log out" }).click();
      await expect(a.page.locator("button.submit")).toBeVisible();
      expect(dialogs).toEqual([]);

      // Bob's side ends at once, not after the server's 30 s resume grace.
      await expect(callB.locator(".call-status")).toHaveText("Call ended");
      await expect(callB).toHaveCount(0, { timeout: 5_000 });
      await expect(b.page.locator(".call-entry").last().locator("b")).toHaveText("Voice call");
    } finally {
      await a.ctx.close();
      await b.ctx.close();
    }
  });

  test("a ringing call's notification closes once the call is answered or declined", async ({ browser }) => {
    const full = await launchBrowser({ channel: "chromium" });
    const a = await loginAs(browser, USERS.alice, CALL_PERMISSIONS);
    // Bob's second device, for a call answered elsewhere.
    const b2 = await loginAs(browser, USERS.bob, CALL_PERMISSIONS);
    const bCtx = await full.newContext({ permissions: [...CALL_PERMISSIONS, "notifications"] });
    const bPage = await bCtx.newPage();
    // Bob's socket names each call that rings for him.
    const rang: string[] = [];
    bPage.on("websocket", (ws) =>
      ws.on("framereceived", ({ payload }) => {
        const ev = JSON.parse(String(payload)) as { type?: string; call?: { id: string } };
        if (ev.type === "call_incoming" && ev.call) rang.push(ev.call.id);
      }),
    );
    const ring = bPage.getByRole("dialog", { name: "Incoming call from Alice" });
    const callA = a.page.getByRole("dialog", { name: "Call with Bob" });
    /** Alice calls Bob. Once it rings, shows what the server's ringing Web
     *  Push shows (static/sw.js), since the test server reaches no push
     *  service, and returns that notification's tag. */
    const ringBob = async () => {
      const before = rang.length;
      await a.page.getByRole("button", { name: "Start a voice call" }).click();
      await expect(ring).toBeVisible();
      await expect.poll(() => rang.length).toBe(before + 1);
      const tag = `call-${rang.at(-1)}`;
      await bPage.evaluate(async (tag) => {
        const reg = await navigator.serviceWorker.ready;
        await reg.showNotification("Alice", { tag, body: "Incoming voice call", requireInteraction: true });
      }, tag);
      await expect.poll(() => notificationTags(bPage)).toEqual(["am-1", tag].sort());
      return tag;
    };
    try {
      await login(bPage, USERS.bob);
      await dismissPushPrompt(bPage);
      await openDM(a.page, "Bob");
      // An unrelated message notification, which must stay.
      await bPage.evaluate(async () => {
        const reg = await navigator.serviceWorker.ready;
        await reg.showNotification("Alice", { tag: "am-1", body: "hello" });
      });

      // Answered here: the ringing notification closes.
      await ringBob();
      await ring.getByRole("button", { name: "Accept" }).click();
      await expect.poll(() => notificationTags(bPage)).toEqual(["am-1"]);
      await callA.getByRole("button", { name: "End call" }).click();
      await expect(bPage.getByRole("dialog", { name: "Call with Alice" })).toHaveCount(0, { timeout: 5_000 });

      // Declined here: it closes too.
      await ringBob();
      await ring.getByRole("button", { name: "Decline" }).click();
      await expect.poll(() => notificationTags(bPage)).toEqual(["am-1"]);
      await expect(callA).toHaveCount(0, { timeout: 5_000 });

      // Answered on Bob's other device: it closes here as well.
      await ringBob();
      await b2.page.getByRole("dialog", { name: "Incoming call from Alice" }).getByRole("button", { name: "Accept" }).click();
      await expect(ring).toHaveCount(0);
      await expect.poll(() => notificationTags(bPage)).toEqual(["am-1"]);
      await callA.getByRole("button", { name: "End call" }).click();
      await expect(b2.page.getByRole("dialog", { name: "Call with Alice" })).toHaveCount(0, { timeout: 5_000 });

      // Missed: it stays for the server's missed-call push, which replaces
      // it under the same tag.
      const missed = await ringBob();
      await callA.getByRole("button", { name: "End call" }).click();
      await expect(ring).toHaveCount(0);
      await expect(bPage.locator(".dm-row").filter({ hasText: "Alice" }).first()).toContainText("Missed voice call");
      expect(await notificationTags(bPage)).toEqual(["am-1", missed].sort());
    } finally {
      await a.ctx.close();
      await b2.ctx.close();
      await bCtx.close();
      await full.close();
    }
  });
});
