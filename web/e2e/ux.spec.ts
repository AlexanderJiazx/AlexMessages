import { expect, test, type Page } from "@playwright/test";
import { dismissPushPrompt, login, loginAs, msgBody, openDM, sendText, USERS } from "./helpers";

/** Distance of the stream's scroll position from its bottom, in px. */
function gapFromBottom(page: Page) {
  return page
    .locator(".stream")
    .evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight);
}

test.describe("stream autoscroll (B1)", () => {
  test("a new message scrolls down at the bottom, not when scrolled up", async ({ browser }) => {
    const a = await loginAs(browser, USERS.alice);
    const b = await loginAs(browser, USERS.bob);
    try {
      // A short window, so the thread overflows with a few messages.
      await a.page.setViewportSize({ width: 1100, height: 520 });
      await openDM(a.page, "Bob");
      await openDM(b.page, "Alice");
      const stamp = Date.now();
      // Make sure the stream has well over a screen of content.
      for (let i = 0; i < 40; i++) {
        const room = await a.page
          .locator(".stream")
          .evaluate((el) => el.scrollHeight - el.clientHeight);
        if (room > 900) break;
        const text = `filler ${stamp}-${i}.`;
        const box = b.page.getByPlaceholder("Message Alice");
        await box.fill(`${text}\nsecond line\nthird line`);
        await box.press("Enter");
        await expect(msgBody(a.page, text)).toHaveCount(1);
      }

      // At the bottom: a new message keeps the stream pinned to the bottom.
      await expect.poll(() => gapFromBottom(a.page)).toBeLessThan(80);
      const atBottom = `at-bottom ${stamp}`;
      await sendText(b.page, "Alice", atBottom);
      await expect(msgBody(a.page, atBottom)).toBeVisible();
      await expect.poll(() => gapFromBottom(a.page)).toBeLessThan(80);

      // Scrolled up to read: a new message must not move the view.
      await a.page.locator(".stream").evaluate((el) => {
        el.scrollTop = el.scrollHeight - el.clientHeight - 500;
      });
      await a.page.waitForTimeout(150);
      const before = await a.page.locator(".stream").evaluate((el) => el.scrollTop);
      const scrolledUp = `scrolled-up ${stamp}`;
      await sendText(b.page, "Alice", scrolledUp);
      await expect(msgBody(a.page, scrolledUp)).toHaveCount(1);
      await a.page.waitForTimeout(150);
      const after = await a.page.locator(".stream").evaluate((el) => el.scrollTop);
      expect(Math.abs(after - before)).toBeLessThanOrEqual(2);
      expect(await gapFromBottom(a.page)).toBeGreaterThan(300);
      await expect(msgBody(a.page, scrolledUp)).not.toBeInViewport();
    } finally {
      await a.ctx.close();
      await b.ctx.close();
    }
  });
});

test.describe("IME composition (B6)", () => {
  test("Enter that confirms an IME composition neither sends nor saves", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await openDM(page, "Bob");
    const box = page.getByPlaceholder("Message Bob");
    const text = `ime ${Date.now()}`;
    await box.fill(text);
    const composingEnter = (sel: string) =>
      page.locator(sel).evaluate((el) => {
        el.dispatchEvent(
          new KeyboardEvent("keydown", {
            key: "Enter",
            code: "Enter",
            isComposing: true,
            bubbles: true,
            cancelable: true,
          }),
        );
      });
    await composingEnter("textarea.input");
    await page.waitForTimeout(300);
    await expect(box).toHaveValue(text);
    await expect(msgBody(page, text)).toHaveCount(0);

    // A plain Enter still sends.
    await box.press("Enter");
    await expect(msgBody(page, text)).toHaveCount(1);
    await expect(box).toHaveValue("");

    // The inline editor: a composing Enter keeps it open and unsaved.
    const row = page.locator(".msg", { hasText: text }).first();
    await row.hover();
    await row.getByTitle("Edit").click();
    const editor = page.getByLabel("Edit message");
    await editor.fill(`${text} changed`);
    await composingEnter("textarea.edit-area");
    await page.waitForTimeout(300);
    await expect(editor).toBeVisible();
    await expect(msgBody(page, `${text} changed`)).toHaveCount(0);
    await page.keyboard.press("Escape");
    await expect(editor).toHaveCount(0);
  });
});

test.describe("layered Escape (B7)", () => {
  test("Escape closes only the topmost dialog", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await page.getByRole("button", { name: "Settings" }).click();
    const settings = page.getByRole("dialog", { name: "Settings" });
    await expect(settings).toBeVisible();
    await page.getByRole("button", { name: "Account", exact: true }).click();
    await page.getByRole("button", { name: "Delete my account…" }).click();
    const del = page.getByRole("dialog", { name: "Delete account" });
    await expect(del).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(del).toHaveCount(0);
    await expect(settings).toBeVisible();
    // Focus went back to the button that opened the dialog.
    await expect(page.getByRole("button", { name: "Delete my account…" })).toBeFocused();

    await page.keyboard.press("Escape");
    await expect(settings).toHaveCount(0);
  });
});

test.describe("stable bubbles (B11)", () => {
  test("the bubble's DOM node survives delivery and an edit", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await openDM(page, "Bob");
    const text = `stable ${Date.now()}`;
    // Record every .msg node added to the stream that carries the text.
    await page.locator(".stream").evaluate((stream, t) => {
      const w = window as unknown as { __added: Element[] };
      w.__added = [];
      new MutationObserver((records) => {
        for (const r of records) {
          for (const n of r.addedNodes) {
            if (n instanceof Element && n.matches(".msg") && (n.textContent || "").includes(t)) {
              w.__added.push(n);
            }
          }
        }
      }).observe(stream, { childList: true, subtree: true });
    }, text);
    await sendText(page, "Bob", text);
    await expect(page.locator(".send-status").last()).toHaveText(/Delivered|Read/);

    const row = page.locator(".msg", { hasText: text }).first();
    await row.hover();
    await row.getByTitle("Edit").click();
    await page.getByLabel("Edit message").fill(`${text} edited`);
    await page.getByRole("button", { name: "Save" }).click();
    await expect(msgBody(page, `${text} edited`)).toBeVisible();
    await expect(page.locator(".msg", { hasText: text }).first().locator(".edited-tag")).toBeVisible();

    const result = await page.evaluate(() => {
      const added = (window as unknown as { __added: Element[] }).__added;
      return { count: added.length, connected: added.length === 1 && added[0].isConnected };
    });
    expect(result).toEqual({ count: 1, connected: true });
  });
});

test.describe("touch-width actions (B8, B10)", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("message actions open with a tap; the rail menu button shows and toggles", async ({ page }) => {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);

    // Rail drawer: the "…" button is visible without hover and toggles.
    await page.getByRole("button", { name: "Open menu" }).click();
    await expect(page.locator(".pane.left.open")).toBeVisible();
    const bobRow = page.locator(".dm-row").filter({ hasText: "Bob" }).first();
    const menuBtn = bobRow.getByLabel("Conversation actions");
    await expect(menuBtn).toHaveCSS("opacity", "1");
    await menuBtn.click();
    await expect(menuBtn).toHaveAttribute("aria-expanded", "true");
    await expect(page.locator(".dm-menu")).toBeVisible();
    // A second click on the same button closes the menu (B10).
    await menuBtn.click();
    await expect(page.locator(".dm-menu")).toHaveCount(0);
    await expect(menuBtn).toHaveAttribute("aria-expanded", "false");

    await bobRow.locator(".dm").click();
    await expect(page.getByPlaceholder("Message Bob")).toBeVisible();
    const text = `touch ${Date.now()}`;
    await sendText(page, "Bob", text);
    await expect(page.locator(".send-status").last()).toHaveText(/Delivered|Read/);

    const row = page.locator(".msg", { hasText: text }).first();
    const actions = row.locator(".actions");
    await expect(actions).toHaveCSS("opacity", "0");
    await row.locator(".body").click();
    await expect(actions).toHaveCSS("opacity", "1");
    await row.getByRole("button", { name: "Reply" }).click();
    await expect(page.locator(".replying-to")).toContainText("Replying to");
    await expect(actions).toHaveCSS("opacity", "0");
  });
});

test.describe("older history paging (B12)", () => {
  /** Opens alice's thread with bob, making sure it has more than one page. */
  async function openLongThread(page: Page) {
    await login(page, USERS.alice);
    await dismissPushPrompt(page);
    await openDM(page, "Bob");
    if ((await page.locator(".history-top").count()) === 0) {
      const box = page.getByPlaceholder("Message Bob");
      for (let i = 0; i < 55; i++) {
        await box.fill(`paging ${Date.now()}-${i}.`);
        await box.press("Enter");
      }
      await expect(page.locator(".send-status").last()).toHaveText(/Delivered|Read/);
      await page.reload();
      await dismissPushPrompt(page);
      await openDM(page, "Bob");
    }
    await expect(page.locator(".history-top")).toHaveCount(1);
  }
  const rows = (page: Page) => page.locator(".stream > .msg, .stream > .call-entry");

  test("scrolling to the top prepends a page and keeps the same messages in view", async ({ page }) => {
    await page.setViewportSize({ width: 1100, height: 700 });
    await openLongThread(page);
    const before = await rows(page).count();
    // The newest page's first message; scroll to the top and measure it in
    // one step, before the older page can land. Its bottom edge is measured:
    // its name line may go once it continues an older run.
    const key = await page.locator(".stream > .msg").first().getAttribute("data-key");
    const same = page.locator(`.stream > .msg[data-key="${key}"]`);
    const bottom0 = await same.evaluate((el) => {
      el.parentElement!.scrollTop = 0;
      return el.getBoundingClientRect().bottom;
    });
    await expect.poll(() => rows(page).count()).toBeGreaterThan(before);
    await page.waitForTimeout(300);
    const bottom1 = await same.evaluate((el) => el.getBoundingClientRect().bottom);
    expect(Math.abs(bottom1 - bottom0)).toBeLessThanOrEqual(4);
  });

  test("a first page that does not fill the stream loads older pages by itself", async ({ page }) => {
    await page.setViewportSize({ width: 1100, height: 700 });
    await openLongThread(page);
    // A window far taller than one page of messages: no scrollbar, so no
    // scroll event can ask for older history.
    await page.setViewportSize({ width: 1600, height: 9000 });
    // The app reopens the last conversation by itself after the reload.
    const paged = page.waitForRequest((r) => r.url().includes("/api/history/"));
    await page.reload();
    await dismissPushPrompt(page);
    await openDM(page, "Bob");
    await paged;
    await expect
      .poll(
        async () => {
          const fills = await page.locator(".stream").evaluate((el) => el.scrollHeight > el.clientHeight);
          const more = (await page.locator(".history-top").count()) > 0;
          return fills || !more;
        },
        { timeout: 15_000 },
      )
      .toBe(true);
    expect(await rows(page).count()).toBeGreaterThan(50);
  });
});
