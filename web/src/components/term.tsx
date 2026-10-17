// xterm.js in the panel's colours. A chunk of its own: only the terminal and the recordings'
// player load it (React.lazy at the call sites), never the first screen.
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useEffect, useRef } from "react";

/** What the owner of a terminal does with it. */
export type TermHandle = {
  write: (data: string | Uint8Array) => void;
  reset: () => void;
  resize: (cols: number, rows: number) => void;
  focus: () => void;
  size: () => { cols: number; rows: number };
};

type Props = {
  /** Gives the handle once the terminal is up; called again if it is rebuilt. */
  onReady: (h: TermHandle) => void;
  /** What the person types. Left out, the terminal is for watching only. */
  onData?: (data: string) => void;
  /** The size changed (the window, the fit). */
  onResize?: (cols: number, rows: number) => void;
  /** Shift+Esc: the way out of the terminal for a keyboard (it takes Tab and Esc for itself). */
  onLeave?: () => void;
  /** Without a size the terminal fills its box; with one it is exactly that big (a recording). */
  size?: { cols: number; rows: number };
  label: string;
};

function token(css: CSSStyleDeclaration, name: string, fallback: string): string {
  return css.getPropertyValue(name).trim() || fallback;
}

export default function Term({ onReady, onData, onResize, onLeave, size, label }: Props) {
  const host = useRef<HTMLDivElement>(null);
  // The latest callbacks without rebuilding the terminal when a parent renders again.
  const cb = useRef({ onReady, onData, onResize, onLeave });
  useEffect(() => {
    cb.current = { onReady, onData, onResize, onLeave };
  });
  const watchOnly = !onData;
  const fixed = size !== undefined;
  const cols = size?.cols;
  const rows = size?.rows;

  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const css = getComputedStyle(document.documentElement);
    const term = new Terminal({
      cursorBlink: !watchOnly,
      disableStdin: watchOnly,
      fontFamily: token(css, "--font-mono-stack", "ui-monospace, monospace"),
      fontSize: 13,
      lineHeight: 1.25,
      scrollback: 5000,
      // A recording is a fixed grid; a live terminal is told its size by the fit.
      ...(cols && rows ? { cols, rows } : {}),
      theme: { background: token(css, "--code-bg", "#161a24"), foreground: token(css, "--code-fg", "#e7e9ed") },
    });
    const fit = fixed ? null : new FitAddon();
    if (fit) term.loadAddon(fit);
    term.open(el);
    const refit = () => {
      try {
        fit?.fit();
      } catch {
        // the box has no size yet (hidden); the observer fits it once it has
      }
    };
    refit();
    // The monospace font may arrive after the first measure: measure again then.
    void document.fonts?.ready.then(refit);
    term.attachCustomKeyEventHandler((e) => {
      if (e.type === "keydown" && e.key === "Escape" && e.shiftKey && cb.current.onLeave) {
        cb.current.onLeave();
        return false;
      }
      return true;
    });
    const typed = term.onData((d) => cb.current.onData?.(d));
    const resized = term.onResize(({ cols: c, rows: r }) => cb.current.onResize?.(c, r));
    const ro = fit ? new ResizeObserver(refit) : null;
    ro?.observe(el);
    cb.current.onReady({
      write: (d) => term.write(d),
      reset: () => term.reset(),
      // After what is queued to be written: a resize in a recording lands between its lines.
      resize: (c, r) => term.write("", () => term.resize(Math.max(2, c), Math.max(1, r))),
      focus: () => term.focus(),
      size: () => ({ cols: term.cols, rows: term.rows }),
    });
    return () => {
      ro?.disconnect();
      typed.dispose();
      resized.dispose();
      term.dispose();
    };
    // The terminal is built once per mount; a different recording is a different mount (key).
  }, [watchOnly, fixed, cols, rows]);

  return <div ref={host} className="term-host" role="group" aria-label={label} />;
}
