/**
 * Message-body rendering: converts shared bodySegments() output into React
 * nodes — text, inline code, links, and line breaks — without any HTML
 * string interpolation (so injection is impossible by construction).
 */
import { Fragment, type JSX } from "react";
import { bodySegments } from "@shared/format";

export function renderBodyNodes(text: string): JSX.Element[] {
  return bodySegments(text).map((seg, i) => {
    switch (seg.kind) {
      case "code":
        return <code key={i}>{seg.text}</code>;
      case "link":
        return (
          <a key={i} href={seg.href} target="_blank" rel="noopener noreferrer">
            {seg.text}
          </a>
        );
      case "br":
        return <br key={i} />;
      default:
        return <Fragment key={i}>{seg.text}</Fragment>;
    }
  });
}
