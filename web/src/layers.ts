/**
 * The overlay layer stack: dialogs, sheets, menus, the lightbox, and the
 * phone's rail drawer. Escape closes only the topmost open layer (the one
 * opened last), and dialogs keep Tab inside themselves and hand focus back
 * to whatever opened them when they close.
 */
import { useEffect, useRef, type RefObject } from "react";

type Layer = { id: number; onEscape: () => void };

const stack: Layer[] = [];
let nextId = 0;
let installed = false;

function onKeyDown(e: KeyboardEvent): void {
  if (e.key !== "Escape" || e.defaultPrevented) return;
  // An IME uses Escape to cancel its composition.
  if (e.isComposing || e.keyCode === 229) return;
  const top = stack[stack.length - 1];
  if (!top) return;
  e.preventDefault();
  top.onEscape();
}

/**
 * Registers a layer while `active` is true. The newest active layer is the
 * top one; Escape calls only its `onEscape`. The callback is read through a
 * ref, so passing an inline function does not reorder the stack.
 */
export function useEscapeLayer(active: boolean, onEscape: () => void): void {
  const cb = useRef(onEscape);
  cb.current = onEscape;
  useEffect(() => {
    if (!active) return;
    if (!installed) {
      document.addEventListener("keydown", onKeyDown);
      installed = true;
    }
    const layer: Layer = { id: ++nextId, onEscape: () => cb.current() };
    stack.push(layer);
    return () => {
      const i = stack.findIndex((l) => l.id === layer.id);
      if (i >= 0) stack.splice(i, 1);
    };
  }, [active]);
}

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

function focusables(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
    (el) => el.offsetParent !== null || el === document.activeElement,
  );
}

/**
 * Dialog focus handling while `active`: moves focus into `ref` (unless
 * something inside already has it), keeps Tab / Shift+Tab cycling inside,
 * and on close returns focus to the element that had it before opening.
 */
export function useFocusTrap(ref: RefObject<HTMLElement>, active: boolean): void {
  useEffect(() => {
    if (!active) return;
    const before = document.activeElement as HTMLElement | null;
    const root = ref.current;
    if (root && !root.contains(document.activeElement)) {
      const first = focusables(root)[0];
      (first || root).focus({ preventScroll: true });
    }
    const onKey = (e: KeyboardEvent) => {
      const el = ref.current;
      if (e.key !== "Tab" || !el) return;
      // Only the dialog that holds focus traps it (dialogs can stack).
      if (!el.contains(document.activeElement)) return;
      const items = focusables(el);
      if (!items.length) {
        e.preventDefault();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      if (before && before.isConnected && typeof before.focus === "function") {
        before.focus({ preventScroll: true });
      }
    };
  }, [active, ref]);
}

/** Enter / Space activate a non-button element with role="button". */
export function onActivateKey(fn: () => void) {
  return (e: React.KeyboardEvent) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      fn();
    }
  };
}

/** True while an IME is composing: Enter then confirms the composition. */
export function isImeComposing(e: React.KeyboardEvent): boolean {
  return e.nativeEvent.isComposing || e.keyCode === 229;
}
