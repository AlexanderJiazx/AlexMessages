(() => {
  // ──────── State ────────
  const LAST_CHANNEL_KEY = "am_last_channel";
  const PUSH_PROMPTED_KEY = "am_push_prompted";   // shown a popup at least once
  const PUSH_DECLINED_KEY = "am_push_declined";   // user dismissed; don't ask again
  const PAGE_SIZE_DEFAULT = 50;
  const state = {
    me: null,                // {id, username, display_name, bio, avatar}
    isAdmin: false,
    activeChannel: null,     // null => "No conversation selected" view
    users: {},               // id -> {id, username, display_name, bio, avatar}
    contacts: new Set(),     // user_id Set
    dmThreads: [],           // [{channel, peer_id}]
    online: new Set(),       // user_id Set
    history: {},             // channel_id -> [msg]
    historyHasMore: {},      // channel_id -> bool (more older messages available)
    historyLoading: {},      // channel_id -> bool (in-flight fetch flag)
    dmState: {},             // channel_id -> {pinned, lastReadAt, clearedAt, unreadCount, peerLastReadAt}
    linkPreviews: {},        // url -> preview object | "none" (negative cache)
    pendingAtt: [],
    replyTo: null,
    sheetView: null,         // null | "peer" (peer profile floating sheet)
    sheetPeerId: null,
    settingsOpen: false,     // Settings overlay visibility
    settingsTab: "profile",  // active Settings tab
    meCreatedAt: 0,          // own account creation epoch (for Account tab)
    pending: {},             // client_id -> {timer, channel} for optimistic sends
    editing: false,
    pageSize: PAGE_SIZE_DEFAULT,
    maxUpload: 20 * 1024 * 1024,
    swReg: null,             // ServiceWorkerRegistration once available
    pushSubscribed: false,
    activeMenuChannel: null, // channel whose three-dot menu is open
  };

  function saveLastChannel(id) {
    try {
      if (id) localStorage.setItem(LAST_CHANNEL_KEY, id);
      else localStorage.removeItem(LAST_CHANNEL_KEY);
    } catch {}
  }
  function loadLastChannel() {
    try { return localStorage.getItem(LAST_CHANNEL_KEY) || null; } catch { return null; }
  }

  // ──────── Debug reporter ────────
  // Streams real-time client actions to the admin debug console over a
  // rate-limited tunnel (POST /api/debug/report). Events are batched and sent
  // on a short timer so we never spam the network or the server.
  const Debug = (() => {
    const SESSION_KEY = "am_dbg_session";
    let session = "anon";
    try {
      session = sessionStorage.getItem(SESSION_KEY) || "";
      if (!session) { session = Math.random().toString(36).slice(2, 10); sessionStorage.setItem(SESSION_KEY, session); }
    } catch {}

    let queue = [];
    let timer = null;
    function flush() {
      timer = null;
      if (!queue.length) return;
      const events = queue.splice(0, 50);
      try {
        fetch("/api/debug/report", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          keepalive: true,
          body: JSON.stringify({ session, events }),
        }).catch(() => {});
      } catch {}
    }
    function log(level, event, message, context) {
      queue.push({ ts_ms: Date.now(), level, event, message: message == null ? "" : String(message), context });
      if (queue.length >= 25) flush();
      else if (!timer) timer = setTimeout(flush, 2000);
    }
    window.addEventListener("error", (e) => log("error", "window.error", e.message, { src: e.filename, line: e.lineno }));
    window.addEventListener("unhandledrejection", (e) => {
      const r = e.reason; log("error", "unhandledrejection", (r && r.message) || String(r));
    });
    window.addEventListener("pagehide", flush);
    return {
      debug: (ev, msg, ctx) => log("debug", ev, msg, ctx),
      info:  (ev, msg, ctx) => log("info", ev, msg, ctx),
      warn:  (ev, msg, ctx) => log("warn", ev, msg, ctx),
      error: (ev, msg, ctx) => log("error", ev, msg, ctx),
    };
  })();
  function channelExists(id) {
    if (!id) return false;
    if (state.dmThreads.find(t => t.channel === id)) return true;
    // DM channels with users we know but haven't messaged yet are also OK
    if (typeof id === "string" && id.startsWith("dm:")) {
      const peer = dmPeerOf(id);
      return peer != null && !!state.users[peer];
    }
    return false;
  }

  // ──────── Helpers ────────
  const $ = (id) => document.getElementById(id);
  const PEER_COLORS = ["#4F7A5E","#7BA17F","#39604A","#A3B581","#6F8A6E","#8FAE92","#5A7773","#94A36B","#5B8A6C","#6A8E5F"];

  // ──────── Icons (Lucide, same set as Alex Meet) ────────
  const ICON_PATHS = {
    user:     '<circle cx="12" cy="8" r="5"/><path d="M20 21a8 8 0 1 0-16 0"/>',
    shield:   '<path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/>',
    bell:     '<path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0"/>',
    database: '<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14a9 3 0 0 0 18 0V5"/><path d="M3 12a9 3 0 0 0 18 0"/>',
    lock:     '<rect width="18" height="11" x="3" y="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
    camera:   '<path d="M14.5 4h-5L7 7H4a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2h-3l-2.5-3z"/><circle cx="12" cy="13" r="3"/>',
    upload:   '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="M17 8l-5-5-5 5"/><path d="M12 3v12"/>',
    trash:    '<path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>',
    logout:   '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><path d="M16 17l5-5-5-5"/><path d="M21 12H9"/>',
    download: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="M7 10l5 5 5-5"/><path d="M12 15V3"/>',
    external: '<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>',
  };
  function icon(name, size = 16, sw = 1.7) {
    const p = ICON_PATHS[name] || "";
    return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="${sw}" stroke-linecap="round" stroke-linejoin="round">${p}</svg>`;
  }

  function hash(s) {
    let h = 0;
    for (let i = 0; i < s.length; i++) h = ((h << 5) - h + s.charCodeAt(i)) | 0;
    return Math.abs(h);
  }
  function colorFor(uid) {
    const key = String(uid ?? "?");
    return PEER_COLORS[hash(key) % PEER_COLORS.length];
  }
  function shade(hex, amt) {
    const c = hex.replace("#",""); const r=parseInt(c.slice(0,2),16),g=parseInt(c.slice(2,4),16),b=parseInt(c.slice(4,6),16);
    const adj = v => Math.max(0, Math.min(255, Math.round(v + (amt<0 ? v*amt : (255-v)*amt))));
    return `#${[adj(r),adj(g),adj(b)].map(v=>v.toString(16).padStart(2,"0")).join("")}`;
  }
  function userFor(uid) {
    if (uid == null) return null;
    return state.users[uid] || null;
  }
  function isDeletedUser(uid) {
    return uid == null || !state.users[uid];
  }
  function nameFor(uid) {
    if (uid == null) return "Deleted User";
    const u = userFor(uid);
    return (u && u.display_name) || (u && u.username) || "Deleted User";
  }
  function handleFor(uid) {
    if (uid == null) return "";
    const u = userFor(uid);
    return u ? "@" + u.username : "";
  }
  function initialsFor(uid) {
    if (isDeletedUser(uid)) return "?";
    const name = nameFor(uid);
    const parts = name.split(/[\s._-]+/).filter(Boolean);
    if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
    return (parts[0] || "?").slice(0,2).toUpperCase();
  }
  function fmtSize(n) {
    if (n < 1024) return n + " B";
    if (n < 1024*1024) return (n/1024).toFixed(1) + " KB";
    return (n/1024/1024).toFixed(2) + " MB";
  }
  function escapeHTML(s) {
    return String(s ?? "").replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]));
  }
  function renderBody(text) {
    return escapeHTML(text)
      .replace(/`([^`\n]+)`/g, "<code>$1</code>")
      .replace(/(https?:\/\/[^\s<]+)/g, url => `<a href="${url}" target="_blank" rel="noopener">${url}</a>`)
      .replace(/\n/g, "<br/>");
  }
  function fmtTime(ts) {
    if (!ts) return "";
    const d = new Date(ts * 1000);
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
  function isoDate(ts) {
    if (!ts) return "";
    return new Date(ts * 1000).toISOString().slice(0, 10);
  }
  function avatarHTML(uid, large = false) {
    const c = colorFor(uid);
    const cls = large ? "av lg" : "av";
    const online = uid != null && state.online.has(uid);
    const u = userFor(uid);
    const face = (u && u.avatar)
      ? `<img class="av-photo" src="${escapeHTML(u.avatar)}" alt="" loading="lazy"/>`
      : `<span>${escapeHTML(initialsFor(uid))}</span>`;
    return `<div class="${cls}" style="background: linear-gradient(135deg, ${c}, ${shade(c,-.18)})" title="${escapeHTML(nameFor(uid))}">
      ${face}
      <span class="stat${online ? "" : " offline"}"></span>
    </div>`;
  }

  // Attachment kind for previews: "image" | "video" | "file".
  function attachmentKind(a) {
    const mime = (a && a.mime) || "";
    if (mime.startsWith("image/")) return "image";
    if (mime.startsWith("video/")) return "video";
    return "file";
  }

  // One-line preview of the latest message in a thread, for the sidebar.
  function lastMessagePreviewFor(channel) {
    const arr = state.history[channel] || [];
    for (let i = arr.length - 1; i >= 0; i--) {
      const m = arr[i];
      if (m.type === "system") continue;
      const mine = state.me && m.user_id === state.me.id;
      const prefix = mine ? "You: " : "";
      if (m.text) return prefix + m.text.replace(/\s+/g, " ").slice(0, 80);
      if (m.attachments && m.attachments.length) {
        return prefix + "Attachment: " + attachmentKind(m.attachments[0]);
      }
      return "";
    }
    return "";
  }

  // Timestamp of the latest message in a thread (0 when empty) — sidebar sort key.
  function lastMessageTSFor(channel) {
    const arr = state.history[channel] || [];
    for (let i = arr.length - 1; i >= 0; i--) {
      if (arr[i].type !== "system") return arr[i].created_at || 0;
    }
    return 0;
  }
  function toast(text, isErr = false) {
    const el = document.createElement("div");
    el.className = "toast" + (isErr ? " err" : "");
    el.textContent = text;
    $("toasts").appendChild(el);
    setTimeout(() => el.remove(), 2200);
  }

  // ──────── Channel/DM helpers ────────
  function isDM(channelId) { return typeof channelId === "string" && channelId.startsWith("dm:"); }
  function dmPeerOf(channelId) {
    if (!isDM(channelId) || !state.me) return null;
    const [, a, b] = channelId.split(":");
    const x = parseInt(a, 10), y = parseInt(b, 10);
    return x === state.me.id ? y : x;
  }
  function dmChannelFor(peerId) {
    if (!state.me) return null;
    const [lo, hi] = [state.me.id, peerId].sort((a, b) => a - b);
    return `dm:${lo}:${hi}`;
  }
  function activeChannelMeta() {
    const peerId = dmPeerOf(state.activeChannel);
    return { kind: "dm", name: nameFor(peerId), peerId };
  }

  function dmStateFor(ch) {
    return state.dmState[ch] || { pinned: false, lastReadAt: 0, clearedAt: 0, unreadCount: 0, peerLastReadAt: 0 };
  }

  function isUnread(ch) {
    return dmStateFor(ch).unreadCount > 0;
  }

  function dmListItems() {
    const seen = new Set();
    const items = [];
    state.dmThreads.forEach(t => {
      if (seen.has(t.channel)) return;
      seen.add(t.channel);
      items.push({ channel: t.channel, peerId: t.peer_id });
    });
    state.contacts.forEach(cid => {
      const ch = dmChannelFor(cid);
      if (!ch || seen.has(ch)) return;
      seen.add(ch);
      items.push({ channel: ch, peerId: cid });
    });
    // Most recent conversation first; empty threads trail, alphabetically.
    items.sort((a, b) => {
      const ta = lastMessageTSFor(a.channel), tb = lastMessageTSFor(b.channel);
      if (ta !== tb) return tb - ta;
      return nameFor(a.peerId).localeCompare(nameFor(b.peerId));
    });
    return items;
  }

  function renderDMItem(it) {
    const st = dmStateFor(it.channel);
    const isActive = it.channel === state.activeChannel;
    const unread = isUnread(it.channel);
    const preview = lastMessagePreviewFor(it.channel);
    const row = document.createElement("div");
    row.className = "dm-row" + (isActive ? " active" : "") + (unread ? " unread" : "");
    row.innerHTML = `
      <button class="dm" data-channel="${escapeHTML(it.channel)}">
        ${avatarHTML(it.peerId)}
        <div class="who">
          <b>${escapeHTML(nameFor(it.peerId))}</b>
          <span class="sub">${escapeHTML(preview)}</span>
        </div>
        <span class="unread-dot" title="Unread messages" aria-hidden="${unread ? "false" : "true"}"></span>
      </button>
      <button class="dm-menu-btn" data-channel="${escapeHTML(it.channel)}" aria-label="Conversation actions" title="Actions">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><circle cx="5" cy="12" r="1.4"/><circle cx="12" cy="12" r="1.4"/><circle cx="19" cy="12" r="1.4"/></svg>
      </button>`;
    row.querySelector(".dm").addEventListener("click", () => switchChannel(it.channel));
    row.querySelector(".dm-menu-btn").addEventListener("click", (e) => {
      e.stopPropagation();
      toggleDmMenu(it.channel, e.currentTarget);
    });
    return row;
  }

  function renderDMs() {
    const all = dmListItems();
    const pinned = all.filter(it => dmStateFor(it.channel).pinned);
    const normal = all.filter(it => !dmStateFor(it.channel).pinned);

    const cont = $("dms");
    cont.innerHTML = "";

    const pinnedSection = $("pinnedSection");
    const pinnedList = $("pinnedList");
    if (pinned.length) {
      pinnedSection.style.display = "";
      pinnedList.innerHTML = "";
      pinned.forEach(it => pinnedList.appendChild(renderDMItem(it)));
    } else {
      pinnedSection.style.display = "none";
      pinnedList.innerHTML = "";
    }
    normal.forEach(it => cont.appendChild(renderDMItem(it)));
  }

  // ──────── DM action menu ────────
  function closeDmMenu() {
    state.activeMenuChannel = null;
    const m = document.getElementById("dmMenu");
    if (m) m.remove();
  }

  function toggleDmMenu(channel, anchorBtn) {
    if (state.activeMenuChannel === channel) { closeDmMenu(); return; }
    closeDmMenu();
    state.activeMenuChannel = channel;
    const st = dmStateFor(channel);
    const unread = isUnread(channel);
    const menu = document.createElement("div");
    menu.id = "dmMenu";
    menu.className = "dm-menu";
    menu.innerHTML = `
      <button data-act="pin">${st.pinned ? "Unpin" : "Pin"} conversation</button>
      <button data-act="${unread ? "read" : "unread"}">${unread ? "Mark as read" : "Mark as unread"}</button>
      <button data-act="delete" class="danger">Delete for me</button>`;
    document.body.appendChild(menu);
    positionDmMenu(menu, anchorBtn);
    menu.addEventListener("click", (e) => {
      const btn = e.target.closest("button[data-act]");
      if (!btn) return;
      const act = btn.getAttribute("data-act");
      closeDmMenu();
      if (act === "pin") togglePin(channel, !st.pinned);
      else if (act === "read") markRead(channel);
      else if (act === "unread") markUnread(channel);
      else if (act === "delete") deleteDm(channel);
    });
  }

  function positionDmMenu(menu, anchor) {
    const r = anchor.getBoundingClientRect();
    menu.style.position = "fixed";
    // Default below the anchor; flip above when there's no room.
    const desired = 8;
    let top = r.bottom + desired;
    let left = r.right - 180;
    if (left < 8) left = 8;
    menu.style.left = left + "px";
    menu.style.top = top + "px";
    // After append we can measure
    requestAnimationFrame(() => {
      const mr = menu.getBoundingClientRect();
      if (mr.bottom > window.innerHeight - 8) {
        menu.style.top = (r.top - mr.height - desired) + "px";
      }
    });
  }

  document.addEventListener("click", (e) => {
    if (!state.activeMenuChannel) return;
    const inMenu = e.target.closest("#dmMenu");
    const inBtn = e.target.closest(".dm-menu-btn");
    if (!inMenu && !inBtn) closeDmMenu();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") closeDmMenu();
  });
  window.addEventListener("resize", closeDmMenu);
  document.addEventListener("scroll", closeDmMenu, true);

  async function togglePin(channel, pinned) {
    const url = `/api/dm/${encodeURIComponent(channel)}/${pinned ? "pin" : "unpin"}`;
    try {
      const r = await fetch(url, { method: "POST", credentials: "same-origin" });
      if (!r.ok) throw new Error();
      const st = dmStateFor(channel);
      state.dmState[channel] = { ...st, pinned };
      renderDMs();
      toast(pinned ? "Pinned" : "Unpinned");
    } catch {
      toast("Couldn't update pin", true);
    }
  }

  async function markRead(channel, opts = {}) {
    try {
      const r = await fetch(`/api/dm/${encodeURIComponent(channel)}/read`, {
        method: "POST", credentials: "same-origin",
      });
      if (!r.ok) throw new Error();
      const j = await r.json();
      const st = dmStateFor(channel);
      state.dmState[channel] = { ...st, lastReadAt: j.last_read_at || Date.now()/1000|0, unreadCount: 0 };
      renderDMs();
    } catch {
      if (!opts.silent) toast("Couldn't mark as read", true);
    }
  }

  async function markUnread(channel) {
    try {
      const r = await fetch(`/api/dm/${encodeURIComponent(channel)}/unread`, {
        method: "POST", credentials: "same-origin",
      });
      if (!r.ok) throw new Error();
      const st = dmStateFor(channel);
      const arr = state.history[channel] || [];
      // Count messages from others as the unread total.
      const myId = state.me?.id;
      const unreadCount = arr.filter(m => m.user_id != null && m.user_id !== myId && m.type !== "system").length || 1;
      state.dmState[channel] = { ...st, lastReadAt: 0, unreadCount };
      renderDMs();
      toast("Marked as unread");
    } catch {
      toast("Couldn't mark as unread", true);
    }
  }

  async function deleteDm(channel) {
    if (!confirm("Delete this conversation for you? The other person will still see it.")) return;
    try {
      const r = await fetch(`/api/dm/${encodeURIComponent(channel)}`, {
        method: "DELETE", credentials: "same-origin",
      });
      if (!r.ok) throw new Error();
      const j = await r.json();
      // Remove the thread from the sidebar; clear local history.
      const cleared = j.cleared_at || (Date.now()/1000|0);
      state.history[channel] = [];
      state.historyHasMore[channel] = false;
      state.dmState[channel] = { pinned: false, lastReadAt: cleared, clearedAt: cleared, unreadCount: 0 };
      const peerId = dmPeerOf(channel);
      // Drop the thread entirely unless this peer is a contact (contacts
      // always have a slot in the sidebar so the user can re-message them).
      state.dmThreads = state.dmThreads.filter(t => t.channel !== channel);
      if (state.activeChannel === channel) {
        state.activeChannel = null;
        saveLastChannel(null);
        renderTopbar();
        renderStream();
      }
      renderDMs();
      toast("Conversation deleted");
    } catch {
      toast("Couldn't delete", true);
    }
  }

  function renderTopbar() {
    const composerWrap = document.querySelector(".composer-wrap");
    const chip = $("chSub");
    if (!state.activeChannel) {
      $("chName").textContent = "Messages";
      chip.style.display = "none";
      $("input").placeholder = "";
      composerWrap?.classList.add("hidden");
      return;
    }
    composerWrap?.classList.remove("hidden");
    const meta = activeChannelMeta();
    $("chName").textContent = meta.name;
    const online = state.online.has(meta.peerId);
    chip.style.display = "";
    $("chSubText").textContent = online ? "online" : "offline";
    chip.querySelector(".cdot").style.background = online ? "var(--sage)" : "var(--faint)";
    $("input").placeholder = `Message ${meta.name}`;
  }

  function switchChannel(id) {
    if (state.activeChannel === id) { closeLeft(); return; }
    closeDmMenu();
    state.activeChannel = id;
    saveLastChannel(id);
    state.replyTo = null;
    renderReplyBar();
    renderDMs();
    renderTopbar();
    renderStream();
    if (id) {
      send({ type: "switch", channel: id });
      // Auto-mark a DM as read when the user opens it.
      if (isDM(id) && isUnread(id)) {
        markRead(id, { silent: true });
      }
    }
    closeLeft();
  }

  function openDM(peerId) {
    if (!state.me || peerId === state.me.id) return;
    const ch = dmChannelFor(peerId);
    // Ensure thread metadata exists locally
    if (!state.dmThreads.find(t => t.channel === ch)) {
      state.dmThreads.push({ channel: ch, peer_id: peerId });
    }
    // Ask server to ensure history (idempotent)
    send({ type: "open_dm", peer_id: peerId });
    switchChannel(ch);
  }

  // The "People" rail is intentionally not rendered: the full user roster is
  // private. Users are only ever surfaced through DM threads, contacts, or
  // messages the viewer can already see. Contact management lives in the
  // peer profile pane on the right.
  // The bottom-left rail trigger mirrors the signed-in account (avatar + name +
  // @handle) and opens Settings.
  function renderAccountTrigger() {
    if (!state.me) return;
    const av = $("accountAvatar");
    if (av) av.innerHTML = avatarHTML(state.me.id);
    const nm = $("accountName");
    if (nm) nm.textContent = state.me.display_name || state.me.username;
    const h = $("accountHandle");
    if (h) h.textContent = "@" + state.me.username;
  }

  async function toggleContact(uid) {
    try {
      if (state.contacts.has(uid)) {
        const r = await fetch(`/api/contacts/${uid}`, { method: "DELETE", credentials: "same-origin" });
        if (!r.ok) throw new Error("remove failed");
        state.contacts.delete(uid);
        toast("Contact removed");
      } else {
        const r = await fetch(`/api/contacts/${uid}`, { method: "POST", credentials: "same-origin" });
        if (!r.ok) throw new Error("add failed");
        state.contacts.add(uid);
        toast("Added to contacts");
      }
      renderDMs();
    } catch (err) {
      toast("Contact update failed", true);
    }
  }

  // ──────── Rendering: stream ────────
  // Incremental: a new message arriving is appended to the existing DOM rather
  // than re-rendering everything. This avoids re-creating <img> nodes for
  // earlier messages on every send, which previously caused all images to
  // briefly disappear and reload.
  function renderEmptyState() {
    const stream = $("stream");
    stream.innerHTML = `
      <div class="empty-state">
        <div class="empty-logo"></div>
        <div class="empty-title serif">No conversation selected</div>
        <div class="empty-sub">Pick a conversation from the list, or start a new one with the write button.</div>
      </div>`;
  }

  function nearBottom(el) {
    return el.scrollHeight - el.scrollTop - el.clientHeight < 80;
  }

  function renderStream() {
    const stream = $("stream");
    stream.innerHTML = "";
    stream.dataset.lastDate = "";
    if (!state.activeChannel) { renderEmptyState(); return; }
    const msgs = state.history[state.activeChannel] || [];
    if (state.historyHasMore[state.activeChannel]) {
      const indicator = document.createElement("div");
      indicator.className = "history-top";
      indicator.id = "historyTop";
      indicator.innerHTML = `<span class="spinner"></span><span>Loading older messages…</span>`;
      stream.appendChild(indicator);
    }
    let prev = null;
    msgs.forEach(m => {
      appendMessageEl(m, prev, msgs);
      if (m.type !== "system") prev = m;
    });
    updateStatusRemarks();
    requestAnimationFrame(() => { stream.scrollTop = stream.scrollHeight; });
  }

  // ──────── Delivery / read remarks ────────
  // Adds the iMessage-style footnotes under own messages:
  //   • every failed send gets its own "Failed to send · Retry" line, and
  //   • the most-recent own message shows a single Sending… / Delivered / Read.
  function updateStatusRemarks() {
    const stream = $("stream");
    stream.querySelectorAll(".read-remark, .send-status").forEach(el => el.remove());
    const ch = state.activeChannel;
    if (!isDM(ch) || !state.me) return;
    const arr = state.history[ch] || [];
    const st = dmStateFor(ch);

    // Failed sends — each retryable on its own.
    arr.forEach(m => {
      if (m.type === "system" || m.user_id !== state.me.id || m._status !== "failed") return;
      const el = document.getElementById("msg-" + m.id);
      if (!el) return;
      const r = document.createElement("div");
      r.className = "send-status failed";
      r.innerHTML = `<span>Failed to send</span><span aria-hidden="true">·</span><button class="retry" type="button">Retry</button>`;
      r.querySelector(".retry").addEventListener("click", () => retrySend(m.client_id || m.id));
      (el.querySelector(".msg-col") || el).appendChild(r);
    });

    // Status under the most-recent message, only when it's ours and not failed.
    let lastOwn = null;
    for (let i = arr.length - 1; i >= 0; i--) {
      if (arr[i].type === "system") continue;
      lastOwn = arr[i].user_id === state.me.id ? arr[i] : null;
      break;
    }
    if (!lastOwn || lastOwn._status === "failed") return;
    let label;
    if (st.peerLastReadAt && (lastOwn.created_at || 0) <= st.peerLastReadAt) label = "Read";
    else if (lastOwn._status === "sending") label = "Sending…";
    else label = "Delivered";
    const el = document.getElementById("msg-" + lastOwn.id);
    if (el) {
      const remark = document.createElement("div");
      remark.className = "send-status";
      remark.textContent = label;
      (el.querySelector(".msg-col") || el).appendChild(remark);
    }
  }

  async function loadOlder(channel) {
    if (!channel) return;
    if (state.historyLoading[channel]) return;
    if (!state.historyHasMore[channel]) return;
    const arr = state.history[channel] || [];
    const oldest = arr.length ? arr[0].created_at : null;
    if (oldest == null) return;
    state.historyLoading[channel] = true;
    const stream = $("stream");
    const prevScrollHeight = stream.scrollHeight;
    const prevScrollTop = stream.scrollTop;
    try {
      const url = `/api/history/${encodeURIComponent(channel)}?before=${encodeURIComponent(oldest)}&limit=${state.pageSize}`;
      const r = await fetch(url, { credentials: "same-origin" });
      if (!r.ok) throw new Error();
      const j = await r.json();
      const older = j.messages || [];
      if (!older.length) {
        state.historyHasMore[channel] = false;
      } else {
        const seenIds = new Set(arr.map(m => m.id));
        const merged = [...older.filter(m => !seenIds.has(m.id)), ...arr];
        state.history[channel] = merged;
        state.historyHasMore[channel] = !!j.has_more;
      }
      if (channel === state.activeChannel) {
        renderStream();
        // Preserve visual scroll position so the user isn't yanked.
        requestAnimationFrame(() => {
          const newHeight = stream.scrollHeight;
          stream.scrollTop = newHeight - prevScrollHeight + prevScrollTop;
        });
      }
    } catch {
      // Leave hasMore=true so the user can retry by scrolling.
    } finally {
      state.historyLoading[channel] = false;
    }
  }

  // Trigger lazy load when the user scrolls near the top of the message stream.
  $("stream").addEventListener("scroll", () => {
    const ch = state.activeChannel;
    if (!ch) return;
    const stream = $("stream");
    if (stream.scrollTop < 120 && state.historyHasMore[ch] && !state.historyLoading[ch]) {
      loadOlder(ch);
    }
  });

  function appendMessageEl(m, prev, allMsgs) {
    // Legacy join/left messages aren't rendered any more.
    if (m.type === "system") return;
    const stream = $("stream");
    const d = isoDate(m.created_at);
    if (d && d !== stream.dataset.lastDate) {
      const sep = document.createElement("div");
      sep.className = "day-sep";
      sep.innerHTML = `<span>${formatDate(d)}</span>`;
      stream.appendChild(sep);
      stream.dataset.lastDate = d;
    }
    const prevForCont = (prev && prev.type !== "system") ? prev : null;
    stream.appendChild(renderMessage(m, prevForCont, allMsgs));
  }

  function formatDate(d) {
    if (!d) return "";
    const today = new Date().toISOString().slice(0,10);
    if (d === today) return "Today";
    const dt = new Date(d + "T00:00:00");
    return dt.toLocaleDateString(undefined, { weekday: "long", month: "short", day: "numeric" });
  }

  function renderMessage(m, prev, allMsgs) {
    const wrap = document.createElement("div");
    const mine = state.me && m.user_id === state.me.id;
    const isCont = prev && prev.type === "message" && prev.user_id === m.user_id && !m.reply_to
                   && Math.abs((m.created_at || 0) - (prev.created_at || 0)) < 5 * 60;
    wrap.className = "msg " + (mine ? "me" : "them") + (isCont ? " cont" : "");
    wrap.id = "msg-" + m.id;
    const c = colorFor(m.user_id);

    let replyHTML = "";
    if (m.reply_to) {
      const target = (allMsgs || []).find(x => x.id === m.reply_to);
      if (target) {
        const tc = colorFor(target.user_id);
        const preview = (target.text || (target.attachments && target.attachments[0] ? `📎 ${target.attachments[0].name}` : ""))
                          .replace(/\s+/g, " ").slice(0, 100);
        replyHTML = `<div class="reply-ref" data-jump="${target.id}">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 14l-5-5 5-5"/><path d="M4 9h9a7 7 0 0 1 7 7v3"/></svg>
          <span>replying to</span>
          <b style="color:${tc}">${escapeHTML(nameFor(target.user_id))}</b>
          <span class="preview">${escapeHTML(preview)}</span>
        </div>`;
      }
    }

    let attachHTML = "";
    if (m.attachments && m.attachments.length) {
      attachHTML = '<div class="attachments">' + m.attachments.map(a => renderAttachment(a)).join("") + '</div>';
    }
    const editedTag = m.edited_at ? `<span class="edited-tag">(edited)</span>` : "";
    const bodyHTML = m.text ? `<div class="body">${renderBody(m.text)}${editedTag}</div>` : "";
    const time = escapeHTML(fmtTime(m.created_at));
    const headBlock = isCont ? "" : (mine
      ? `<div class="head"><span class="time">${time}</span></div>`
      : `<div class="head">
          <b style="color:${c}" data-peer="${m.user_id}">${escapeHTML(nameFor(m.user_id))}</b>
          <span class="time">${time}</span>
        </div>`);
    const avSlot = mine ? "" : (isCont
      ? `<div class="av-slot"><span class="timestamp-gutter">${time}</span></div>`
      : `<div class="av-slot" data-peer="${m.user_id}">${avatarHTML(m.user_id)}</div>`);

    wrap.innerHTML = `
      ${avSlot}
      <div class="msg-col">
        ${headBlock}
        <div class="bubble" title="${time}">
          ${replyHTML}
          ${bodyHTML}
          ${attachHTML}
        </div>
        <div class="link-previews"></div>
      </div>
      <div class="actions">
        <button data-act="reply" title="Reply">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 14l-5-5 5-5"/><path d="M4 9h9a7 7 0 0 1 7 7v3"/></svg>
        </button>
        ${mine && m.text ? `
        <button data-act="edit" title="Edit">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/></svg>
        </button>` : ""}
        <button data-act="copy" title="Copy">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2.5"/><path d="M5 15V6a2 2 0 0 1 2-2h9"/></svg>
        </button>
      </div>`;

    wrap.querySelectorAll("[data-act]").forEach(btn => {
      btn.addEventListener("click", (e) => {
        e.stopPropagation();
        const act = btn.getAttribute("data-act");
        if (act === "reply") { state.replyTo = m; renderReplyBar(); $("input").focus(); }
        if (act === "edit") startEditMessage(m);
        if (act === "copy") {
          navigator.clipboard?.writeText(m.text || "").then(() => toast("Copied")).catch(() => toast("Copy failed", true));
        }
      });
    });
    wrap.querySelectorAll(".reply-ref").forEach(el => {
      el.addEventListener("click", () => jumpTo(el.getAttribute("data-jump")));
    });
    wrap.querySelectorAll("[data-peer]").forEach(el => {
      el.addEventListener("click", (e) => {
        e.stopPropagation();
        const uid = parseInt(el.getAttribute("data-peer"), 10);
        if (!Number.isFinite(uid)) return;
        if (state.me && uid === state.me.id) openProfile("me");
        else openProfile("peer", uid);
      });
    });
    wrap.querySelectorAll(".att-img").forEach(img => {
      img.addEventListener("click", () => openLightbox(img.src));
    });
    wireImagePlaceholders(wrap);
    hydrateLinkPreview(wrap.querySelector(".link-previews"), m);
    return wrap;
  }

  function renderAttachment(a) {
    const isImg = (a.mime || "").startsWith("image/");
    if (isImg) {
      // Reserve the final layout box up front (same constraints as the CSS
      // max sizes), so a loading placeholder occupies exactly the space the
      // image will: no reflow, no pop-in.
      const w = a.width | 0, h = a.height | 0;
      let cls = "att-img-wrap loading", style = "";
      if (w > 0 && h > 0) {
        const scale = Math.min(1, 360 / w, 320 / h);
        const dw = Math.max(1, Math.round(w * scale));
        const dh = Math.max(1, Math.round(h * scale));
        style = `width:${dw}px;aspect-ratio:${dw} / ${dh};`;
      } else {
        cls += " unknown"; // legacy upload without stored dimensions
      }
      return `<div class="${cls}" style="${style}">
        <span class="spinner"></span>
        <img class="att-img" src="${escapeHTML(a.url)}" alt="${escapeHTML(a.name)}" loading="lazy"/>
      </div>`;
    }
    return `<a class="att-file" href="${escapeHTML(a.url)}" target="_blank" rel="noopener" download="${escapeHTML(a.name)}">
      <span class="ico">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/></svg>
      </span>
      <span class="meta"><b>${escapeHTML(a.name)}</b><span>${fmtSize(a.size)}</span></span>
    </a>`;
  }

  // Reveal images once loaded; placeholders keep the reserved box meanwhile.
  function wireImagePlaceholders(scope) {
    scope.querySelectorAll(".att-img-wrap").forEach(wrapEl => {
      const img = wrapEl.querySelector("img");
      if (!img) return;
      const done = (ok) => {
        wrapEl.classList.remove("loading");
        if (wrapEl.classList.contains("unknown")) {
          // No stored dimensions: let the loaded image size itself.
          wrapEl.classList.remove("unknown");
          wrapEl.classList.add("natural");
        }
        if (!ok) wrapEl.classList.add("err");
      };
      if (img.complete && img.naturalWidth > 0) { done(true); return; }
      img.addEventListener("load", () => {
        const stream = $("stream");
        const stick = nearBottom(stream);
        done(true);
        if (stick) requestAnimationFrame(() => { stream.scrollTop = stream.scrollHeight; });
      });
      img.addEventListener("error", () => done(false));
    });
  }

  // ──────── Link previews ────────
  const FIRST_URL_RE = /(https?:\/\/[^\s<]+)/;

  function linkPreviewCardHTML(p) {
    if (!p || (!p.title && !p.image)) return "";
    let host = ""; try { host = new URL(p.url).host; } catch {}
    return `<a class="link-preview" href="${escapeHTML(p.url)}" target="_blank" rel="noopener">
      ${p.image ? `<img class="lp-img" src="${escapeHTML(p.image)}" alt="" loading="lazy"/>` : ""}
      <span class="lp-meta">
        <span class="lp-site">${escapeHTML(p.site_name || host)}</span>
        ${p.title ? `<b class="lp-title">${escapeHTML(p.title)}</b>` : ""}
        ${p.description ? `<span class="lp-desc">${escapeHTML(p.description)}</span>` : ""}
      </span>
    </a>`;
  }

  // Fetches (and caches) the preview for the first URL in a message, then
  // fills the message's link-previews slot.
  function hydrateLinkPreview(slot, m) {
    if (!slot || m.type === "system") return;
    const match = (m.text || "").match(FIRST_URL_RE);
    if (!match) return;
    const url = match[1];
    const cached = state.linkPreviews[url];
    if (cached === "none") return;
    if (cached) { slot.innerHTML = linkPreviewCardHTML(cached); return; }
    fetch(`/api/link-preview?url=${encodeURIComponent(url)}`, { credentials: "same-origin" })
      .then(r => r.ok ? r.json() : null)
      .then(p => {
        if (!p || (!p.title && !p.image)) { state.linkPreviews[url] = "none"; return; }
        state.linkPreviews[url] = p;
        if (!slot.isConnected) return;
        const stream = $("stream");
        const stick = nearBottom(stream);
        slot.innerHTML = linkPreviewCardHTML(p);
        if (stick) requestAnimationFrame(() => { stream.scrollTop = stream.scrollHeight; });
      })
      .catch(() => { /* leave uncached so a later render can retry */ });
  }

  function jumpTo(id) {
    const el = document.getElementById("msg-" + id);
    if (!el) return;
    el.scrollIntoView({ block: "center", behavior: "smooth" });
    el.classList.add("highlight");
    setTimeout(() => el.classList.remove("highlight"), 1400);
  }

  // ──────── Inline message editing ────────
  // Re-renders one message element in place (after an edit or cancel).
  function rerenderMessage(channel, id) {
    if (channel !== state.activeChannel) return;
    const arr = state.history[channel] || [];
    const idx = arr.findIndex(x => x.id === id);
    if (idx < 0) return;
    const el = document.getElementById("msg-" + id);
    if (!el) return;
    let prev = null;
    for (let i = idx - 1; i >= 0; i--) {
      if (arr[i].type !== "system") { prev = arr[i]; break; }
    }
    el.replaceWith(renderMessage(arr[idx], prev, arr));
    updateStatusRemarks();
  }

  // Swaps the bubble content for a textarea with Save/Cancel (own messages only).
  function startEditMessage(m) {
    const el = document.getElementById("msg-" + m.id);
    if (!el || el.querySelector(".edit-area")) return;
    const bubble = el.querySelector(".bubble");
    if (!bubble) return;
    const channel = m.channel || state.activeChannel;
    bubble.classList.add("editing");
    bubble.innerHTML = `
      <textarea class="edit-area" aria-label="Edit message"></textarea>
      <div class="edit-actions">
        <button class="cancel">Cancel</button>
        <button class="save">Save</button>
      </div>`;
    const area = bubble.querySelector(".edit-area");
    area.value = m.text || "";
    const autosize = () => { area.style.height = "auto"; area.style.height = Math.min(200, area.scrollHeight) + "px"; };
    area.addEventListener("input", autosize);
    const finish = () => rerenderMessage(channel, m.id);
    const save = () => {
      const text = area.value.trim();
      if (!text || text === m.text) { finish(); return; }
      send({ type: "edit", id: m.id, text });
      // Optimistic local apply; the server's message_edited broadcast confirms.
      m.text = text;
      m.edited_at = Math.floor(Date.now() / 1000);
      finish();
      renderDMs();
    };
    bubble.querySelector(".save").addEventListener("click", save);
    bubble.querySelector(".cancel").addEventListener("click", finish);
    area.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); save(); }
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); finish(); }
    });
    autosize();
    area.focus();
    area.setSelectionRange(area.value.length, area.value.length);
  }

  // ──────── Reply bar ────────
  function renderReplyBar() {
    const bar = $("replyBar");
    if (!state.replyTo) { bar.style.display = "none"; return; }
    bar.style.display = "flex";
    $("replyName").textContent = nameFor(state.replyTo.user_id);
    const t = state.replyTo.text || (state.replyTo.attachments?.[0]?.name ? `📎 ${state.replyTo.attachments[0].name}` : "");
    $("replyPreview").textContent = "· " + t;
  }
  $("cancelReply").addEventListener("click", () => { state.replyTo = null; renderReplyBar(); });

  // ──────── Pending attachments ────────
  // Images get an iMessage-style thumbnail preview; other files keep the pill.
  function renderPendingAtt() {
    const el = $("pendingAtt");
    if (!state.pendingAtt.length) { el.style.display = "none"; el.innerHTML = ""; return; }
    el.style.display = "flex";
    el.innerHTML = "";
    const removeBtnSVG = `<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>`;
    state.pendingAtt.forEach((a, i) => {
      const isImg = (a.mime || "").startsWith("image/");
      const tag = document.createElement("span");
      if (isImg) {
        tag.className = "pending-thumb";
        tag.innerHTML = `<img src="${escapeHTML(a.url)}" alt="${escapeHTML(a.name)}" title="${escapeHTML(a.name)}"/>
          <button data-i="${i}" aria-label="Remove">${removeBtnSVG}</button>`;
      } else {
        tag.className = "pending-att";
        tag.innerHTML = `<b>${escapeHTML(a.name)}</b><span style="opacity:.7">${fmtSize(a.size)}</span>
          <button data-i="${i}" aria-label="Remove">${removeBtnSVG}</button>`;
      }
      tag.querySelector("button").addEventListener("click", () => {
        state.pendingAtt.splice(i, 1); renderPendingAtt(); updateSendState();
      });
      el.appendChild(tag);
    });
  }

  // ──────── Peer profile floating sheet ────────
  // `openProfile("me")` is routed to the new Settings overlay; "peer" opens the
  // read-only profile sheet.
  function openProfile(view, uid) {
    if (view === "me") { openSettings("profile"); return; }
    state.sheetView = "peer";
    state.sheetPeerId = uid ?? null;
    renderSheet();
    $("profileSheet").classList.add("on");
  }
  function closeSheet() {
    state.sheetView = null;
    state.sheetPeerId = null;
    $("profileSheet").classList.remove("on");
  }
  $("sheetClose").addEventListener("click", closeSheet);
  $("profileSheet").addEventListener("click", (e) => {
    if (e.target === $("profileSheet")) closeSheet();
  });

  function renderSheet() {
    if (state.sheetView !== "peer" || state.sheetPeerId == null) return;
    const title = $("sheetTitle");
    const body = $("sheetBody");
    const uid = state.sheetPeerId;
    const u = userFor(uid) || { id: uid, username: "?", display_name: "?", bio: "" };
    const online = state.online.has(uid);
    const isContact = state.contacts.has(uid);
    title.textContent = "Profile";
    body.innerHTML = `
      <div class="profile-banner"></div>
      <div class="profile-row">
        ${avatarHTML(uid, true)}
        <div class="name-block">
          <div class="name">${escapeHTML(u.display_name)}</div>
        </div>
      </div>
      <div class="field">
        <label>Bio</label>
        <div class="read-only ${u.bio ? "" : "empty"}">${u.bio ? escapeHTML(u.bio) : "No bio yet."}</div>
      </div>
      <div class="btn-row">
        <button class="btn primary" id="dmBtn">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7l9 6 9-6"/><rect x="3" y="5" width="18" height="14" rx="2"/></svg>
          Message
        </button>
        <button class="btn" id="contactBtn">
          ${isContact ? "Remove from contacts" : "Add to contacts"}
        </button>
      </div>
      <div class="field" style="margin-top:18px">
        <label>Account</label>
        <div class="net-card">
          <div class="row"><span class="k">Username</span><span class="v">@${escapeHTML(u.username)}</span></div>
          <div class="row"><span class="k">User ID</span><span class="v">#${u.id}</span></div>
          <div class="row"><span class="k">Status</span><span class="v" style="color:${online ? "var(--sage-deep)" : "var(--muted)"}">${online ? "online" : "offline"}</span></div>
        </div>
      </div>`;
    $("dmBtn").addEventListener("click", () => { closeSheet(); openDM(uid); });
    $("contactBtn").addEventListener("click", () => { toggleContact(uid).then(renderSheet); });
  }

  // ──────── Settings overlay (Apple-style vertical tabs) ────────
  function settingsTabDefs() {
    const tabs = [
      { id: "profile", label: "Profile", icon: "user" },
      { id: "account", label: "Account", icon: "shield" },
      { id: "notifications", label: "Notifications", icon: "bell" },
      { id: "data", label: "Data", icon: "database" },
    ];
    if (state.isAdmin) tabs.push({ id: "admin", label: "Admin", icon: "lock" });
    return tabs;
  }

  function openSettings(tab) {
    if (!state.me) return;
    state.settingsOpen = true;
    state.settingsTab = tab || state.settingsTab || "profile";
    renderSettingsTabs();
    renderSettingsContent();
    $("settingsModal").classList.add("on");
  }
  function closeSettings() {
    state.settingsOpen = false;
    closeAvatarMenu();
    $("settingsSaveBar").classList.remove("on");
    $("settingsModal").classList.remove("on");
  }
  function setSettingsTab(id) {
    state.settingsTab = id;
    renderSettingsTabs();
    renderSettingsContent();
  }
  $("settingsClose").addEventListener("click", closeSettings);
  $("settingsModal").addEventListener("click", (e) => {
    if (e.target === $("settingsModal")) closeSettings();
  });
  // Single Escape handler for both overlays.
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    if (state.settingsOpen) { closeSettings(); return; }
    if (state.sheetView) closeSheet();
  });

  function renderSettingsTabs() {
    const cont = $("settingsTabs");
    if (!cont) return;
    cont.innerHTML = "";
    settingsTabDefs().forEach(t => {
      const btn = document.createElement("button");
      btn.className = "settings-tab" + (t.id === state.settingsTab ? " active" : "");
      btn.innerHTML = `${icon(t.icon, 17)}<span>${t.label}</span>`;
      btn.addEventListener("click", () => setSettingsTab(t.id));
      cont.appendChild(btn);
    });
  }

  function renderSettingsContent() {
    const c = $("settingsContent");
    if (!c || !state.me) return;
    // The Save/Cancel bar only belongs to the Profile tab.
    $("settingsSaveBar").classList.remove("on");
    switch (state.settingsTab) {
      case "profile":       renderProfileTab(c); break;
      case "account":       renderAccountTab(c); break;
      case "notifications": renderNotificationsTab(c); break;
      case "data":          renderDataTab(c); break;
      case "admin":         renderAdminTab(c); break;
      default:              renderProfileTab(c);
    }
  }

  // ---- Profile tab ----
  function renderProfileTab(c) {
    const me = state.me;
    c.innerHTML = `
      <h2 class="settings-h">Profile</h2>
      <div class="avatar-editor">
        ${avatarHTML(me.id, true)}
        <button class="avatar-cam" id="avatarCam" aria-label="Change photo" title="Change photo">${icon("camera", 16, 1.7)}</button>
      </div>
      <input class="profile-name-input" id="pfName" maxlength="40" value="${escapeHTML(me.display_name)}" placeholder="Your name" />
      <div class="settings-group" style="margin-top:18px">
        <div class="settings-group-label">Bio</div>
        <textarea class="bio" id="pfBio" maxlength="280" placeholder="A line or two about you…">${escapeHTML(me.bio || "")}</textarea>
      </div>`;
    const nameEl = $("pfName"), bioEl = $("pfBio");
    const autosize = () => { bioEl.style.height = "auto"; bioEl.style.height = Math.min(220, bioEl.scrollHeight) + "px"; };
    const checkDirty = () => {
      const dirty = nameEl.value.trim() !== (me.display_name || "") || bioEl.value.trim() !== (me.bio || "");
      $("settingsSaveBar").classList.toggle("on", dirty);
    };
    nameEl.addEventListener("input", checkDirty);
    bioEl.addEventListener("input", () => { autosize(); checkDirty(); });
    $("avatarCam").addEventListener("click", openAvatarMenu);
    autosize();
  }

  function saveProfileTab() {
    const nameEl = $("pfName"), bioEl = $("pfBio");
    if (!nameEl || !bioEl) return;
    saveProfile(nameEl.value.trim(), bioEl.value.trim());
  }
  function cancelProfileTab() {
    renderProfileTab($("settingsContent"));
    $("settingsSaveBar").classList.remove("on");
  }
  $("pfSave").addEventListener("click", saveProfileTab);
  $("pfCancel").addEventListener("click", cancelProfileTab);

  // ---- Avatar photo menu (upload / remove) ----
  function closeAvatarMenu() {
    const m = document.getElementById("avatarMenu");
    if (m) m.remove();
  }
  function openAvatarMenu(e) {
    e.stopPropagation();
    closeAvatarMenu();
    // No existing photo → go straight to the file picker.
    if (!state.me.avatar) { $("avatarInput").click(); return; }
    const anchor = e.currentTarget;
    const menu = document.createElement("div");
    menu.id = "avatarMenu";
    menu.className = "avatar-menu";
    menu.innerHTML = `
      <button data-act="upload">${icon("upload", 15)}<span>Upload photo</span></button>
      <button data-act="remove" class="danger">${icon("trash", 15)}<span>Remove photo</span></button>`;
    document.body.appendChild(menu);
    const r = anchor.getBoundingClientRect();
    menu.style.position = "fixed";
    let left = r.right - 170;
    if (left < 8) left = 8;
    menu.style.left = left + "px";
    menu.style.top = (r.bottom + 8) + "px";
    requestAnimationFrame(() => {
      const mr = menu.getBoundingClientRect();
      if (mr.bottom > window.innerHeight - 8) menu.style.top = (r.top - mr.height - 8) + "px";
    });
    menu.addEventListener("click", (ev) => {
      const btn = ev.target.closest("button[data-act]");
      if (!btn) return;
      const act = btn.getAttribute("data-act");
      closeAvatarMenu();
      if (act === "upload") $("avatarInput").click();
      else if (act === "remove") removeAvatar();
    });
  }
  document.addEventListener("click", (e) => {
    if (!document.getElementById("avatarMenu")) return;
    if (!e.target.closest("#avatarMenu") && !e.target.closest("#avatarCam")) closeAvatarMenu();
  });

  // ---- Account tab ----
  function fmtAccountDate(ts) {
    if (!ts) return "—";
    try { return new Date(ts * 1000).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }); }
    catch { return "—"; }
  }
  function renderAccountTab(c) {
    const me = state.me;
    c.innerHTML = `
      <h2 class="settings-h">Account</h2>
      <div class="settings-group">
        <div class="settings-group-label">Account info</div>
        <div class="net-card">
          <div class="row"><span class="k">Username</span><span class="v">@${escapeHTML(me.username)}</span></div>
          <div class="row"><span class="k">User ID</span><span class="v">#${me.id}</span></div>
          <div class="row"><span class="k">Member since</span><span class="v">${escapeHTML(fmtAccountDate(state.meCreatedAt))}</span></div>
          <div class="row"><span class="k">Status</span><span class="v" style="color:var(--sage-deep)">approved</span></div>
        </div>
      </div>
      <div class="settings-group">
        <div class="settings-group-label">Change password</div>
        <div class="pw-form">
          <input type="password" id="pwCurrent" placeholder="Current password" autocomplete="current-password"/>
          <input type="password" id="pwNew" placeholder="New password (8–128 chars)" autocomplete="new-password"/>
          <input type="password" id="pwConfirm" placeholder="Confirm new password" autocomplete="new-password"/>
          <div id="pwMsg" class="settings-sub" style="margin:2px 0 0"></div>
          <div class="btn-row"><button class="btn primary" id="pwSubmit">Update password</button></div>
        </div>
      </div>
      <div class="settings-group">
        <div class="settings-group-label">Session</div>
        ${rowHTML("Log out from all of your devices", "Ends every active session, including this one.",
          `<button class="btn" id="acLogout">${icon("logout", 14)} Log out</button>`)}
      </div>
      <div class="settings-group">
        <div class="settings-group-label">Danger zone</div>
        <div class="danger-zone">
          <div class="dz-title">Delete account</div>
          <div class="dz-desc">Permanently deletes your account, profile, and message history. This can't be undone.</div>
          <button class="btn danger" id="acDelete">Delete my account…</button>
        </div>
      </div>`;
    $("pwSubmit").addEventListener("click", submitPasswordChange);
    $("acLogout").addEventListener("click", doLogout);
    $("acDelete").addEventListener("click", openDeleteAccount);
  }

  async function submitPasswordChange() {
    const cur = $("pwCurrent").value, nw = $("pwNew").value, cf = $("pwConfirm").value;
    const msg = $("pwMsg");
    const setMsg = (t, ok) => { msg.textContent = t; msg.style.color = ok ? "var(--sage-deep)" : "var(--danger)"; };
    if (nw.length < 8 || nw.length > 128) { setMsg("New password must be 8–128 characters.", false); return; }
    if (nw !== cf) { setMsg("New passwords don't match.", false); return; }
    try {
      const r = await fetch("/api/me/password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ current_password: cur, new_password: nw }),
      });
      if (!r.ok) {
        const j = await r.json().catch(() => null);
        setMsg((j && j.detail) || "Couldn't update password.", false);
        return;
      }
      $("pwCurrent").value = ""; $("pwNew").value = ""; $("pwConfirm").value = "";
      setMsg("Password updated. Other devices were signed out.", true);
      toast("Password updated");
    } catch {
      setMsg("Couldn't update password.", false);
    }
  }

  // ---- Delete-account modal ----
  function openDeleteAccount() {
    $("delPassword").value = "";
    $("delError").style.display = "none";
    $("deleteAccountModal").classList.add("on");
    setTimeout(() => $("delPassword").focus(), 0);
  }
  function closeDeleteAccount() { $("deleteAccountModal").classList.remove("on"); }
  async function confirmDeleteAccount() {
    const pw = $("delPassword").value;
    const err = $("delError");
    err.style.display = "none";
    if (!pw) { err.textContent = "Enter your password."; err.style.display = "block"; return; }
    try {
      const r = await fetch("/api/me/delete", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ password: pw }),
      });
      if (!r.ok) {
        const j = await r.json().catch(() => null);
        err.textContent = (j && j.detail) || "Couldn't delete account.";
        err.style.display = "block";
        return;
      }
      window.location.href = "/login";
    } catch {
      err.textContent = "Couldn't delete account.";
      err.style.display = "block";
    }
  }
  $("delCancel").addEventListener("click", closeDeleteAccount);
  $("delConfirm").addEventListener("click", confirmDeleteAccount);
  $("deleteAccountModal").addEventListener("click", (e) => {
    if (e.target === $("deleteAccountModal")) closeDeleteAccount();
  });
  $("delPassword").addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); confirmDeleteAccount(); }
  });

  // ---- Notifications tab ----
  function renderNotificationsTab(c) {
    c.innerHTML = `<h2 class="settings-h">Notifications</h2>` + pushSettingsBody();
    const t = $("notifToggle");
    if (t && !t.disabled) {
      t.addEventListener("click", () => {
        if (t.classList.contains("on")) disablePushFlow();
        else enablePushFlow();
      });
    }
  }
  function pushSettingsBody() {
    if (!("Notification" in window) || !("serviceWorker" in navigator) || !("PushManager" in window)) {
      return rowHTML("Direct message alerts", "Push notifications aren't supported in this browser.", "");
    }
    const perm = Notification.permission;
    const enabled = state.pushSubscribed && perm === "granted";
    let desc, disabled = false;
    if (perm === "denied") {
      desc = `Blocked by your browser. Open the site settings (click the lock icon in the address bar) to allow notifications.`;
      disabled = true;
    } else if (enabled) {
      desc = `You'll get a system notification for new direct messages while Alex Messages isn't open or focused.`;
    } else {
      desc = `Get a system notification for new direct messages even when Alex Messages is in the background.`;
    }
    const toggle = `<button class="toggle${enabled ? " on" : ""}" id="notifToggle" role="switch"
      aria-checked="${enabled}" aria-label="Toggle notifications"${disabled ? " disabled" : ""}></button>`;
    return rowHTML("Direct message alerts", desc, toggle);
  }

  // rowHTML builds a label-left / control-right setting row: a primary line, a
  // dimmed description on the panel background, and a control on the right.
  function rowHTML(title, desc, control) {
    return `
      <div class="settings-row">
        <div class="settings-row-label">
          <div class="t">${escapeHTML(title)}</div>
          ${desc ? `<div class="d">${escapeHTML(desc)}</div>` : ""}
        </div>
        ${control}
      </div>`;
  }

  // ---- Data tab ----
  function renderDataTab(c) {
    c.innerHTML = `<h2 class="settings-h">Data</h2>` + rowHTML(
      "Export my data",
      "Download a copy of your account, contacts, and full message history as a JSON file.",
      `<a class="btn" id="exportBtn" href="/api/me/export" download>${icon("download", 14)} Export</a>`
    );
  }

  // ---- Admin tab ----
  function renderAdminTab(c) {
    c.innerHTML = `
      <h2 class="settings-h">Admin</h2>
      <div class="settings-sub" style="margin-top:0">You have administrator access. The control panel opens in a new tab.</div>
      <div class="btn-row"><a class="btn primary" href="${adminUrl()}" target="_blank" rel="noopener">${icon("external", 14)} Open admin panel</a></div>`;
  }

  // ──────── Profile photo upload ────────
  $("avatarInput").addEventListener("change", async (e) => {
    const file = e.target.files && e.target.files[0];
    e.target.value = "";
    if (!file) return;
    if (file.size > 5 * 1024 * 1024) { toast("Photo exceeds 5MB", true); return; }
    try {
      const fd = new FormData();
      fd.append("file", file);
      const r = await fetch("/api/me/avatar", { method: "POST", credentials: "same-origin", body: fd });
      if (!r.ok) {
        const j = await r.json().catch(() => null);
        toast((j && j.detail) || "Couldn't update photo", true);
        return;
      }
      const j = await r.json();
      applyOwnProfile(j.user);
      toast("Photo updated");
    } catch {
      toast("Couldn't update photo", true);
    }
  });

  async function removeAvatar() {
    try {
      const r = await fetch("/api/me/avatar", { method: "DELETE", credentials: "same-origin" });
      if (!r.ok) throw new Error();
      const j = await r.json();
      applyOwnProfile(j.user);
      toast("Photo removed");
    } catch {
      toast("Couldn't remove photo", true);
    }
  }

  // applyOwnProfile merges a fresh own profile into state and re-renders.
  function applyOwnProfile(user) {
    state.me = { ...state.me, ...user };
    state.users[state.me.id] = { ...(state.users[state.me.id] || {}), ...user };
    renderAccountTrigger();
    if (state.sheetView === "peer") renderSheet();
    if (state.settingsOpen && state.settingsTab === "profile") renderSettingsContent();
    renderDMs();
    renderStream();
    renderTopbar();
  }

  function adminUrl() {
    const port = location.port === "8765" ? "8001" : "8001"; // dev mapping; admin defaults to 8001
    return `${location.protocol}//${location.hostname}:${port}/`;
  }

  async function saveProfile(name, bio) {
    try {
      const r = await fetch("/api/me", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ display_name: name, bio }),
      });
      if (!r.ok) throw new Error();
      const j = await r.json();
      $("settingsSaveBar").classList.remove("on");
      applyOwnProfile(j.user);
      toast("Profile saved");
    } catch {
      toast("Save failed", true);
    }
  }

  async function doLogout() {
    try { await fetch("/api/logout", { method: "POST", credentials: "same-origin" }); } catch {}
    window.location.href = "/login";
  }

  // ──────── New-DM modal ────────
  $("newDmBtn").addEventListener("click", openNewDmModal);
  $("newDmCancel").addEventListener("click", closeNewDmModal);
  $("newDmModal").addEventListener("click", (e) => {
    if (e.target === $("newDmModal")) closeNewDmModal();
  });
  function openNewDmModal() {
    $("newDmInput").value = "";
    $("newDmError").style.display = "none";
    $("newDmModal").classList.add("on");
    setTimeout(() => $("newDmInput").focus(), 0);
  }
  function closeNewDmModal() { $("newDmModal").classList.remove("on"); }
  function showNewDmError(msg) {
    const el = $("newDmError");
    el.textContent = msg;
    el.style.display = "block";
  }
  async function submitNewDm() {
    const raw = $("newDmInput").value.trim();
    if (!raw) return;
    // Strip a leading @ if the user typed one.
    const username = raw.replace(/^@/, "");
    $("newDmError").style.display = "none";
    try {
      const r = await fetch(`/api/users/lookup?username=${encodeURIComponent(username)}`, { credentials: "same-origin" });
      if (r.status === 404) { showNewDmError("No user with that username."); return; }
      if (r.status === 400) { showNewDmError("That's your own username."); return; }
      if (!r.ok) { showNewDmError("Lookup failed. Try again."); return; }
      const j = await r.json();
      const u = j.user;
      state.users[u.id] = { ...(state.users[u.id] || {}), ...u };
      closeNewDmModal();
      openDM(u.id);
    } catch {
      showNewDmError("Lookup failed. Try again.");
    }
  }
  $("newDmSubmit").addEventListener("click", submitNewDm);
  $("newDmInput").addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); submitNewDm(); }
    if (e.key === "Escape") { e.preventDefault(); closeNewDmModal(); }
  });

  // ──────── Mobile panels ────────
  function openLeft()  { $("leftPane").classList.add("open"); $("backdrop").classList.add("on"); }
  function closeLeft() { $("leftPane").classList.remove("open"); $("backdrop").classList.remove("on"); }
  $("menuBtn").addEventListener("click", openLeft);
  $("leftClose").addEventListener("click", closeLeft);
  $("accountTrigger").addEventListener("click", () => openSettings("profile"));
  $("backdrop").addEventListener("click", closeLeft);

  // ──────── Lightbox ────────
  function openLightbox(src) {
    $("lightboxImg").src = src;
    $("lightbox").classList.add("on");
  }
  $("lightbox").addEventListener("click", () => $("lightbox").classList.remove("on"));

  // ──────── Composer ────────
  const input = $("input");
  function updateSendState() {
    $("sendBtn").disabled = !(input.value.trim() || state.pendingAtt.length);
  }
  input.addEventListener("input", () => {
    input.style.height = "auto";
    input.style.height = Math.min(160, input.scrollHeight) + "px";
    updateSendState();
  });
  input.addEventListener("focus", () => $("composer").classList.add("focused"));
  input.addEventListener("blur",  () => $("composer").classList.remove("focused"));
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); doSend(); }
    if (e.key === "Escape" && state.replyTo) { state.replyTo = null; renderReplyBar(); }
  });
  $("sendBtn").addEventListener("click", doSend);

  // How long to wait for the server's broadcast echo before flagging a send as
  // failed (so the user gets a Retry).
  const SEND_TIMEOUT_MS = 10000;

  // doSend renders the message optimistically (immediately), then sends it with
  // a client-side nonce. The server echoes that nonce in its broadcast so we
  // can reconcile the local bubble; until then the footnote reads "Sending…",
  // flipping to "Delivered" on echo or "Failed to send" after the timeout.
  function doSend() {
    if (!state.activeChannel) return;
    const text = input.value.trim();
    if (!text && !state.pendingAtt.length) return;
    const channel = state.activeChannel;
    const cid = "c" + Math.random().toString(36).slice(2, 10) + Date.now().toString(36);
    const optimistic = {
      id: cid,
      client_id: cid,
      type: "message",
      channel,
      user_id: state.me.id,
      text,
      reply_to: state.replyTo ? state.replyTo.id : null,
      attachments: state.pendingAtt.slice(),
      created_at: Math.floor(Date.now() / 1000),
      edited_at: null,
      _status: "sending",
    };

    const arr = state.history[channel] = state.history[channel] || [];
    const prev = arr.length ? arr[arr.length - 1] : null;
    arr.push(optimistic);
    while (arr.length > 200) arr.shift();
    // Surface a new (or previously deleted) DM thread.
    if (isDM(channel) && !state.dmThreads.find(t => t.channel === channel)) {
      const peer = dmPeerOf(channel);
      if (peer != null) state.dmThreads.push({ channel, peer_id: peer });
    }
    if (channel === state.activeChannel) {
      const stream = $("stream");
      appendMessageEl(optimistic, prev && prev.type !== "system" ? prev : null, arr);
      updateStatusRemarks();
      requestAnimationFrame(() => { stream.scrollTop = stream.scrollHeight; });
    }
    renderDMs();

    send({
      type: "message",
      channel,
      text,
      reply_to: optimistic.reply_to,
      attachments: optimistic.attachments,
      client_id: cid,
    });
    state.pending[cid] = { channel, timer: setTimeout(() => markSendFailed(channel, cid), SEND_TIMEOUT_MS) };

    input.value = "";
    input.style.height = "auto";
    state.pendingAtt = [];
    state.replyTo = null;
    renderPendingAtt();
    renderReplyBar();
    updateSendState();
  }

  function findOptimistic(channel, cid) {
    const arr = state.history[channel] || [];
    return arr.find(m => m.client_id === cid);
  }

  function markSendFailed(channel, cid) {
    const p = state.pending[cid];
    if (p && p.timer) clearTimeout(p.timer);
    if (state.pending[cid]) state.pending[cid] = { channel, timer: null };
    const m = findOptimistic(channel, cid);
    if (!m) return;
    m._status = "failed";
    if (channel === state.activeChannel) { rerenderMessage(channel, m.id); }
    renderDMs();
  }

  function retrySend(cid) {
    const p = state.pending[cid];
    const channel = p ? p.channel : state.activeChannel;
    const m = findOptimistic(channel, cid);
    if (!m) return;
    m._status = "sending";
    if (channel === state.activeChannel) rerenderMessage(channel, m.id);
    send({
      type: "message",
      channel,
      text: m.text,
      reply_to: m.reply_to,
      attachments: m.attachments,
      client_id: cid,
    });
    state.pending[cid] = { channel, timer: setTimeout(() => markSendFailed(channel, cid), SEND_TIMEOUT_MS) };
  }

  // ──────── File upload ────────
  $("attachBtn").addEventListener("click", () => $("fileInput").click());
  $("fileInput").addEventListener("change", async (e) => {
    const files = [...e.target.files];
    e.target.value = "";
    for (const f of files) {
      if (f.size > state.maxUpload) { toast(`${f.name} exceeds 20MB`, true); continue; }
      await uploadOne(f);
    }
    updateSendState();
  });

  async function uploadOne(file) {
    $("uploadStatus").textContent = `Uploading ${file.name}…`;
    $("composerMeta").classList.add("active");
    try {
      const fd = new FormData();
      fd.append("file", file);
      const res = await fetch("/api/upload", { method: "POST", credentials: "same-origin", body: fd });
      if (!res.ok) throw new Error(await res.text());
      const j = await res.json();
      state.pendingAtt.push({
        name: j.name, url: j.url, size: j.size, mime: j.mime,
        width: j.width || 0, height: j.height || 0,
      });
      renderPendingAtt();
    } catch (err) {
      toast(`Upload failed: ${file.name}`, true);
    } finally {
      $("uploadStatus").textContent = "";
      $("composerMeta").classList.remove("active");
    }
  }

  // Drag & drop into composer
  const composer = $("composer");
  ["dragenter","dragover"].forEach(ev => composer.addEventListener(ev, (e) => {
    e.preventDefault(); composer.classList.add("focused");
  }));
  ["dragleave","drop"].forEach(ev => composer.addEventListener(ev, (e) => {
    e.preventDefault(); composer.classList.remove("focused");
  }));
  composer.addEventListener("drop", async (e) => {
    e.preventDefault();
    const files = [...(e.dataTransfer?.files || [])];
    for (const f of files) {
      if (f.size > state.maxUpload) { toast(`${f.name} exceeds 20MB`, true); continue; }
      await uploadOne(f);
    }
    updateSendState();
  });

  // ──────── WebSocket ────────
  let ws;
  let wsReady = false;
  let pendingSend = [];
  // Reconnect bookkeeping: the "Reconnecting…" banner only appears once the
  // socket has stayed down for >3s, so a quick blip doesn't flash a toast.
  let reconnectAttemptTimer = null;
  let reconnBannerTimer = null;
  let connBannerEl = null;

  function showConnBanner() {
    if (connBannerEl) return;
    connBannerEl = document.createElement("div");
    connBannerEl.className = "conn-banner";
    connBannerEl.innerHTML = `<span class="conn-spinner"></span><span>Reconnecting…</span>`;
    $("toasts").appendChild(connBannerEl);
  }
  function clearConnBanner() {
    if (reconnBannerTimer) { clearTimeout(reconnBannerTimer); reconnBannerTimer = null; }
    if (connBannerEl) { connBannerEl.remove(); connBannerEl = null; }
  }

  function connect() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${location.host}/ws`);
    ws.addEventListener("open", () => {
      wsReady = true;
      clearConnBanner();
      Debug.info("ws_open", "WebSocket connected", { queued: pendingSend.length });
      pendingSend.forEach(p => ws.send(JSON.stringify(p)));
      pendingSend = [];
    });
    ws.addEventListener("message", (e) => {
      let data; try { data = JSON.parse(e.data); } catch { return; }
      onMessage(data);
    });
    ws.addEventListener("close", (e) => {
      wsReady = false;
      if (e.code === 4401) {
        Debug.warn("ws_close", "Session expired (4401), redirecting to login");
        window.location.href = "/login";
        return;
      }
      Debug.warn("ws_close", "WebSocket closed, reconnecting", { code: e.code });
      // Arm the banner once; it only shows if we're still down after 3s.
      if (!reconnBannerTimer && !connBannerEl) {
        reconnBannerTimer = setTimeout(() => { reconnBannerTimer = null; showConnBanner(); }, 3000);
      }
      // Schedule a single reconnect attempt.
      if (!reconnectAttemptTimer) {
        reconnectAttemptTimer = setTimeout(() => { reconnectAttemptTimer = null; connect(); }, 1500);
      }
    });
    ws.addEventListener("error", () => Debug.error("ws_error", "WebSocket error"));
  }
  function send(payload) {
    Debug.debug("ws_send", payload && payload.type, payload && payload.channel ? { channel: payload.channel } : undefined);
    if (wsReady) ws.send(JSON.stringify(payload));
    else pendingSend.push(payload);
  }

  function onMessage(data) {
    if (data.type === "init") {
      state.me = data.me;
      state.users = {};
      (data.users || []).forEach(u => state.users[u.id] = u);
      if (state.me) state.users[state.me.id] = state.me;
      state.contacts = new Set(data.contacts || []);
      state.dmThreads = data.dm_threads || [];
      state.online = new Set(data.online || []);
      state.history = data.history || {};
      state.historyHasMore = data.history_has_more || {};
      state.historyLoading = {};
      state.pageSize = data.page_size || PAGE_SIZE_DEFAULT;
      state.maxUpload = data.max_upload || state.maxUpload;
      Debug.info("init", "Session initialized", { users: (data.users || []).length, threads: (data.dm_threads || []).length });
      state.dmState = {};
      Object.entries(data.dm_state || {}).forEach(([ch, s]) => {
        state.dmState[ch] = {
          pinned: !!s.pinned,
          lastReadAt: s.last_read_at || 0,
          clearedAt: s.cleared_at || 0,
          unreadCount: s.unread_count || 0,
          peerLastReadAt: s.peer_last_read_at || 0,
        };
      });
      const saved = loadLastChannel();
      state.activeChannel = channelExists(saved) ? saved : null;
      fetch("/api/me", { credentials: "same-origin" })
        .then(r => r.ok ? r.json() : null)
        .then(j => {
          state.isAdmin = !!(j && j.is_admin);
          state.meCreatedAt = (j && j.created_at) || 0;
          if (state.settingsOpen) { renderSettingsTabs(); renderSettingsContent(); }
        })
        .catch(() => {});
      renderAccountTrigger();
      renderDMs();
      renderTopbar();
      renderStream();
      if (state.sheetView === "peer") renderSheet();
      if (state.settingsOpen) renderSettingsContent();
      if (state.activeChannel) {
        send({ type: "switch", channel: state.activeChannel });
        if (isDM(state.activeChannel) && isUnread(state.activeChannel)) {
          markRead(state.activeChannel, { silent: true });
        }
      }
      reconcilePushOnLoad();
    } else if (data.type === "message") {
      const msg = data.message;
      if (msg.author && msg.author.id != null) {
        state.users[msg.author.id] = { ...(state.users[msg.author.id] || {}), ...msg.author };
      }
      // Reconcile our own optimistic bubble (matched by the echoed client_id)
      // rather than appending a duplicate.
      if (msg.client_id && state.pending[msg.client_id] && state.me && msg.user_id === state.me.id) {
        const cid = msg.client_id;
        const p = state.pending[cid];
        if (p && p.timer) clearTimeout(p.timer);
        delete state.pending[cid];
        const arr = state.history[data.channel] || [];
        const m = arr.find(x => x.client_id === cid);
        if (m) {
          const oldId = m.id;
          m.id = msg.id;
          m.created_at = msg.created_at;
          m.edited_at = msg.edited_at ?? null;
          m._status = "delivered";
          const el = document.getElementById("msg-" + oldId);
          if (el && data.channel === state.activeChannel) {
            el.id = "msg-" + msg.id;
            rerenderMessage(data.channel, msg.id);
          }
          renderDMs();
          return;
        }
        // Optimistic bubble already evicted (rare) — fall through to append.
      }
      let presenceChanged = false;
      if (msg.user_id != null && !state.online.has(msg.user_id)) {
        state.online.add(msg.user_id);
        presenceChanged = true;
      }
      const arr = state.history[data.channel] = state.history[data.channel] || [];
      const wasFull = arr.length >= 200;
      arr.push(msg);
      while (arr.length > 200) arr.shift();
      // Surface a new (or previously deleted) DM thread.
      if (isDM(data.channel) && !state.dmThreads.find(t => t.channel === data.channel)) {
        const peer = dmPeerOf(data.channel);
        if (peer != null) state.dmThreads.push({ channel: data.channel, peer_id: peer });
      }
      // Maintain unread state for DMs.
      if (isDM(data.channel) && msg.user_id != null && state.me && msg.user_id !== state.me.id) {
        const st = dmStateFor(data.channel);
        if (data.channel === state.activeChannel && document.visibilityState === "visible") {
          // User is actively looking — mark read instead of incrementing.
          state.dmState[data.channel] = { ...st, lastReadAt: msg.created_at || (Date.now()/1000|0), unreadCount: 0 };
          markRead(data.channel, { silent: true });
        } else {
          state.dmState[data.channel] = { ...st, unreadCount: (st.unreadCount || 0) + 1 };
        }
      }
      renderDMs();
      if (presenceChanged) renderTopbar();
      if (data.channel === state.activeChannel) {
        const stream = $("stream");
        const wasNearBottom = nearBottom(stream);
        if (wasFull) {
          renderStream();
        } else {
          const prev = arr.length > 1 ? arr[arr.length - 2] : null;
          appendMessageEl(msg, prev, arr);
          if (wasNearBottom) {
            requestAnimationFrame(() => { stream.scrollTop = stream.scrollHeight; });
          }
        }
      }
    } else if (data.type === "presence") {
      state.online = new Set(data.online || []);
      renderTopbar();
      renderDMs(); // refresh online dots on DM avatars
    } else if (data.type === "profile_update") {
      const p = data.profile;
      const isSelf = state.me && p.id === state.me.id;
      if (!isSelf && !state.users[p.id]) return; // ignore strangers
      state.users[p.id] = { ...(state.users[p.id] || {}), ...p };
      if (isSelf) state.me = { ...state.me, ...p };
      if (isSelf) renderAccountTrigger();
      renderDMs();
      renderTopbar();
      renderStream();
      if (state.sheetView === "peer" && state.sheetPeerId === p.id) renderSheet();
      // Refresh open Settings on a self-update (skip the Profile tab while the
      // user is mid-edit, to avoid clobbering unsaved field values).
      if (isSelf && state.settingsOpen && state.settingsTab !== "profile") renderSettingsContent();
    } else if (data.type === "message_edited") {
      const arr = state.history[data.channel] || [];
      const m = arr.find(x => x.id === data.id);
      if (m) { m.text = data.text; m.edited_at = data.edited_at; }
      rerenderMessage(data.channel, data.id);
      renderDMs(); // sidebar preview may show the edited text
    } else if (data.type === "dm_read") {
      // Read receipt: the reader is data.user_id.
      const ch = data.channel;
      const st = dmStateFor(ch);
      if (state.me && data.user_id === state.me.id) {
        // Our own read action (possibly from another tab) — clear unread.
        state.dmState[ch] = { ...st, lastReadAt: data.last_read_at || st.lastReadAt, unreadCount: 0 };
        renderDMs();
      } else {
        state.dmState[ch] = { ...st, peerLastReadAt: data.last_read_at || 0 };
        if (ch === state.activeChannel) updateStatusRemarks();
      }
    } else if (data.type === "dm_opened") {
      state.history[data.channel] = data.history || [];
      state.historyHasMore[data.channel] = !!data.has_more;
      if (!state.dmThreads.find(t => t.channel === data.channel)) {
        state.dmThreads.push({ channel: data.channel, peer_id: data.peer_id });
      }
      if (data.state) {
        const cur = dmStateFor(data.channel);
        state.dmState[data.channel] = {
          pinned: data.state.pinned ?? cur.pinned,
          lastReadAt: data.state.last_read_at ?? cur.lastReadAt,
          clearedAt: data.state.cleared_at ?? cur.clearedAt,
          unreadCount: data.state.unread_count ?? 0,
          peerLastReadAt: data.state.peer_last_read_at ?? cur.peerLastReadAt,
        };
      }
      renderDMs();
      if (data.channel === state.activeChannel) renderStream();
    }
  }

  // ──────── Web Push ────────
  //
  // Lifecycle: we eagerly register the SW on boot (so it can claim the page
  // and start listening for any pending push events), but the actual
  // subscription step — which calls `pushManager.subscribe()` and triggers
  // the browser's permission prompt — is gated behind an explicit user
  // click, in line with the user-gesture rule that all major browsers
  // enforce. The "Enable notifications" popup that appears on first visit
  // is the user gesture; users who dismiss it can re-enable from the
  // profile pane in the right rail.

  function urlBase64ToUint8Array(base64) {
    const pad = "=".repeat((4 - (base64.length % 4)) % 4);
    const raw = (base64 + pad).replace(/-/g, "+").replace(/_/g, "/");
    const bin = atob(raw);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  }

  function pushSupported() {
    return ("serviceWorker" in navigator)
        && ("PushManager" in window)
        && ("Notification" in window);
  }

  async function registerAndReady() {
    // `navigator.serviceWorker.ready` resolves once a SW is active and
    // controlling this page — the tutorial-recommended check, since
    // `controller` can briefly be null right after first install.
    const reg = await navigator.serviceWorker.register("/sw.js", { updateViaCache: "none" });
    await reg.update();
    await navigator.serviceWorker.ready;
    state.swReg = reg;
    return reg;
  }

  function onServiceWorkerMessage(event) {
    const data = event.data || {};
    if (data.type === "OPEN_CHANNEL" && data.channel) {
      const ch = data.channel;
      if (isDM(ch)) {
        const peer = dmPeerOf(ch);
        if (peer != null && !state.dmThreads.find(t => t.channel === ch)) {
          state.dmThreads.push({ channel: ch, peer_id: peer });
          renderDMs();
        }
      }
      switchChannel(ch);
    } else if (data.type === "PUSH_RECEIVED") {
      // The SW received a push; bump unread state for the affected DM so
      // the green dot appears even before the WS message arrives.
      const ch = data.channel;
      if (isDM(ch) && ch !== state.activeChannel) {
        const st = dmStateFor(ch);
        state.dmState[ch] = { ...st, unreadCount: (st.unreadCount || 0) + 1 };
        renderDMs();
      }
    }
  }

  async function postSubscriptionToServer(sub) {
    const payload = { ...sub.toJSON(), user_agent: navigator.userAgent };
    const r = await fetch("/api/push/subscribe", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify(payload),
    });
    if (!r.ok) throw new Error("subscribe POST failed: " + r.status);
  }

  async function getOrCreateSubscription(reg, vapidKey) {
    let sub = await reg.pushManager.getSubscription();
    if (sub) {
      // Verify the existing subscription was made against the current VAPID
      // public key. If the key rotated (or we accidentally regenerated),
      // unsubscribe and re-subscribe so pushes don't silently 403.
      const existing = sub.options?.applicationServerKey;
      const want = urlBase64ToUint8Array(vapidKey);
      if (existing && !sameBuffer(new Uint8Array(existing), want)) {
        try { await sub.unsubscribe(); } catch (_) {}
        sub = null;
      }
    }
    if (!sub) {
      sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(vapidKey),
      });
    }
    return sub;
  }

  function sameBuffer(a, b) {
    if (!a || !b || a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
    return true;
  }

  // Must be invoked from inside a real user-gesture handler so the browser
  // allows `Notification.requestPermission()` to prompt.
  async function enablePushFlow() {
    if (!pushSupported()) {
      toast("Notifications aren't supported in this browser", true);
      return false;
    }
    try {
      const reg = await registerAndReady();
      const permission = await Notification.requestPermission();
      if (permission !== "granted") {
        try { localStorage.setItem(PUSH_DECLINED_KEY, "1"); } catch (_) {}
        toast(permission === "denied"
          ? "Notifications are blocked — enable them in your browser settings"
          : "Notifications were declined");
        state.pushSubscribed = false;
        if (state.settingsOpen && state.settingsTab === "notifications") renderSettingsContent();
        return false;
      }
      const keyRes = await fetch("/api/push/public-key", { credentials: "same-origin" });
      if (!keyRes.ok) throw new Error("public-key fetch failed: " + keyRes.status);
      const { public_key: vapidKey } = await keyRes.json();
      const sub = await getOrCreateSubscription(reg, vapidKey);
      await postSubscriptionToServer(sub);
      try { localStorage.removeItem(PUSH_DECLINED_KEY); } catch (_) {}
      state.pushSubscribed = true;
      toast("Notifications enabled");
      if (state.settingsOpen && state.settingsTab === "notifications") renderSettingsContent();
      return true;
    } catch (err) {
      console.error("[push] enable flow failed", err);
      toast("Couldn't enable notifications", true);
      return false;
    }
  }

  async function disablePushFlow() {
    try {
      const reg = state.swReg || (await navigator.serviceWorker.getRegistration());
      if (!reg) return;
      const sub = await reg.pushManager.getSubscription();
      if (sub) {
        try {
          await fetch("/api/push/unsubscribe", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            credentials: "same-origin",
            body: JSON.stringify({ endpoint: sub.endpoint }),
          });
        } catch (_) {}
        try { await sub.unsubscribe(); } catch (_) {}
      }
      state.pushSubscribed = false;
      try { localStorage.setItem(PUSH_DECLINED_KEY, "1"); } catch (_) {}
      toast("Notifications disabled");
      if (state.settingsOpen && state.settingsTab === "notifications") renderSettingsContent();
    } catch (err) {
      console.error("[push] disable flow failed", err);
    }
  }

  // Make the profile pane button reachable.
  window.amEnablePush = enablePushFlow;
  window.amDisablePush = disablePushFlow;

  function pushAlreadyPrompted() {
    try { return !!localStorage.getItem(PUSH_PROMPTED_KEY); } catch { return false; }
  }
  function pushPreviouslyDeclined() {
    try { return !!localStorage.getItem(PUSH_DECLINED_KEY); } catch { return false; }
  }
  function markPushPrompted() {
    try { localStorage.setItem(PUSH_PROMPTED_KEY, "1"); } catch {}
  }
  function showPushPrompt() { $("pushPromptModal")?.classList.add("on"); }
  function hidePushPrompt() { $("pushPromptModal")?.classList.remove("on"); }

  async function reconcilePushOnLoad() {
    // Idempotently sync server state for users who already granted permission.
    // Does NOT prompt — that requires a user gesture.
    if (!pushSupported()) return;
    if (Notification.permission !== "granted") {
      // If permission isn't granted and the user hasn't been shown the popup
      // (or explicitly declined), surface it.
      if (Notification.permission === "default"
          && !pushAlreadyPrompted()
          && !pushPreviouslyDeclined()) {
        markPushPrompted();
        showPushPrompt();
      }
      return;
    }
    try {
      const reg = await registerAndReady();
      const keyRes = await fetch("/api/push/public-key", { credentials: "same-origin" });
      if (!keyRes.ok) return;
      const { public_key: vapidKey } = await keyRes.json();
      const sub = await getOrCreateSubscription(reg, vapidKey);
      await postSubscriptionToServer(sub);
      state.pushSubscribed = true;
      if (state.settingsOpen && state.settingsTab === "notifications") renderSettingsContent();
    } catch (err) {
      console.warn("[push] reconcile failed", err);
    }
  }

  $("pushPromptEnable")?.addEventListener("click", async () => {
    hidePushPrompt();
    await enablePushFlow();
  });
  $("pushPromptDismiss")?.addEventListener("click", () => {
    try { localStorage.setItem(PUSH_DECLINED_KEY, "1"); } catch {}
    hidePushPrompt();
  });
  $("pushPromptModal")?.addEventListener("click", (e) => {
    if (e.target.id === "pushPromptModal") hidePushPrompt();
  });

  // ──────── Visibility & focus ────────
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible" && state.activeChannel
        && isDM(state.activeChannel) && isUnread(state.activeChannel)) {
      markRead(state.activeChannel, { silent: true });
    }
  });

  // ──────── Boot ────────
  if ("serviceWorker" in navigator) {
    navigator.serviceWorker.addEventListener("message", onServiceWorkerMessage);
    // Kick off SW registration immediately; reconcile push state separately
    // after init so we know the user's identity for the subscribe POST.
    navigator.serviceWorker.register("/sw.js", { updateViaCache: "none" })
      .then((reg) => { state.swReg = reg; })
      .catch((err) => console.warn("[push] sw register failed", err));
  }
  connect();
})();
