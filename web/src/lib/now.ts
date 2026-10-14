import { useEffect, useState } from "react";

/** The current time, as a state that moves every `every` ms: a countdown that redraws itself. */
export function useNow(every = 30_000): number {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), every);
    return () => window.clearInterval(id);
  }, [every]);
  return now;
}
