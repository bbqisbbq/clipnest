import { useState, type ReactNode } from "react";

/**
 * Tip renders a small info icon with an explanation. It opens on hover and can
 * also be toggled by click (or Enter/Space when focused), which keeps the hint
 * reachable for keyboard users and when hover events are unreliable.
 */
export function Tip({ text, right = false }: { text: string; right?: boolean }): ReactNode {
  const [open, setOpen] = useState(false);

  return (
    <span
      className={`tip ${open ? "is-open" : ""}`}
      tabIndex={0}
      role="button"
      aria-label={text}
      aria-expanded={open}
      onClick={(e) => {
        e.stopPropagation();
        e.preventDefault();
        setOpen((v) => !v);
      }}
      onBlur={() => setOpen(false)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          setOpen((v) => !v);
        } else if (e.key === "Escape") {
          setOpen(false);
        }
      }}
    >
      <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true">
        <circle cx="8" cy="8" r="6.4" fill="none" stroke="currentColor" strokeWidth="1.3" />
        <path d="M8 7.2v4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
        <circle cx="8" cy="4.9" r="0.85" fill="currentColor" />
      </svg>
      <span className={`tip-body${right ? " is-right" : ""}`} role="tooltip">
        {text}
      </span>
    </span>
  );
}
