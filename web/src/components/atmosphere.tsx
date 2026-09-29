/**
 * A rose-engine band, as on watch dials and banknotes: n copies of r = R + A·sin(m(θ − s)),
 * their phases spread over part of a petal, weave a ribbon of m petals.
 */
type Band = { r: number; a: number; m: number; n: number; spread: number; off: number };

// Ten petals, like a mandarin's segments: two ribbons crossing, a small star inside.
const BANDS: Band[] = [
  { r: 330, a: 110, m: 10, n: 14, spread: 0.3, off: 0 },
  { r: 330, a: 110, m: 10, n: 14, spread: 0.3, off: 0.5 },
  { r: 150, a: 40, m: 10, n: 8, spread: 0.25, off: 0.25 },
];

/** The band's curves as SVG paths of cubic pieces, eight a petal: smooth at any size. */
function band({ r, a, m, n, spread, off }: Band): string[] {
  const pieces = 8 * m;
  const h = (2 * Math.PI) / pieces;
  const f = (x: number) => Math.round(x * 10) / 10;
  return Array.from({ length: n }, (_, k) => {
    const s = ((2 * Math.PI) / m) * ((k / n) * spread + off);
    // A point of the curve and its derivative by θ.
    const at = (t: number) => {
      const rr = r + a * Math.sin(m * (t - s));
      const dr = a * m * Math.cos(m * (t - s));
      const cos = Math.cos(t);
      const sin = Math.sin(t);
      return { x: rr * cos, y: rr * sin, dx: dr * cos - rr * sin, dy: dr * sin + rr * cos };
    };
    let p = at(0);
    let d = `M${f(p.x)} ${f(p.y)}`;
    for (let i = 1; i <= pieces; i++) {
      const q = at(i * h);
      d += `C${f(p.x + (p.dx * h) / 3)} ${f(p.y + (p.dy * h) / 3)} ${f(q.x - (q.dx * h) / 3)} ${f(q.y - (q.dy * h) / 3)} ${f(q.x)} ${f(q.y)}`;
      p = q;
    }
    return d + "Z";
  });
}

const ROSETTE = BANDS.flatMap(band);

/**
 * The backdrop behind the glass: citrus light drifting slowly, a guilloché rosette in
 * hairlines, paper grain. The rosette is drawn once and shown twice.
 */
export function Atmosphere() {
  return (
    <div className="atmo" aria-hidden>
      <span className="glow glow-warm" />
      <span className="glow glow-cool" />
      <svg className="rosette rosette-main" viewBox="-500 -500 1000 1000">
        <defs>
          <linearGradient id="atmo-ink" gradientUnits="userSpaceOnUse" x1="-500" y1="-500" x2="500" y2="500">
            <stop offset="0" stopColor="#f07a2e" />
            <stop offset=".55" stopColor="#e0a021" />
            <stop offset="1" stopColor="#2b8c9e" />
          </linearGradient>
          <g id="atmo-rosette" fill="none" stroke="url(#atmo-ink)" strokeWidth={1}>
            <circle r="470" vectorEffect="non-scaling-stroke" />
            <circle r="478" vectorEffect="non-scaling-stroke" />
            {ROSETTE.map((d, i) => (
              <path key={i} d={d} vectorEffect="non-scaling-stroke" />
            ))}
          </g>
        </defs>
        <use href="#atmo-rosette" />
      </svg>
      <svg className="rosette rosette-echo" viewBox="-500 -500 1000 1000">
        <use href="#atmo-rosette" />
      </svg>
      <span className="grain" />
    </div>
  );
}

export function Logo({ size = 32 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden>
      <defs>
        <radialGradient id="mk-shade" cx=".36" cy=".32" r=".8">
          <stop offset="0" stopColor="#FFB27A" />
          <stop offset=".55" stopColor="#F07A2E" stopOpacity="0" />
        </radialGradient>
      </defs>
      <circle cx="16" cy="18" r="12" fill="#F07A2E" />
      <circle cx="16" cy="18" r="12" fill="url(#mk-shade)" />
      <path d="M16 7.5c.3-2.6 2.4-4.4 5.6-4.4-.2 2.9-2.4 4.7-5.6 4.4Z" fill="#2F9E6B" />
      <path d="M16 6.2v3" stroke="#1F7650" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  );
}
