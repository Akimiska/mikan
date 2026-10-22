import { useEffect, useState } from "react";

/** value once it has stood still for ms: a search being typed is asked of the server once,
 * not on every key. */
export function useDebounced<T>(value: T, ms = 300): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setSettled(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return settled;
}
