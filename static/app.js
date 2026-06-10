(() => {
  // ──────── State ────────
  const LAST_CHANNEL_KEY = "am_last_channel";
  const PUSH_PROMPTED_KEY = "am_push_prompted";   // shown a popup at least once
  const PUSH_DECLINED_KEY = "am_push_declined";   // user dismissed; don't ask again
  const PAGE_SIZE_DEFAULT = 50;
  const state = {
    me: null,                // {id, username, display_name, bio}
    isAdmin: false,
    channels: [],            // [{id, name, desc}]
    activeChannel: null,     // null => "No conversation selected" view
    users: {},               // id -> {id, username, display_name, bio}
    contacts: new Set(),     // user_id Set
    dmThreads: [],           // [{channel, peer_id}]
    online: new Set(),       // user_id Set
    history: {},             // channel_id -> [msg]
    historyHasMore: {},      // channel_id -> bool (more older messages available)
    historyLoading: {},      // channel_id -> bool (in-flight fetch flag)
    dmState: {},             // channel_id -> {pinned, lastReadAt, clearedAt, unreadCount}
    pendingAtt: [],
    replyTo: null,
    rightView: "me",         // "me" | "peer"
    rightPeerId: null,
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
  function channelExists(id) {
    if (!id) return false;
    if (state.channels.find(c => c.id === id)) return true;
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
    const initials = initialsFor(uid);
    const cls = large ? "av lg" : "av";
    const online = uid != null && state.online.has(uid);
    return `<div class="${cls}" style="background: linear-gradient(135deg, ${c}, ${shade(c,-.18)})" title="${escapeHTML(nameFor(uid))}">
      <span>${escapeHTML(initials)}</span>
      <span class="stat${online ? "" : " offline"}"></span>
    </div>`;
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
    if (isDM(state.activeChannel)) {
      const peerId = dmPeerOf(state.activeChannel);
      return { kind: "dm", name: nameFor(peerId), desc: handleFor(peerId), peerId };
    }
    const ch = state.channels.find(c => c.id === state.activeChannel);
    if (ch) return { kind: "channel", name: ch.name, desc: ch.desc };
    return { kind: "channel", name: "?", desc: "" };
  }

  // ──────── Rendering: channels ────────
  function renderChannels() {
    const cont = $("rooms");
    cont.innerHTML = "";
    state.channels.forEach(ch => {
      const b = document.createElement("button");
      b.className = "room" + (ch.id === state.activeChannel ? " active" : "");
      b.innerHTML = `<span class="hash">#</span><span>${escapeHTML(ch.name)}</span>`;
      b.addEventListener("click", () => switchChannel(ch.id));
      cont.appendChild(b);
    });
    $("chCount").textContent = state.channels.length;
  }

  function dmStateFor(ch) {
    return state.dmState[ch] || { pinned: false, lastReadAt: 0, clearedAt: 0, unreadCount: 0 };
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
    items.sort((a, b) => nameFor(a.peerId).localeCompare(nameFor(b.peerId)));
    return items;
  }

  function renderDMItem(it) {
    const st = dmStateFor(it.channel);
    const isActive = it.channel === state.activeChannel;
    const unread = isUnread(it.channel);
    const row = document.createElement("div");
    row.className = "dm-row" + (isActive ? " active" : "") + (unread ? " unread" : "");
    row.innerHTML = `
      <button class="dm" data-channel="${escapeHTML(it.channel)}">
        ${avatarHTML(it.peerId)}
        <div class="who">
          <b>${escapeHTML(nameFor(it.peerId))}</b>
          <span class="sub">${escapeHTML(handleFor(it.peerId))}</span>
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
    if (!state.activeChannel) {
      $("chHash").textContent = "";
      $("chName").textContent = "Messages";
      $("chSub").textContent = "";
      $("topbarChip").style.display = "none";
      $("input").placeholder = "";
      composerWrap?.classList.add("hidden");
      return;
    }
    composerWrap?.classList.remove("hidden");
    const meta = activeChannelMeta();
    if (meta.kind === "dm") {
      $("chHash").textContent = "@";
      $("chName").textContent = meta.name;
      $("chSub").textContent = meta.desc;
      const online = state.online.has(meta.peerId);
      $("topbarChip").style.display = "";
      $("topbarChipText").textContent = online ? "online" : "offline";
      $("topbarChip").querySelector(".cdot").style.background = online ? "var(--sage)" : "var(--faint)";
      $("input").placeholder = `Message ${meta.name}`;
    } else {
      $("chHash").textContent = "#";
      $("chName").textContent = meta.name;
      $("chSub").textContent = meta.desc || "";
      $("topbarChip").style.display = "";
      $("topbarChipText").textContent = "connected";
      $("topbarChip").querySelector(".cdot").style.background = "var(--sage)";
      $("input").placeholder = "Message";
    }
  }

  function switchChannel(id) {
    if (state.activeChannel === id) { closeLeft(); return; }
    closeDmMenu();
    state.activeChannel = id;
    saveLastChannel(id);
    state.replyTo = null;
    renderReplyBar();
    renderChannels();
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
  function renderNetLabel() {
    $("netLabel").textContent = "online";
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
        <div class="empty-sub">Pick a channel or direct message from the list, or start a new chat.</div>
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
    requestAnimationFrame(() => { stream.scrollTop = stream.scrollHeight; });
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
    const isCont = prev && prev.type === "message" && prev.user_id === m.user_id && !m.reply_to
                   && Math.abs((m.created_at || 0) - (prev.created_at || 0)) < 5 * 60;
    wrap.className = "msg" + (isCont ? " cont" : "");
    wrap.id = "msg-" + m.id;
    const c = colorFor(m.user_id);
    const head = isCont
      ? `<div class="av-slot"><span class="timestamp-gutter">${escapeHTML(fmtTime(m.created_at))}</span></div>`
      : `<div class="av-slot">${avatarHTML(m.user_id)}</div>`;

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
    const bodyHTML = m.text ? `<div class="body">${renderBody(m.text)}</div>` : "";
    const headBlock = isCont ? "" : `
      <div class="head">
        <b style="color:${c}" data-peer="${m.user_id}">${escapeHTML(nameFor(m.user_id))}</b>
        <span class="handle mono">${escapeHTML(handleFor(m.user_id))}</span>
        <span class="time">${escapeHTML(fmtTime(m.created_at))}</span>
      </div>`;

    wrap.innerHTML = `
      ${head}
      <div>
        ${headBlock}
        ${replyHTML}
        ${bodyHTML}
        ${attachHTML}
        <div class="actions">
          <button data-act="reply" title="Reply">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 14l-5-5 5-5"/><path d="M4 9h9a7 7 0 0 1 7 7v3"/></svg>
          </button>
          <button data-act="copy" title="Copy">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2.5"/><path d="M5 15V6a2 2 0 0 1 2-2h9"/></svg>
          </button>
        </div>
      </div>`;

    wrap.querySelectorAll("[data-act]").forEach(btn => {
      btn.addEventListener("click", (e) => {
        e.stopPropagation();
        const act = btn.getAttribute("data-act");
        if (act === "reply") { state.replyTo = m; renderReplyBar(); $("input").focus(); }
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
    return wrap;
  }

  function renderAttachment(a) {
    const isImg = (a.mime || "").startsWith("image/");
    if (isImg) {
      return `<img class="att-img" src="${escapeHTML(a.url)}" alt="${escapeHTML(a.name)}" loading="lazy"/>`;
    }
    return `<a class="att-file" href="${escapeHTML(a.url)}" target="_blank" rel="noopener" download="${escapeHTML(a.name)}">
      <span class="ico">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/></svg>
      </span>
      <span class="meta"><b>${escapeHTML(a.name)}</b><span>${fmtSize(a.size)}</span></span>
    </a>`;
  }

  function jumpTo(id) {
    const el = document.getElementById("msg-" + id);
    if (!el) return;
    el.scrollIntoView({ block: "center", behavior: "smooth" });
    el.classList.add("highlight");
    setTimeout(() => el.classList.remove("highlight"), 1400);
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
  function renderPendingAtt() {
    const el = $("pendingAtt");
    if (!state.pendingAtt.length) { el.style.display = "none"; el.innerHTML = ""; return; }
    el.style.display = "flex";
    el.innerHTML = "";
    state.pendingAtt.forEach((a, i) => {
      const tag = document.createElement("span");
      tag.className = "pending-att";
      tag.innerHTML = `<b>${escapeHTML(a.name)}</b><span style="opacity:.7">${fmtSize(a.size)}</span>
        <button data-i="${i}" aria-label="Remove">
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
        </button>`;
      tag.querySelector("button").addEventListener("click", () => {
        state.pendingAtt.splice(i, 1); renderPendingAtt(); updateSendState();
      });
      el.appendChild(tag);
    });
  }

  // ──────── Right rail (profile) ────────
  function openProfile(view, uid) {
    state.rightView = view;
    state.rightPeerId = uid ?? null;
    state.editing = false;
    renderRight();
    openRight();
  }

  function pushSettingsHTML() {
    if (!("Notification" in window) || !("serviceWorker" in navigator) || !("PushManager" in window)) {
      return "";
    }
    const perm = Notification.permission;
    const enabled = state.pushSubscribed && perm === "granted";
    let body, btn;
    if (perm === "denied") {
      body = `Blocked by your browser. Open the site settings (click the lock icon in the address bar) to allow notifications.`;
      btn = "";
    } else if (enabled) {
      body = `You'll get a system notification for new direct messages while AlexMessage isn't open or focused.`;
      btn = `<button class="btn" id="notifDisable">Turn off notifications</button>`;
    } else {
      body = `Get a system notification for new direct messages even when AlexMessage is in the background.`;
      btn = `<button class="btn primary" id="notifEnable">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M6 8a6 6 0 1 1 12 0c0 7 3 8 3 8H3s3-1 3-8"/><path d="M10 21a2 2 0 0 0 4 0"/></svg>
        Enable notifications
      </button>`;
    }
    return `
      <div class="field" style="margin-top:18px">
        <label>Notifications</label>
        <div class="read-only" style="line-height:1.5;">${body}</div>
        ${btn ? `<div class="btn-row">${btn}</div>` : ""}
      </div>`;
  }

  function renderRight() {
    const title = $("rightTitle");
    const body = $("rightBody");
    if (state.rightView === "me" && state.me) {
      title.textContent = "Your profile";
      const me = state.me;
      body.innerHTML = `
        <div class="profile-banner"></div>
        <div class="profile-row">
          ${avatarHTML(me.id, true)}
          <div class="name-block">
            <div class="name">${escapeHTML(me.display_name)}</div>
            <div class="handle">@${escapeHTML(me.username)}</div>
          </div>
        </div>
        ${state.editing ? `
          <div class="field">
            <label>Display name <span class="hint">Shown to others</span></label>
            <input type="text" class="text" id="editName" maxlength="40" value="${escapeHTML(me.display_name)}" placeholder="e.g. Alex" />
          </div>
          <div class="field">
            <label>Bio <span class="hint">A line or two</span></label>
            <textarea class="bio" id="editBio" maxlength="280" placeholder="What you're up to, where you are, anything…">${escapeHTML(me.bio || "")}</textarea>
          </div>
          <div class="btn-row">
            <button class="btn primary" id="saveProfile">Save</button>
            <button class="btn ghost" id="cancelEdit">Cancel</button>
          </div>
        ` : `
          <div class="field">
            <label>Bio</label>
            <div class="read-only ${me.bio ? "" : "empty"}">${me.bio ? escapeHTML(me.bio) : "No bio yet — click edit to add one."}</div>
          </div>
          <div class="btn-row">
            <button class="btn primary" id="editProfile">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 20h4l10-10-4-4L4 16v4z"/><path d="M14 6l4 4"/></svg>
              Edit profile
            </button>
            <button class="btn danger" id="logoutBtn">Sign out</button>
            ${state.isAdmin ? `<a class="btn" href="${adminUrl()}" target="_blank">Admin panel</a>` : ""}
          </div>
        `}
        ${pushSettingsHTML()}
        <div class="field" style="margin-top:18px">
          <label>Account</label>
          <div class="net-card">
            <div class="row"><span class="k">Username</span><span class="v">@${escapeHTML(me.username)}<span class="badge-you">YOU</span></span></div>
            <div class="row"><span class="k">User ID</span><span class="v">#${me.id}</span></div>
            <div class="row"><span class="k">Status</span><span class="v" style="color:var(--sage-deep)">online</span></div>
          </div>
        </div>`;

      if (state.editing) {
        $("saveProfile").addEventListener("click", saveProfile);
        $("cancelEdit").addEventListener("click", () => { state.editing = false; renderRight(); });
      } else {
        $("editProfile").addEventListener("click", () => { state.editing = true; renderRight(); setTimeout(() => $("editName")?.focus(), 0); });
        $("logoutBtn").addEventListener("click", doLogout);
      }
      $("notifEnable")?.addEventListener("click", () => enablePushFlow());
      $("notifDisable")?.addEventListener("click", () => disablePushFlow());
    } else if (state.rightView === "peer" && state.rightPeerId != null) {
      const uid = state.rightPeerId;
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
            <div class="handle">@${escapeHTML(u.username)}</div>
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
      $("dmBtn").addEventListener("click", () => { openDM(uid); });
      $("contactBtn").addEventListener("click", () => { toggleContact(uid).then(renderRight); });
    } else {
      title.textContent = "—";
      body.innerHTML = "";
    }
  }

  function adminUrl() {
    const port = location.port === "8765" ? "8001" : "8001"; // dev mapping; admin defaults to 8001
    return `${location.protocol}//${location.hostname}:${port}/`;
  }

  async function saveProfile() {
    const name = $("editName").value.trim();
    const bio = $("editBio").value.trim();
    try {
      const r = await fetch("/api/me", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ display_name: name, bio }),
      });
      if (!r.ok) throw new Error();
      const j = await r.json();
      state.me = { ...state.me, ...j.user };
      state.users[state.me.id] = j.user;
      state.editing = false;
      renderRight();
      renderDMs();
      renderStream();
      renderTopbar();
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
  function closeLeft() { $("leftPane").classList.remove("open"); maybeCloseBackdrop(); }
  function openRight() { $("rightPane").classList.add("open"); if (isMobile()) $("backdrop").classList.add("on"); }
  function closeRight(){ $("rightPane").classList.remove("open"); maybeCloseBackdrop(); }
  function maybeCloseBackdrop() {
    if (!$("leftPane").classList.contains("open") && !$("rightPane").classList.contains("open")) {
      $("backdrop").classList.remove("on");
    }
  }
  function isMobile() { return window.matchMedia("(max-width: 920px)").matches; }
  const appEl = document.querySelector(".app");
  $("menuBtn").addEventListener("click", openLeft);
  $("leftClose").addEventListener("click", closeLeft);
  $("rightClose").addEventListener("click", () => {
    if (isMobile()) closeRight();
    else appEl.classList.add("right-collapsed");
  });
  $("profileToggle").addEventListener("click", () => {
    if (!isMobile()) appEl.classList.remove("right-collapsed");
    openProfile("me");
  });
  $("backdrop").addEventListener("click", () => { closeLeft(); closeRight(); });

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

  function doSend() {
    if (!state.activeChannel) return;
    const text = input.value.trim();
    if (!text && !state.pendingAtt.length) return;
    send({
      type: "message",
      channel: state.activeChannel,
      text,
      reply_to: state.replyTo ? state.replyTo.id : null,
      attachments: state.pendingAtt,
    });
    input.value = "";
    input.style.height = "auto";
    state.pendingAtt = [];
    state.replyTo = null;
    renderPendingAtt();
    renderReplyBar();
    updateSendState();
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
      state.pendingAtt.push({ name: j.name, url: j.url, size: j.size, mime: j.mime });
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

  function connect() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${location.host}/ws`);
    ws.addEventListener("open", () => {
      wsReady = true;
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
        window.location.href = "/login";
        return;
      }
      toast("Disconnected — reconnecting…", true);
      setTimeout(connect, 1500);
    });
    ws.addEventListener("error", () => {});
  }
  function send(payload) {
    if (wsReady) ws.send(JSON.stringify(payload));
    else pendingSend.push(payload);
  }

  function onMessage(data) {
    if (data.type === "init") {
      state.me = data.me;
      state.channels = data.channels;
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
      state.dmState = {};
      Object.entries(data.dm_state || {}).forEach(([ch, s]) => {
        state.dmState[ch] = {
          pinned: !!s.pinned,
          lastReadAt: s.last_read_at || 0,
          clearedAt: s.cleared_at || 0,
          unreadCount: s.unread_count || 0,
        };
      });
      const saved = loadLastChannel();
      state.activeChannel = channelExists(saved) ? saved : null;
      fetch("/api/me", { credentials: "same-origin" })
        .then(r => r.ok ? r.json() : null)
        .then(j => { state.isAdmin = !!(j && j.is_admin); if (state.rightView === "me") renderRight(); })
        .catch(() => {});
      renderNetLabel();
      renderChannels();
      renderDMs();
      renderTopbar();
      renderStream();
      renderRight();
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
      renderDMs();
      renderTopbar();
      renderStream();
      if (state.rightView === "peer" && state.rightPeerId === p.id) renderRight();
      if (state.rightView === "me" && isSelf && !state.editing) renderRight();
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
        if (state.rightView === "me") renderRight();
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
      if (state.rightView === "me") renderRight();
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
      if (state.rightView === "me") renderRight();
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
      if (state.rightView === "me") renderRight();
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
