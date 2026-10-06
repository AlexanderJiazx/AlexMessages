import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient, ApiError, dmStateFromPayload } from "../src/api";

function mockFetch(impl: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>) {
  return vi.fn(impl as typeof fetch);
}

afterEach(() => vi.unstubAllGlobals());

describe("ApiClient", () => {
  it("sends Authorization header when a token is set", async () => {
    const f = mockFetch(async (input, init) => {
      const h = new Headers(init?.headers);
      expect(h.get("Authorization")).toBe("Bearer tok123");
      return new Response(JSON.stringify({ ok: true }), { status: 200 });
    });
    vi.stubGlobal("fetch", f);
    const api = new ApiClient({ baseUrl: "http://x", token: "tok123" });
    await api.getMe();
    expect(f).toHaveBeenCalledOnce();
  });

  it("omits Authorization for cookie mode", async () => {
    const f = mockFetch(async (_input, init) => {
      const h = new Headers(init?.headers);
      expect(h.get("Authorization")).toBeNull();
      return new Response("{}", { status: 200 });
    });
    vi.stubGlobal("fetch", f);
    const api = new ApiClient({ baseUrl: "http://x" });
    await api.getMe();
  });

  it("throws ApiError with the server detail", async () => {
    vi.stubGlobal(
      "fetch",
      mockFetch(async () => new Response(JSON.stringify({ detail: "Invalid username or password" }), { status: 401 })),
    );
    const api = new ApiClient({ baseUrl: "http://x" });
    await expect(api.login("a", "b")).rejects.toMatchObject({
      name: "ApiError",
      status: 401,
      detail: "Invalid username or password",
    });
    await expect(api.login("a", "b")).rejects.toBeInstanceOf(ApiError);
  });

  it("builds history query params", async () => {
    let url = "";
    vi.stubGlobal(
      "fetch",
      mockFetch(async (input) => {
        url = String(input);
        return new Response(JSON.stringify({ messages: [], has_more: false }), { status: 200 });
      }),
    );
    const api = new ApiClient({ baseUrl: "http://x" });
    await api.history("dm:1:2", 500, 50);
    expect(url).toBe("http://x/api/history/dm%3A1%3A2?before=500&limit=50");
  });
});

describe("dmStateFromPayload", () => {
  it("maps snake_case wire fields", () => {
    const st = dmStateFromPayload({
      pinned: true,
      last_read_at: 10,
      cleared_at: 5,
      force_unread: false,
      unread_count: 3,
      peer_last_read_at: 7,
    });
    expect(st).toEqual({ pinned: true, lastReadAt: 10, clearedAt: 5, unreadCount: 3, peerLastReadAt: 7 });
  });

  it("falls back field-wise", () => {
    const st = dmStateFromPayload({ pinned: true }, {
      pinned: false, lastReadAt: 1, clearedAt: 2, unreadCount: 4, peerLastReadAt: 8,
    });
    expect(st.lastReadAt).toBe(1);
    expect(st.pinned).toBe(true);
  });
});
