import { useEffect, useRef, useState } from "react";
import { fmtSize, nameFor } from "@shared/format";
import { store, toast } from "../client";
import { useChatState } from "../hooks";
import { Icon } from "./icons";
import { DictationBar } from "./DictationBar";

/**
 * The composer: reply bar, pending-attachment thumbnails, the autosizing
 * textarea, attach/ dictate/send buttons, drag-and-drop upload, and the
 * upload status line. Mirrors the legacy pill composer plus the new mic.
 */
export function Composer({ peerName }: { peerName: string }) {
  const s = useChatState();
  const [text, setText] = useState("");
  const [dictating, setDictating] = useState(false);
  const [uploading, setUploading] = useState<string | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const [dragOver, setDragOver] = useState(false);

  const canSend = !!(text.trim() || s.pendingAtt.length) && !dictating;

  // Autosize (cap 160px like the legacy client).
  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = Math.min(160, el.scrollHeight) + "px";
  }, [text, s.activeChannel]);

  // Clear the draft when switching conversations.
  useEffect(() => {
    setText("");
    setDictating(false);
  }, [s.activeChannel]);

  function doSend() {
    if (!canSend) return;
    store.sendMessage(text);
    setText("");
  }

  async function uploadFiles(files: File[]) {
    for (const f of files) {
      if (f.size > s.maxUpload) {
        toast(`${f.name} exceeds ${Math.round(s.maxUpload / 1024 / 1024)}MB`, true);
        continue;
      }
      setUploading(f.name);
      await store.uploadAttachment(f, f.name);
      setUploading(null);
    }
  }

  const replyTo = s.replyTo;

  return (
    <div className={`composer-wrap${s.activeChannel ? "" : " hidden"}`}>
      <div
        className={`composer${dragOver ? " focused" : ""}`}
        onDragEnter={(e) => {
          e.preventDefault();
          setDragOver(true);
        }}
        onDragOver={(e) => e.preventDefault()}
        onDragLeave={(e) => {
          e.preventDefault();
          setDragOver(false);
        }}
        onDrop={(e) => {
          e.preventDefault();
          setDragOver(false);
          void uploadFiles([...(e.dataTransfer?.files || [])]);
        }}
      >
        {replyTo && (
          <div className="replying-to">
            <Icon name="reply" size={13} sw={1.8} />
            <span>
              Replying to <b>{nameFor(s.users, replyTo.user_id)}</b>
            </span>
            <span className="preview">
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

        {!!s.pendingAtt.length && (
          <div className="pending-attachments">
            {s.pendingAtt.map((a, i) => (
              <PendingAtt key={i} a={a} onRemove={() => store.removePendingAtt(i)} />
            ))}
          </div>
        )}

        {dictating ? (
          <DictationBar
            onExit={() => setDictating(false)}
            onTranscribed={(t) => {
              setText((cur) => (cur ? cur + " " + t : t));
              inputRef.current?.focus();
            }}
          />
        ) : (
          <div className="input-row">
            <button
              className="toolbtn"
              title={`Attach file (max ${Math.round(s.maxUpload / 1024 / 1024)}MB)`}
              aria-label="Attach"
              onClick={() => fileRef.current?.click()}
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
              rows={1}
              value={text}
              onChange={(e) => setText(e.target.value)}
              onFocus={(e) => e.currentTarget.closest(".composer")?.classList.add("focused")}
              onBlur={(e) => e.currentTarget.closest(".composer")?.classList.remove("focused")}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  doSend();
                }
                if (e.key === "Escape" && s.replyTo) store.setReplyTo(null);
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
                onClick={() => setDictating(true)}
              >
                <Icon name="mic" size={18} sw={1.7} />
              </button>
            )}
          </div>
        )}
      </div>
      <div className={`composer-meta${uploading ? " active" : ""}`}>
        <span>{uploading ? `Uploading ${uploading}…` : ""}</span>
      </div>
    </div>
  );
}

/** A staged attachment chip: image thumbnails get the iMessage-style preview. */
function PendingAtt({
  a,
  onRemove,
}: {
  a: { name: string; url: string; size: number; mime: string };
  onRemove: () => void;
}) {
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
      <button aria-label="Remove" onClick={onRemove}>
        <Icon name="x" size={11} sw={2.4} />
      </button>
    </span>
  );
}
