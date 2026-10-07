import { useEffect, useRef, useState, type JSX } from "react";
import type { Attachment, LinkPreview } from "@shared/types";
import {
  attachmentKind,
  colorFor,
  firstUrl,
  fmtSize,
  fmtTime,
  isVoiceMessage,
  nameFor,
} from "@shared/format";
import type { ChatMessage } from "@shared/store";
import { store, toast } from "../client";
import { useChatState } from "../hooks";
import { renderBodyNodes } from "../format-ui";
import { Avatar } from "./Avatar";
import { Icon } from "./icons";
import { VoiceMessage } from "./VoiceMessage";

/**
 * One message row: avatar slot, header (name + time), bubble (reply-ref,
 * text, attachments), hover actions, link previews, and the delivery remark.
 * Mirrors renderMessage()/updateStatusRemarks() from the legacy client.
 */
export function MessageBubble({
  m,
  prev,
  allMsgs,
  onOpenProfile,
  onImageClick,
}: {
  m: ChatMessage;
  prev: ChatMessage | null;
  allMsgs: ChatMessage[];
  onOpenProfile: (uid: number) => void;
  onImageClick: (src: string) => void;
}) {
  const s = useChatState();
  const mine = s.me != null && m.user_id === s.me.id;
  const isCont =
    !!prev &&
    prev.type === "message" &&
    prev.user_id === m.user_id &&
    !m.reply_to &&
    Math.abs((m.created_at || 0) - (prev.created_at || 0)) < 5 * 60;
  const c = colorFor(m.user_id);
  const [editing, setEditing] = useState(false);
  const status = store.deliveryStatus(m.channel);

  const replyTarget = m.reply_to ? allMsgs.find((x) => x.id === m.reply_to) : null;

  return (
    <div
      className={`msg ${mine ? "me" : "them"}${isCont ? " cont" : ""}`}
      id={`msg-${m.id}`}
    >
      {!mine && (
        <div
          className="av-slot"
          onClick={isCont ? undefined : () => m.user_id != null && onOpenProfile(m.user_id)}
        >
          {isCont ? (
            <span className="timestamp-gutter">{fmtTime(m.created_at)}</span>
          ) : (
            <Avatar uid={m.user_id} />
          )}
        </div>
      )}
      <div className="msg-col">
        {!isCont &&
          (mine ? (
            <div className="head">
              <span className="time">{fmtTime(m.created_at)}</span>
            </div>
          ) : (
            <div className="head">
              <b
                style={{ color: c }}
                onClick={() => m.user_id != null && onOpenProfile(m.user_id)}
              >
                {nameFor(s.users, m.user_id)}
              </b>
              <span className="time">{fmtTime(m.created_at)}</span>
            </div>
          ))}
        <div className="bubble" title={fmtTime(m.created_at)}>
          {replyTarget && <ReplyRef target={replyTarget} />}
          {editing ? (
            <EditArea m={m} onDone={() => setEditing(false)} />
          ) : (
            m.text != null &&
            m.text !== "" && (
              <div className="body">
                {renderBodyNodes(m.text)}
                {m.edited_at != null && <span className="edited-tag">(edited)</span>}
              </div>
            )
          )}
          {!!m.attachments?.length && (
            <div className="attachments">
              {m.attachments.map((a, i) => (
                <AttachmentView key={i} a={a} onImageClick={onImageClick} />
              ))}
            </div>
          )}
        </div>
        <LinkPreviewSlot text={m.text} />
        {m._status === "failed" && (
          <div className="send-status failed">
            <span>Failed to send</span>
            <span aria-hidden="true">·</span>
            <button
              className="retry"
              type="button"
              onClick={() => store.retrySend(m.client_id || m.id)}
            >
              Retry
            </button>
          </div>
        )}
        {status && status.messageId === m.id && <div className="send-status">{status.label}</div>}
      </div>
      <div className="actions">
        <button title="Reply" onClick={() => store.setReplyTo(m)}>
          <Icon name="reply" size={14} sw={1.8} />
        </button>
        {mine && !!m.text && (
          <button title="Edit" onClick={() => setEditing(true)}>
            <Icon name="compose" size={14} sw={1.8} />
          </button>
        )}
        <button
          title="Copy"
          onClick={() => {
            navigator.clipboard
              ?.writeText(m.text || "")
              .then(() => toast("Copied"))
              .catch(() => toast("Copy failed", true));
          }}
        >
          <Icon name="copy" size={14} sw={1.8} />
        </button>
      </div>
    </div>
  );
}

/** "replying to <name>: preview" chip — clicking jumps to the target. */
function ReplyRef({ target }: { target: ChatMessage }) {
  const s = useChatState();
  const tc = colorFor(target.user_id);
  const preview = (
    target.text ||
    (target.attachments?.[0] ? `📎 ${target.attachments[0].name}` : "")
  )
    .replace(/\s+/g, " ")
    .slice(0, 100);
  return (
    <div
      className="reply-ref"
      onClick={() => {
        const el = document.getElementById(`msg-${target.id}`);
        if (!el) return;
        el.scrollIntoView({ block: "center", behavior: "smooth" });
        el.classList.add("highlight");
        setTimeout(() => el.classList.remove("highlight"), 1400);
      }}
    >
      <Icon name="reply" size={12} sw={1.8} />
      <span>replying to</span>
      <b style={{ color: tc }}>{nameFor(s.users, target.user_id)}</b>
      <span className="preview">{preview}</span>
    </div>
  );
}

/** Inline message editor (own text messages only). */
function EditArea({ m, onDone }: { m: ChatMessage; onDone: () => void }) {
  const ref = useRef<HTMLTextAreaElement>(null);
  const [val, setVal] = useState(m.text || "");

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = Math.min(200, el.scrollHeight) + "px";
    el.focus();
    el.setSelectionRange(el.value.length, el.value.length);
  }, []);

  const save = () => {
    const text = val.trim();
    if (text && text !== m.text) store.editMessage(m, text);
    onDone();
  };

  return (
    <>
      <textarea
        ref={ref}
        className="edit-area"
        aria-label="Edit message"
        value={val}
        onChange={(e) => {
          setVal(e.target.value);
          const el = e.currentTarget;
          el.style.height = "auto";
          el.style.height = Math.min(200, el.scrollHeight) + "px";
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            save();
          }
          if (e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            onDone();
          }
        }}
      />
      <div className="edit-actions">
        <button className="cancel" onClick={onDone}>
          Cancel
        </button>
        <button className="save" onClick={save}>
          Save
        </button>
      </div>
    </>
  );
}

/**
 * One attachment: inline image (reserved box), <video> player, voice-message
 * waveform player, named audio card (uploaded audio files), or file card.
 */
function AttachmentView({
  a,
  onImageClick,
}: {
  a: Attachment;
  onImageClick: (src: string) => void;
}) {
  const kind = attachmentKind(a);
  switch (kind) {
    case "image":
      return <ImageAttachment a={a} onClick={() => onImageClick(a.url)} />;
    case "video":
      return (
        <div className="att-video-wrap">
          <video className="att-video" src={a.url} controls playsInline preload="metadata" />
          <a className="att-media-name" href={a.url} target="_blank" rel="noopener" download={a.name}>
            {a.name}
          </a>
        </div>
      );
    case "audio":
      if (isVoiceMessage(a)) return <VoiceMessage a={a} />;
      return (
        <div className="att-audio-wrap">
          <span className="att-audio-ico">
            <Icon name="music" size={16} sw={1.8} />
          </span>
          <div className="att-audio-body">
            <audio className="att-audio" src={a.url} controls preload="metadata" />
            <a className="att-audio-name" href={a.url} target="_blank" rel="noopener" download={a.name}>
              {a.name} · {fmtSize(a.size)}
            </a>
          </div>
        </div>
      );
    default:
      return (
        <a className="att-file" href={a.url} target="_blank" rel="noopener" download={a.name}>
          <span className="ico">
            <Icon name="file" size={16} sw={1.8} />
          </span>
          <span className="meta">
            <b>{a.name}</b>
            <span>{fmtSize(a.size)}</span>
          </span>
        </a>
      );
  }
}

/**
 * Image attachment with the reserved loading box: when dimensions are known
 * we reserve exactly the rendered size so there is no reflow pop-in.
 */
function ImageAttachment({ a, onClick }: { a: Attachment; onClick: () => void }) {
  const [state, setState] = useState<"loading" | "ok" | "err">("loading");
  const w = a.width | 0;
  const h = a.height | 0;
  const known = w > 0 && h > 0;
  let style: React.CSSProperties = {};
  if (known) {
    const scale = Math.min(1, 360 / w, 320 / h);
    const dw = Math.max(1, Math.round(w * scale));
    const dh = Math.max(1, Math.round(h * scale));
    style = { width: dw, aspectRatio: `${dw} / ${dh}` };
  }
  const cls = `att-img-wrap${state === "loading" ? " loading" : ""}${
    !known && state === "loading" ? " unknown" : ""
  }${!known && state !== "loading" ? " natural" : ""}${state === "err" ? " err" : ""}`;
  return (
    <div className={cls} style={style}>
      {state === "loading" && <span className="spinner" />}
      <img
        className="att-img"
        src={a.url}
        alt={a.name}
        loading="lazy"
        onLoad={() => setState("ok")}
        onError={() => setState("err")}
        onClick={onClick}
      />
    </div>
  );
}

/** Link preview card under a bubble — hydrates lazily via the store cache. */
function LinkPreviewSlot({ text }: { text: string }) {
  useStoreVersionSafe();
  const [p, setP] = useState<LinkPreview | null>(null);
  const url = firstUrl(text || "");

  useEffect(() => {
    let live = true;
    if (!url) return;
    const cached = store.state.linkPreviews[url];
    if (cached === "none") return;
    if (cached) {
      setP(cached);
      return;
    }
    store
      .linkPreview(url)
      .then((res) => {
        if (live) setP(res);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [url]);

  if (!p || (!p.title && !p.image)) return null;
  let host = "";
  try {
    host = new URL(p.url).host;
  } catch {
    /* ignore */
  }
  return (
    <div className="link-previews">
      <a className="link-preview" href={p.url} target="_blank" rel="noopener">
        {p.image && <img className="lp-img" src={p.image} alt="" loading="lazy" />}
        <span className="lp-meta">
          <span className="lp-site">{p.site_name || host}</span>
          {p.title && <b className="lp-title">{p.title}</b>}
          {p.description && <span className="lp-desc">{p.description}</span>}
        </span>
      </a>
    </div>
  );
}

// Tiny indirection so the component re-renders when the store bumps.
import { useStoreVersion } from "../hooks";
function useStoreVersionSafe() {
  useStoreVersion();
}
