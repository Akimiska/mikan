// A player for the terminal's recordings (asciicast v2: a header line, then one JSON event
// per line: [seconds, "o" | "i" | "r", data]). Its own and small: xterm draws, this runs the
// clock. Only the output ("o") and the resizes ("r") are shown; what was typed is in the file
// but never replayed, so a password typed into a prompt that does not echo stays unseen.
import { Pause, Play, RotateCcw } from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { t } from "../i18n";
import type { TermHandle } from "./term";
import { Button, Segmented, Skeleton } from "./ui";

const Term = lazy(() => import("./term"));

export type Cast = {
  cols: number;
  rows: number;
  /** Time on the player's clock (long pauses cut short), the same index as `events`. */
  at: number[];
  events: ({ kind: "o"; data: string } | { kind: "r"; cols: number; rows: number })[];
  /** What the clock reads at the end. */
  length: number;
};

/** A pause in a session is cut to this long on the clock: a recording is for reading, not for waiting. */
const IDLE_CAP = 2;
const MAX_COLS = 500;
const MAX_ROWS = 200;

/** The recording as the player keeps it; null when the file is not an asciicast v2. */
export function parseCast(text: string): Cast | null {
  const lines = text.split("\n");
  let head: unknown;
  try {
    head = JSON.parse(lines[0] ?? "");
  } catch {
    return null;
  }
  const h = head as { version?: unknown; width?: unknown; height?: unknown };
  if (!h || h.version !== 2 || typeof h.width !== "number" || typeof h.height !== "number") return null;
  const cast: Cast = { cols: clamp(h.width, 2, MAX_COLS), rows: clamp(h.height, 1, MAX_ROWS), at: [], events: [], length: 0 };
  let last = 0;
  let clock = 0;
  for (let i = 1; i < lines.length; i++) {
    const line = lines[i];
    if (!line) continue;
    let ev: unknown;
    try {
      ev = JSON.parse(line);
    } catch {
      continue; // a torn last line of a session cut short
    }
    if (!Array.isArray(ev) || typeof ev[0] !== "number" || typeof ev[1] !== "string" || typeof ev[2] !== "string") continue;
    const [sec, kind, data] = ev as [number, string, string];
    if (kind !== "o" && kind !== "r") continue;
    clock += Math.min(Math.max(0, sec - last), IDLE_CAP);
    last = Math.max(last, sec);
    if (kind === "o") {
      cast.events.push({ kind, data });
    } else {
      const m = /^(\d{1,4})x(\d{1,4})$/.exec(data);
      if (!m) continue;
      cast.events.push({ kind, cols: clamp(Number(m[1]), 2, MAX_COLS), rows: clamp(Number(m[2]), 1, MAX_ROWS) });
    }
    cast.at.push(clock);
  }
  cast.length = clock;
  return cast;
}

function clamp(n: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, Math.round(n)));
}

const clockText = (s: number) => `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, "0")}`;

type Speed = "1" | "2" | "4";

export function CastPlayer({ text, name }: { text: string; name: string }) {
  const cast = useMemo(() => parseCast(text), [text]);
  if (!cast) {
    return (
      <div className="banner err" role="alert">
        {t("support.player.broken")}
      </div>
    );
  }
  // A recording is its own mount: nothing of the last one is left in the terminal.
  return <Player key={name} cast={cast} name={name} />;
}

function Player({ cast, name }: { cast: Cast; name: string }) {
  const term = useRef<TermHandle | null>(null);
  const [ready, setReady] = useState(false);
  const [playing, setPlaying] = useState(false);
  const [pos, setPos] = useState(0);
  const [speed, setSpeed] = useState<Speed>("1");
  // The clock's state outside React: the next event, and where the wall clock met the recording's.
  const next = useRef(0);
  const run = useRef({ wall: 0, from: 0 });
  const speedRef = useRef(1);
  speedRef.current = Number(speed);

  const apply = useCallback(
    (to: number) => {
      const h = term.current;
      if (!h) return;
      let out = "";
      while (next.current < cast.events.length && cast.at[next.current]! <= to) {
        const ev = cast.events[next.current]!;
        if (ev.kind === "o") out += ev.data;
        else {
          if (out) h.write(out);
          out = "";
          h.resize(ev.cols, ev.rows);
        }
        next.current++;
      }
      if (out) h.write(out);
    },
    [cast],
  );

  const seek = useCallback(
    (to: number) => {
      term.current?.reset();
      term.current?.resize(cast.cols, cast.rows);
      next.current = 0;
      apply(to);
      run.current = { wall: performance.now(), from: to };
      setPos(to);
    },
    [apply, cast],
  );

  useEffect(() => {
    if (!playing) return;
    let frame = 0;
    let shown = 0;
    run.current = { wall: performance.now(), from: pos };
    const tick = (now: number) => {
      const at = Math.min(cast.length, run.current.from + ((now - run.current.wall) / 1000) * speedRef.current);
      apply(at);
      // The slider moves a few times a second, not at every frame.
      if (now - shown > 100 || at >= cast.length) {
        shown = now;
        setPos(at);
      }
      if (at >= cast.length) {
        setPlaying(false);
        return;
      }
      frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
    // pos is read once, when play starts; speed changes are picked up through the ref.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [playing, apply, cast]);

  // Speed changes mid-play: the clock continues from where it is, at the new pace.
  useEffect(() => {
    run.current = { wall: performance.now(), from: pos };
    // A new pace from the position of the moment; the position moving is not a new pace.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [speed]);

  const onReady = useCallback(
    (h: TermHandle) => {
      term.current = h;
      setReady(true);
    },
    [],
  );

  const atEnd = pos >= cast.length;
  const toggle = () => {
    if (playing) setPlaying(false);
    else {
      if (atEnd) seek(0);
      setPlaying(true);
    }
  };
  return (
    <div>
      <div className="player-bar mb-3">
        <Button variant="primary" size="sm" disabled={!ready || cast.events.length === 0} onClick={toggle}>
          {playing ? <Pause size={16} aria-hidden /> : <Play size={16} aria-hidden />} {playing ? t("support.player.pause") : atEnd && pos > 0 ? t("support.player.again") : t("support.player.play")}
        </Button>
        <Button size="sm" variant="ghost" disabled={!ready || pos === 0} onClick={() => { setPlaying(false); seek(0); }} aria-label={t("support.player.restart")} title={t("support.player.restart")}>
          <RotateCcw size={16} aria-hidden />
        </Button>
        <input
          type="range"
          className="player-range"
          min={0}
          max={Math.max(cast.length, 0.1)}
          step={0.1}
          value={pos}
          disabled={!ready || cast.length === 0}
          aria-label={t("support.player.position")}
          aria-valuetext={`${clockText(pos)} / ${clockText(cast.length)}`}
          onChange={(e) => {
            setPlaying(false);
            seek(Number(e.target.value));
          }}
        />
        <span className="num player-time" aria-hidden>
          {clockText(pos)} / {clockText(cast.length)}
        </span>
        <Segmented<Speed>
          label={t("support.player.speed")}
          value={speed}
          onChange={setSpeed}
          options={[
            { value: "1", label: "1×" },
            { value: "2", label: "2×" },
            { value: "4", label: "4×" },
          ]}
        />
      </div>
      <div className="player-screen">
        <Suspense fallback={<Skeleton style={{ height: 320, borderRadius: 12 }} />}>
          <Term onReady={onReady} size={{ cols: cast.cols, rows: cast.rows }} label={t("support.player.screen", { name })} />
        </Suspense>
      </div>
      <p className="mt-2 text-xs text-[var(--ink-500)]">{t("support.player.note", { n: IDLE_CAP })}</p>
    </div>
  );
}
