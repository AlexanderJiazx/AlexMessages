import { useEffect, useState } from "react";
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
    <div className="toast-stack">
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
  useEffect(() => {
    if (!src) return;
    const esc = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", esc);
    return () => document.removeEventListener("keydown", esc);
  }, [src, onClose]);
  if (!src) return null;
  return (
    <div className="lightbox on" onClick={onClose}>
      <img src={src} alt="" />
    </div>
  );
}
