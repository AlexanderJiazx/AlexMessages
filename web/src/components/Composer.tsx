import { useEffect, useRef, useState } from "react";
import { fmtSize, nameFor } from "@shared/format";
import type { Attachment } from "@shared/types";
import { store, toast } from "../client";
import { useChatState } from "../hooks";
import { Icon } from "./icons";
import { DictationBar } from "./DictationBar";
import { isImeComposing } from "../layers";

/**
 * The composer: reply bar, pending-attachment thumbnails, the autosizing
 * textarea, attach/ dictate/send buttons, drag-and-drop upload, and the
 * upload status line. Mirrors the legacy pill composer plus the new mic.
 */
export function Composer({ peerName }: { peerName: string }) {
  const s = useChatState();
  const [text, setText] = useState("");
  const [dictating, setDictating] = useState(false);
  // The file being uploaded, and the conversation it was started in.
  const [uploading, setUploading] = useState<{ channel: string; name: string } | null>(null);
  const [focused, setFocused] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const dragDepth = useRef(0);
  const inFlight = useRef(0);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  // The conversation a dictation started in (null while not dictating).
  const dictationChannel = useRef<string | null>(null);

  const canSend = !!(text.trim() || s.pendingAtt.length) && !dictating;

  // Autosize (cap 160px like the legacy client).
  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = Math.min(160, el.scrollHeight) + "px";
  }, [text, s.activeChannel, dictating]);

  // Clear the draft when switching conversations.
  useEffect(() => {
    setText("");
    setDictating(false);
    dictationChannel.current = null;
    dragDepth.current = 0;
    setDragOver(false);
  }, [s.activeChannel]);

  // Back from Stop → transcribe: focus the textarea (it was just remounted).
  const wasDictating = useRef(false);
  useEffect(() => {
    if (wasDictating.current && !dictating && dictationChannel.current === store.state.activeChannel) {
      inputRef.current?.focus();
    }
    if (!dictating) dictationChannel.current = null;
    wasDictating.current = dictating;
  }, [dictating]);

  // Toasts sit above the composer: publish its height as a CSS variable.
  useEffect(() => {
    const el = wrapRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const root = document.documentElement;
    const ro = new ResizeObserver(() => {
      root.style.setProperty("--composer-h", `${el.offsetHeight}px`);
    });
    ro.observe(el);
    return () => {
      ro.disconnect();
      root.style.removeProperty("--composer-h");
    };
  }, []);

  function doSend() {
    if (!canSend) return;
    store.sendMessage(text);
    setText("");
  }

  async function uploadFiles(files: File[]) {
    const channel = store.state.activeChannel;
    if (!channel || !files.length) return;
    const tooBig = files.filter((f) => f.size > s.maxUpload);
    for (const f of tooBig) toast(`${f.name} exceeds ${Math.round(s.maxUpload / 1024 / 1024)}MB`, true);
    const ok = files.filter((f) => f.size <= s.maxUpload);
    // Stage at most what one message may still carry (uploads already
    // running count too).
    const room = Math.max(0, store.state.maxAttachments - store.state.pendingAtt.length - inFlight.current);
    const take = ok.slice(0, room);
    const left = ok.length - take.length;
    if (left > 0) {
      toast(
        `A message can carry at most ${store.state.maxAttachments} files — ${left} ${left === 1 ? "file was" : "files were"} not attached`,
        true,
      );
    }
    inFlight.current += take.length;
    let started = 0;
    try {
      for (const f of take) {
        // The user left the conversation: drop the rest of this batch.
        if (store.state.activeChannel !== channel) break;
        started++;
        setUploading({ channel, name: f.name });
        try {
          await store.uploadAttachment(f, f.name);
        } finally {
          inFlight.current--;
        }
      }
    } finally {
      inFlight.current -= take.length - started;
      setUploading((u) => (u && u.channel === channel ? null : u));
    }
  }

  const replyTo = s.replyTo;
  const showUploading = uploading && uploading.channel === s.activeChannel ? uploading.name : null;

  return (
    <div className={`composer-wrap${s.activeChannel ? "" : " hidden"}`} ref={wrapRef}>
      <div
        className={`composer${focused || dragOver ? " focused" : ""}${dragOver ? " drag-over" : ""}`}
        onDragEnter={(e) => {
          e.preventDefault();
          dragDepth.current++;
          setDragOver(true);
        }}
        onDragOver={(e) => e.preventDefault()}
        onDragLeave={(e) => {
          e.preventDefault();
          // dragleave fires for every child crossed: count the depth.
          dragDepth.current = Math.max(0, dragDepth.current - 1);
          if (dragDepth.current === 0) setDragOver(false);
        }}
        onDrop={(e) => {
          e.preventDefault();
          dragDepth.current = 0;
          setDragOver(false);
          void uploadFiles([...(e.dataTransfer?.files || [])]);
        }}
      >
        {replyTo && (
          <div className="replying-to">
            <Icon name="reply" size={13} sw={1.8} />
            <span className="replying-who">
              Replying to <b dir="auto">{nameFor(s.users, replyTo.user_id)}</b>
            </span>
            <span className="preview" dir="auto">
              ·{" "}
              {replyTo.text ||
                (replyTo.attachments?.[0]
                  ? `📎 ${replyTo.attachments[0].name}`
                  : "")}
            </span>
            <button className="x" aria-label="Cancel reply" onClick={() => store.setReplyTo(null)}>
              <Icon name="x" size={12} sw={2} />
            </button>
          </div>
        )}

        {(!!s.pendingAtt.length || showUploading) && (
          <div className="pending-attachments">
            {s.pendingAtt.map((a, i) => (
              <PendingAtt key={a.url} a={a} onRemove={() => store.removePendingAtt(i)} />
            ))}
            {showUploading && (
              <span className="pending-att uploading" aria-live="polite">
                <span className="spinner" aria-hidden="true" />
                <b>{showUploading}</b>
              </span>
            )}
          </div>
        )}

        {dictating && s.activeChannel ? (
          <DictationBar
            channel={s.activeChannel}
            onExit={() => setDictating(false)}
            onTranscribed={(t) => setText((cur) => (cur ? cur + " " + t : t))}
          />
        ) : (
          <div className="input-row">
            <button
              className="toolbtn"
              title={`Attach file (max ${Math.round(s.maxUpload / 1024 / 1024)}MB)`}
              aria-label="Attach"
              onClick={() => {
                if (!store.canAttachMore()) {
                  toast(`A message can carry at most ${s.maxAttachments} files`, true);
                  return;
                }
                fileRef.current?.click();
              }}
            >
              <Icon name="plus" size={18} sw={1.6} />
            </button>
            <input
              type="file"
              ref={fileRef}
              style={{ display: "none" }}
              multiple
              onChange={(e) => {
                const files = [...(e.target.files || [])];
                e.target.value = "";
                void uploadFiles(files);
              }}
            />
            <textarea
              ref={inputRef}
              className="input"
              placeholder={s.activeChannel ? `Message ${peerName}` : ""}
              aria-label={s.activeChannel ? `Message ${peerName}` : "Message"}
              dir="auto"
              rows={1}
              value={text}
              onChange={(e) => setText(e.target.value)}
              onFocus={() => setFocused(true)}
              onBlur={() => setFocused(false)}
              onKeyDown={(e) => {
                // Enter while an IME composes confirms the composition only.
                if (e.key === "Enter" && !e.shiftKey && !isImeComposing(e)) {
                  e.preventDefault();
                  doSend();
                }
                if (e.key === "Escape" && s.replyTo) {
                  e.preventDefault();
                  store.setReplyTo(null);
                }
              }}
            />
            {text.trim() || s.pendingAtt.length ? (
              <button className="send" aria-label="Send" title="Send" onClick={doSend} disabled={!canSend}>
                <Icon name="send" size={18} sw={2.2} />
              </button>
            ) : (
              <button
                className="toolbtn mic-btn"
                aria-label="Dictate"
                title="Voice dictation"
                onClick={() => {
                  dictationChannel.current = s.activeChannel;
                  setDictating(true);
                }}
              >
                <Icon name="mic" size={18} sw={1.7} />
              </button>
            )}
          </div>
        )}
      </div>
      <div className={`composer-meta${showUploading ? " active" : ""}`}>
        <span>{showUploading ? `Uploading ${showUploading}…` : ""}</span>
      </div>
    </div>
  );
}

/** A staged attachment chip: image thumbnails get the iMessage-style preview. */
function PendingAtt({ a, onRemove }: { a: Attachment; onRemove: () => void }) {
  const isImg = (a.mime || "").startsWith("image/");
  return (
    <span className={isImg ? "pending-thumb" : "pending-att"}>
      {isImg ? (
        <img src={a.url} alt={a.name} title={a.name} />
      ) : (
        <>
          <b>{a.name}</b>
          <span style={{ opacity: 0.7 }}>{fmtSize(a.size)}</span>
        </>
      )}
      <button aria-label={`Remove ${a.name}`} onClick={onRemove}>
        <Icon name="x" size={11} sw={2.4} />
      </button>
    </span>
  );
}
