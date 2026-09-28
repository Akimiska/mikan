export function Atmosphere() {
  return (
    <div className="atmo" aria-hidden>
      <span className="blob b1" />
      <span className="blob b2" />
      <span className="blob b3" />
      <span className="blob b4" />
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
