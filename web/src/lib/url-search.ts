import { useEffect, useRef, useState } from "react";

/**
 * The text of a search field kept in the URL. What is typed reaches the URL after a pause
 * (commit gets the text); a different value that comes from the URL (Back, a link) replaces
 * what is typed. clear() empties the field at once, for a reset that empties the URL itself.
 */
export function useUrlSearchText(urlValue: string, commit: (text: string) => void, ms = 250) {
  const [text, setText] = useState(urlValue);
  // The last value this field put into the URL: another one came from outside.
  const written = useRef(urlValue);
  // The page hands a new function on every render; the timer must not restart on it.
  const latest = useRef(commit);
  useEffect(() => {
    latest.current = commit;
  });
  useEffect(() => {
    if (urlValue === written.current) return;
    written.current = urlValue;
    setText(urlValue);
  }, [urlValue]);
  useEffect(() => {
    if (text === written.current) return;
    const timer = window.setTimeout(() => {
      written.current = text;
      latest.current(text);
    }, ms);
    return () => window.clearTimeout(timer);
  }, [text, ms]);
  const clear = () => {
    written.current = "";
    setText("");
  };
  return { text, setText, clear };
}
