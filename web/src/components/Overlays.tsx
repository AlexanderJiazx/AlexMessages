import { useEffect, useRef, useState } from "react";
import { useEscapeLayer, useFocusTrap } from "../layers";
import { onToast } from "../client";
import type { ToastEvent } from "@shared/store";
import { useChatState } from "../hooks";

/** Bottom-right toast stack + the delayed "Reconnecting…" banner. */
export function Toasts() {
  const [items, setItems] = useState<(ToastEvent & { id: number })[]>([]);
  const s = useChatState();

  useEffect(() => {
    let id = 0;
    return onToast((t) => {
      const tid = ++id;
      setItems((cur) => [...cur, { ...t, id: tid }]);
      setTimeout(() => setItems((cur) => cur.filter((x) => x.id !== tid)), 2200);
    });
  }, []);

  return (
    <div className="toast-stack" role="status" aria-live="polite">
      {items.map((t) => (
        <div key={t.id} className={`toast${t.isErr ? " err" : ""}`}>
          {t.text}
        </div>
      ))}
      {s.connState === "reconnecting" && (
        <div className="conn-banner">
          <span className="conn-spinner" />
          <span>Reconnecting…</span>
        </div>
      )}
    </div>
  );
}

/** Full-screen image preview. */
export function Lightbox({ src, onClose }: { src: string | null; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  useEscapeLayer(!!src, onClose);
  useFocusTrap(ref, !!src);
  if (!src) return null;
  return (
    <div
      ref={ref}
      className="lightbox on"
      role="dialog"
      aria-modal="true"
      aria-label="Image preview"
      tabIndex={-1}
      onClick={onClose}
    >
      <img src={src} alt="" />
    </div>
  );
}
