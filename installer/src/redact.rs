//! Cuts secrets out of text before it leaves the server: the report `mikan doctor --json`
//! writes for the panel, and what `mikan support` sends. The same rules as the panel's and
//! the node's log buffers (internal/logbuf Redact): tokens and API keys, passwords, cookies
//! and bearer headers, share and subscription links, private keys, UUIDs and the random
//! segments of paths. It errs toward cutting too much: the reader is not the admin.

/// What takes the place of a secret.
pub const MARK: &str = "[redacted]";

/// A rule: at a position (with the character before it) either nothing, or how many bytes
/// it takes and what stands in their place.
type Rule = fn(prev: Option<char>, rest: &str) -> Option<(usize, String)>;

const RULES: [Rule; 12] =
    [private_key, share_link, userinfo, bearer, jwt, name_value, prefixed, bot_token, base64_key, uuid, path_segment, long_token];

/// Cuts every secret out of s.
pub fn text(s: &str) -> String {
    RULES.iter().fold(s.to_owned(), |acc, rule| scan(&acc, *rule))
}

/// text, then the words only the caller knows (the values of the server's own .env): each
/// one of six characters or more goes wherever it stands.
pub fn text_with(s: &str, words: &[String]) -> String {
    let mut out = text(s);
    for w in words.iter().filter(|w| w.len() >= 6) {
        out = out.replace(w.as_str(), MARK);
    }
    out
}

fn scan(s: &str, rule: Rule) -> String {
    let mut out = String::with_capacity(s.len());
    let mut prev: Option<char> = None;
    let mut i = 0;
    while i < s.len() {
        let rest = &s[i..];
        if let Some((n, repl)) = rule(prev, rest).filter(|(n, _)| *n > 0) {
            prev = repl.chars().last().or(prev);
            out.push_str(&repl);
            i += n;
            continue;
        }
        let c = rest.chars().next().expect("a character is left");
        out.push(c);
        prev = Some(c);
        i += c.len_utf8();
    }
    out
}

fn is_word(c: char) -> bool {
    c.is_ascii_alphanumeric() || c == '_'
}

/// Like regex's \b before a word character: the one before is none or not a word one.
fn boundary(prev: Option<char>) -> bool {
    !prev.is_some_and(is_word)
}

/// The bytes at the start of s that fit.
fn run(s: &str, fits: impl Fn(char) -> bool) -> usize {
    s.find(|c: char| !fits(c)).unwrap_or(s.len())
}

/// Whitespace within a line: a name and its value are on the same one.
fn blank(c: char) -> bool {
    c.is_whitespace() && c != '\n' && c != '\r'
}

fn starts_with_ci(s: &str, word: &str) -> bool {
    s.get(..word.len()).is_some_and(|h| h.eq_ignore_ascii_case(word))
}

fn private_key(_: Option<char>, rest: &str) -> Option<(usize, String)> {
    const BEGIN: &str = "-----BEGIN ";
    const END: &str = "PRIVATE KEY-----";
    let after = rest.strip_prefix(BEGIN)?;
    let header = &after[..after.find("-----")?];
    if !header.ends_with("PRIVATE KEY") || !header.bytes().all(|b| b.is_ascii_uppercase() || b == b' ') {
        return None;
    }
    let body = BEGIN.len() + header.len() + 5;
    let n = rest[body..].find(END).map_or(rest.len(), |i| body + i + END.len());
    Some((n, "[redacted private key]".into()))
}

const SCHEMES: [&str; 20] = [
    "vless",
    "vmess",
    "trojan",
    "ssr",
    "ss",
    "hysteria2",
    "hysteria",
    "hy2",
    "tuic",
    "anytls",
    "wireguard",
    "socks5",
    "socks",
    "mieru",
    "trusttunnel",
    "clash",
    "happ",
    "v2rayng",
    "sing-box",
    "singbox",
];

/// A share link carries the credentials of a protocol, a subscription link the way into a
/// user's profile.
fn share_link(prev: Option<char>, rest: &str) -> Option<(usize, String)> {
    if !boundary(prev) {
        return None;
    }
    for s in SCHEMES {
        if starts_with_ci(rest, s) && rest[s.len()..].starts_with("://") {
            let tail = &rest[s.len() + 3..];
            let n = run(tail, |c| !c.is_whitespace() && !"\"'<>\\".contains(c));
            return Some((s.len() + 3 + n, format!("{}://{MARK}", &rest[..s.len()])));
        }
    }
    None
}

/// user:password@ in a URL.
fn userinfo(prev: Option<char>, rest: &str) -> Option<(usize, String)> {
    if !boundary(prev) || !rest.starts_with(|c: char| c.is_ascii_alphabetic()) {
        return None;
    }
    let scheme = run(rest, |c| c.is_ascii_alphanumeric() || "+.-".contains(c));
    let after = rest[scheme..].strip_prefix("://")?;
    let user = run(after, |c| !c.is_whitespace() && !"/@:\"'".contains(c));
    let pass = after[user..].strip_prefix(':')?;
    let pw = run(pass, |c| !c.is_whitespace() && !"/@\"'".contains(c));
    if user == 0 || pw == 0 || !pass[pw..].starts_with('@') {
        return None;
    }
    let n = scheme + 3 + user + 1 + pw + 1;
    Some((n, format!("{}://{MARK}@", &rest[..scheme])))
}

fn bearer(prev: Option<char>, rest: &str) -> Option<(usize, String)> {
    if !boundary(prev) {
        return None;
    }
    let word = ["bearer", "basic"].into_iter().find(|w| starts_with_ci(rest, w))?;
    let after = &rest[word.len()..];
    let gap = run(after, blank);
    let token = run(&after[gap..], |c| c.is_ascii_alphanumeric() || "._~+/=-".contains(c));
    if gap == 0 || token < 6 {
        return None;
    }
    Some((word.len() + gap + token, format!("{} {MARK}", &rest[..word.len()])))
}

fn jwt(prev: Option<char>, rest: &str) -> Option<(usize, String)> {
    let part = |c: char| c.is_ascii_alphanumeric() || c == '_' || c == '-';
    if !boundary(prev) {
        return None;
    }
    let a = rest.strip_prefix("eyJ")?;
    let n1 = run(a, part);
    let b = a[n1..].strip_prefix('.')?;
    let n2 = run(b, part);
    let c = b[n2..].strip_prefix('.')?;
    if n1 < 8 || n2 < 8 {
        return None;
    }
    Some((3 + n1 + 1 + n2 + 1 + run(c, part), MARK.into()))
}

/// Whether a name (token, api_key, Authorization…) holds a secret.
fn secret_name(name: &str) -> bool {
    const PARTS: [&str; 10] =
        ["token", "secret", "passwd", "password", "passphrase", "authorization", "cookie", "credential", "csrf", "signature"];
    const FLAT: [&str; 7] = ["apikey", "privatekey", "joinkey", "keyauth", "sublink", "suburl", "psk"];
    let lower = name.to_ascii_lowercase();
    if PARTS.iter().any(|p| lower.contains(p)) {
        return true;
    }
    let flat: String = lower.chars().filter(|c| !"_-.".contains(*c)).collect();
    // MIKAN_NODE_JOIN holds a node's join key, which carries its private key.
    FLAT.iter().any(|p| flat.contains(p)) || lower.split(['_', '-', '.']).any(|seg| seg.starts_with("key") || seg == "join")
}

/// name=value, name: value and "name": "value" of a line of text or JSON, where the name
/// says the value is a secret.
fn name_value(prev: Option<char>, rest: &str) -> Option<(usize, String)> {
    let part = |c: char| is_word(c) || c == '.' || c == '-';
    if prev.is_some_and(part) || !rest.starts_with(part) {
        return None;
    }
    let name = run(rest, part);
    if !secret_name(&rest[..name]) {
        return None;
    }
    let mut at = name;
    // JSON puts the name in quotes, which a quoted slog value escapes.
    for q in ["\\\"", "\""] {
        if rest[at..].starts_with(q) {
            at += q.len();
            break;
        }
    }
    at += run(&rest[at..], blank);
    if !rest[at..].starts_with(['=', ':']) {
        return None;
    }
    at += 1;
    at += run(&rest[at..], blank);
    let value = &rest[at..];
    let len = if let Some(v) = value.strip_prefix("\\\"") {
        2 + v.find("\\\"").map_or(v.len(), |i| i + 2)
    } else if let Some(v) = value.strip_prefix('"') {
        quoted(v, '"')
    } else if let Some(v) = value.strip_prefix('\'') {
        1 + v.find('\'').map_or(v.len(), |i| i + 1)
    } else {
        run(value, |c| !c.is_whitespace() && !",;&}\"']".contains(c))
    };
    (len > 0).then(|| (at + len, format!("{}{MARK}", &rest[..at])))
}

/// The length of a quoted value after its opening quote q, backslash escapes skipped, the
/// closing quote counted in (and the opening one).
fn quoted(after_open: &str, q: char) -> usize {
    let mut esc = false;
    for (i, c) in after_open.char_indices() {
        match c {
            _ if esc => esc = false,
            '\\' => esc = true,
            c if c == q => return 1 + i + 1,
            _ => {}
        }
    }
    1 + after_open.len()
}

/// The tokens the panel hands out: API keys, support access.
fn prefixed(prev: Option<char>, rest: &str) -> Option<(usize, String)> {
    if !boundary(prev) {
        return None;
    }
    let p = ["ms_", "mk_", "mp_"].into_iter().find(|p| rest.starts_with(p))?;
    let n = run(&rest[3..], |c| c.is_ascii_alphanumeric());
    (n >= 12).then(|| (3 + n, format!("{p}{MARK}")))
}

/// A Telegram bot's token, which is also in the URL of every Bot API call.
fn bot_token(_: Option<char>, rest: &str) -> Option<(usize, String)> {
    let digits = run(rest, |c| c.is_ascii_digit());
    if !(6..=12).contains(&digits) {
        return None;
    }
    let secret = rest[digits..].strip_prefix(':')?;
    let n = run(secret, |c| c.is_ascii_alphanumeric() || c == '_' || c == '-');
    (n >= 30).then(|| (digits + 1 + n, "[redacted bot token]".into()))
}

/// A WireGuard or WARP key: 32 bytes of base64.
fn base64_key(prev: Option<char>, rest: &str) -> Option<(usize, String)> {
    if !boundary(prev) {
        return None;
    }
    let n = run(rest, |c| c.is_ascii_alphanumeric() || c == '+' || c == '/');
    (n == 43 && rest[n..].starts_with('=')).then(|| (44, MARK.into()))
}

/// A user's UUID, the credentials of a protocol.
fn uuid(prev: Option<char>, rest: &str) -> Option<(usize, String)> {
    if !boundary(prev) || rest.len() < 36 || !rest.is_char_boundary(36) {
        return None;
    }
    let shape = [8, 4, 4, 4, 12];
    let mut at = 0;
    for (i, len) in shape.iter().enumerate() {
        if i > 0 {
            if !rest[at..].starts_with('-') {
                return None;
            }
            at += 1;
        }
        if run(&rest[at..], |c| c.is_ascii_hexdigit()) < *len {
            return None;
        }
        at += len;
    }
    (!rest[at..].starts_with(is_word)).then(|| (at, "[uuid]".into()))
}

/// A random-looking segment of a path or URL: the panel's secret paths and subscription
/// tokens. Words of letters only (file and unit names) are not.
fn path_segment(_: Option<char>, rest: &str) -> Option<(usize, String)> {
    let seg = rest.strip_prefix('/')?;
    let n = run(seg, |c| c.is_ascii_alphanumeric() || c == '_' || c == '-');
    let s = &seg[..n];
    (n >= 20 && s.bytes().any(|b| b.is_ascii_digit()) && s.bytes().any(|b| b.is_ascii_alphabetic())).then(|| (1 + n, format!("/{MARK}")))
}

/// A long random-looking word on its own: a key pasted into a command (`mikan join <key>`)
/// or printed bare. Hashes (all hex: an image digest, a commit) and words of letters only
/// are left.
fn long_token(prev: Option<char>, rest: &str) -> Option<(usize, String)> {
    if !boundary(prev) {
        return None;
    }
    let n = run(rest, |c| c.is_ascii_alphanumeric() || c == '_' || c == '-');
    let s = &rest[..n];
    let random = n >= 32
        && s.bytes().any(|b| b.is_ascii_digit())
        && s.bytes().any(|b| b.is_ascii_alphabetic())
        && !s.bytes().all(|b| b.is_ascii_hexdigit());
    random.then(|| (n, MARK.into()))
}

#[cfg(test)]
mod tests {
    use super::*;

    const TOK: &str = "AbCdEfGhIjKlMnOpQrStUv0123456789";

    // Every kind of secret the panel and the node handle must be gone, and what is around
    // it must stay: a report with nothing readable in it helps nobody.
    #[test]
    fn secrets_are_cut() {
        let wg = "A".repeat(43);
        let cases: Vec<(&str, String, String, &str)> = vec![
            ("support token", format!("support enter ms_{TOK} ok"), format!("ms_{TOK}"), "support enter"),
            ("api key", format!("key mk_{TOK} used"), format!("mk_{TOK}"), "used"),
            ("bearer header", format!("Authorization: Bearer {TOK}"), TOK.into(), "Authorization"),
            ("bearer in text", format!("got bearer {TOK} from 1.2.3.4"), TOK.into(), "1.2.3.4"),
            ("token field", format!("msg=\"login\" token={TOK} ip=1.2.3.4"), TOK.into(), "ip=1.2.3.4"),
            ("quoted password", "err=\"bad\" password=\"hunter 2 hunter\" user=root".into(), "hunter".into(), "user=root"),
            ("cookie", format!("cookie=mikan_session={TOK}"), TOK.into(), "cookie="),
            ("secret in JSON", format!("{{\"api_key\":\"{TOK}\",\"name\":\"x\"}}"), TOK.into(), "\"name\":\"x\""),
            ("JSON in a value", format!("body=\"{{\\\"token\\\":\\\"{TOK}\\\"}}\""), TOK.into(), "body="),
            (
                "vless link",
                format!("link vless://2f1b9f0e-1111-4222-8333-444455556666@host:443?security=reality&pbk={TOK}#name"),
                TOK.into(),
                "link vless://",
            ),
            ("hysteria link", format!("hy2://{TOK}@host:443/?obfs=salamander"), TOK.into(), "hy2://"),
            ("telegram token", format!("GET https://api.telegram.org/bot123456789:{TOK}/getUpdates"), TOK.into(), "getUpdates"),
            ("url password", "dial https://admin:hunter2pass@host:8080/x".into(), "hunter2pass".into(), "host:8080"),
            ("uuid", "user 2f1b9f0e-1111-4222-8333-444455556666 joined".into(), "2f1b9f0e-1111".into(), "joined"),
            ("subscription link", format!("GET https://panel.example.com:8443/Ab12Cd34Ef56/Zx{TOK} 200"), TOK.into(), "panel.example.com"),
            ("admin path", format!("request path=/{TOK}/api/v1/users"), TOK.into(), "/api/v1/users"),
            ("wireguard key", format!("warp private {wg}= done"), wg.clone(), "done"),
            (
                "jwt",
                "id_token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk x".into(),
                "eyJzdWIi".into(),
                " x",
            ),
            (
                "private key",
                "key -----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n-----END PRIVATE KEY----- end".into(),
                "MIIEvQ".into(),
                " end",
            ),
            ("join key", format!("MIKAN_NODE_JOIN={TOK}"), TOK.into(), "MIKAN_NODE_JOIN="),
        ];
        for (name, input, secret, keep) in cases {
            let got = text(&input);
            assert!(!got.contains(&secret), "{name}: the secret is still there: {got:?}");
            assert!(got.contains(keep), "{name}: too much is gone, {keep:?} is missing: {got:?}");
        }
    }

    // Ordinary lines of a report are not touched: names, ports, versions, commands.
    #[test]
    fn ordinary_lines_stay() {
        for line in [
            "✓ Docker            engine 28.0.1",
            "Container mikan-panel-1  Up 3 hours (healthy)",
            "✗ Disk              0.3 GB free in /opt/mikan",
            "    fix: ufw allow 31234/tcp; mikan logs node",
            "time=2026-10-14T10:00:00Z level=INFO msg=\"panel started\" listen=0.0.0.0:21355 version=0.5.1.1",
            "msg=update unit=mikan-update-request.service path=/opt/mikan/data/panel/update/request",
            "image ghcr.io/miroshka000/mikan-panel:0.5.1.0 pulled",
            "github.com resolves (2 addresses)",
            "Panel contact  the panel dials back on port 31234",
            "image ghcr.io/miroshka000/mikan-panel@sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0 runs",
            "commit 0123456789abcdef0123456789abcdef01234567 built",
            "unit mikan-update-request-and-a-very-long-name-of-letters-only started",
        ] {
            assert_eq!(text(line), line);
        }
    }

    // A key typed into a command, or printed bare, is cut by its look: long, random, not a hash.
    #[test]
    fn a_bare_key_is_cut() {
        let key = "eyAbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-AbCdEfGhIjKl";
        for line in [format!("fix: mikan join {key}"), key.to_owned(), format!("(key {key})")] {
            let got = text(&line);
            assert!(!got.contains("AbCdEfGhIjKl"), "{got}");
        }
        assert_eq!(text("fix: mikan join <new key from the panel's Nodes page>"), "fix: mikan join <new key from the panel's Nodes page>");
    }

    // Multi-byte text passes through whole.
    #[test]
    fn utf8_is_kept() {
        assert_eq!(text("Нода ✓ работает: порт 443"), "Нода ✓ работает: порт 443");
        assert_eq!(text("пароль password=секретный-пароль конец"), format!("пароль password={MARK} конец"));
    }

    #[test]
    fn known_words_go_too() {
        let words = vec!["s3cr3t-value".to_owned(), "abc".to_owned(), String::new()];
        assert_eq!(text_with("a s3cr3t-value b abc c", &words), format!("a {MARK} b abc c"));
    }
}
