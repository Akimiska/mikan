// How a user's Telegram identity reads in the People list: `username (telegram-id)` when
// the account has a username, else the first name with the id. The id only ever comes
// from the subscription's own tg link — a contact field is never shown as one.
export function tgLabel(u: { telegram?: { id: number; username: string; name: string } | null }): string | null {
  const tg = u.telegram;
  if (!tg || !tg.id) return null;
  const who = tg.username ? `@${tg.username}` : tg.name;
  const base = who ? `${who} ` : "";
  return `${base}(${tg.id})`.trim();
}
