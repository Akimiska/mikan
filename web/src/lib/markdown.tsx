import { Fragment, type AnchorHTMLAttributes, type ReactNode } from "react";
import { inlineMarkdown } from "./inline-md";

// The Markdown the admin writes for the subscription page (instructions, text blocks):
// headings, paragraphs, lists, quotes, code, rules and images, with the inline marks of
// release notes (inline-md.tsx) plus *italic*. Built as React nodes, never as HTML, so a
// text cannot put markup on the page; links and images go only to https.

type LinkProps = (href: string) => AnchorHTMLAttributes<HTMLAnchorElement>;

const IMAGE = /!\[([^\]]*)\]\(\s*(https:\/\/[^\s)]+)(?:\s+"[^"]*")?\s*\)/g;
const ITALIC = /(^|[^*\w])\*([^*\s][^*\n]*?)\*(?![*\w])|(^|[^_\w])_([^_\s][^_\n]*?)_(?![_\w])/g;

function inline(text: string, link?: LinkProps): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  // Images first: their alt text is not marked up, their link is not a link.
  for (const m of text.matchAll(IMAGE)) {
    const at = m.index ?? 0;
    if (at > last) out.push(...italics(text.slice(last, at), link));
    out.push(<img key={`i${at}`} src={m[2]} alt={m[1] ?? ""} loading="lazy" decoding="async" referrerPolicy="no-referrer" />);
    last = at + m[0].length;
  }
  if (last < text.length) out.push(...italics(text.slice(last), link));
  return out;
}

function italics(text: string, link?: LinkProps): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  for (const m of text.matchAll(ITALIC)) {
    const lead = m[1] ?? m[3] ?? "";
    const at = (m.index ?? 0) + lead.length;
    if (at > last) out.push(...marks(text.slice(last, at), link));
    out.push(<em key={`e${at}`}>{marks(m[2] ?? m[4] ?? "", link)}</em>);
    last = (m.index ?? 0) + m[0].length;
  }
  if (last < text.length) out.push(...marks(text.slice(last), link));
  return out;
}

/** inlineMarkdown with the caller's props on its links (the Mini App opens them outside). */
function marks(text: string, link?: LinkProps): ReactNode[] {
  return inlineMarkdown(text).map((n, i) => {
    if (link && typeof n === "object" && n !== null && "type" in n && n.type === "a") {
      const props = n.props as AnchorHTMLAttributes<HTMLAnchorElement>;
      return <a key={i} {...props} {...link(props.href ?? "")} />;
    }
    return <Fragment key={i}>{n}</Fragment>;
  });
}

/** Lines joined as typed: a line break in the text is one on the page. */
function lines(ls: string[], link?: LinkProps): ReactNode[] {
  return ls.flatMap((l, i) => (i === 0 ? inline(l, link) : [<br key={`b${i}`} />, ...inline(l, link)]));
}

const HEADING = /^(#{1,6})\s+(.*)$/;
const BULLET = /^\s*[-*+]\s+(.*)$/;
const NUMBER = /^\s*\d{1,3}[.)]\s+(.*)$/;
const QUOTE = /^\s*>\s?(.*)$/;
const RULE = /^\s*([-*_])(\s*\1){2,}\s*$/;
const FENCE = /^\s*```/;

/** The blocks of md as React nodes. */
export function Markdown({ text, className = "md", link }: { text: string; className?: string; link?: LinkProps }) {
  const src = text.replace(/\r\n?/g, "\n").split("\n");
  const out: ReactNode[] = [];
  let i = 0;
  const key = () => out.length;
  while (i < src.length) {
    const line = src[i]!;
    if (line.trim() === "") {
      i++;
      continue;
    }
    if (FENCE.test(line)) {
      const code: string[] = [];
      i++;
      while (i < src.length && !FENCE.test(src[i]!)) code.push(src[i++]!);
      i++;
      out.push(
        <pre key={key()}>
          <code>{code.join("\n")}</code>
        </pre>,
      );
      continue;
    }
    const h = HEADING.exec(line);
    if (h) {
      const level = Math.min(h[1]!.length, 3);
      const content = inline(h[2]!.replace(/\s+#+\s*$/, ""), link);
      out.push(level === 1 ? <h1 key={key()}>{content}</h1> : level === 2 ? <h2 key={key()}>{content}</h2> : <h3 key={key()}>{content}</h3>);
      i++;
      continue;
    }
    if (RULE.test(line)) {
      out.push(<hr key={key()} />);
      i++;
      continue;
    }
    const list = BULLET.test(line) ? BULLET : NUMBER.test(line) ? NUMBER : null;
    if (list) {
      // Items of one kind; an indented line goes on with the item above it.
      const items: string[][] = [];
      while (i < src.length && src[i]!.trim() !== "") {
        const m = list.exec(src[i]!);
        if (m) items.push([m[1]!]);
        else if (items.length && /^\s+\S/.test(src[i]!)) items[items.length - 1]!.push(src[i]!.trim());
        else break;
        i++;
      }
      const lis = items.map((it, n) => <li key={n}>{lines(it, link)}</li>);
      out.push(list === NUMBER ? <ol key={key()}>{lis}</ol> : <ul key={key()}>{lis}</ul>);
      continue;
    }
    if (QUOTE.test(line)) {
      const q: string[] = [];
      while (i < src.length && QUOTE.test(src[i]!)) q.push(QUOTE.exec(src[i++]!)![1]!);
      out.push(<blockquote key={key()}>{lines(q, link)}</blockquote>);
      continue;
    }
    const para: string[] = [];
    while (i < src.length && src[i]!.trim() !== "" && !HEADING.test(src[i]!) && !FENCE.test(src[i]!) && !BULLET.test(src[i]!) && !NUMBER.test(src[i]!) && !QUOTE.test(src[i]!) && !RULE.test(src[i]!)) {
      para.push(src[i++]!);
    }
    out.push(<p key={key()}>{lines(para, link)}</p>);
  }
  return <div className={className}>{out}</div>;
}
