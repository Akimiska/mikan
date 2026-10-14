//! The support terminal's host side: `mikan terminal-agent`, which a systemd path unit starts
//! when the panel, or a node, writes data/<panel|node>/support/terminal/request. This is the
//! admin's "terminal" level of the support access: a shell on this server, for whoever holds
//! a live grant that names it. It is not an ssh daemon and listens on no network port.
//!
//!   request    {"grant_id": N, "target": "panel" | "node:<id>", "expires_at": <unix seconds>,
//!              "socket": "<name>.sock"}: one request is one session. The panel writes it
//!              after it checked the grant; the agent takes it away first and does not trust
//!              more of it than it needs: the time (never longer than a grant lasts), a
//!              target of the shape the panel uses and a plain file name.
//!   revoked    names the grants that ended (ids, whitespace apart; "*" or an empty file: all).
//!              The panel writes it when a grant is revoked; the session of that grant ends.
//!   <socket>   a unix socket in support/, made by the agent for the panel's user alone (0600),
//!              the one way into the shell. The session is the stream on it: frames of
//!              `type:u8 length:u32 (big endian) payload`, D data both ways (what the operator
//!              types, what the shell prints), R a new window size (cols:u16 rows:u16) to the
//!              agent, H hello (a JSON with the recording's name) and X the end (a JSON with
//!              the reason) from it.
//!   recordings/<grant>-<unix time>.cast   the whole session as asciicast v2 (input and
//!              output with times), for root and, to read, the panel's group. A recording that
//!              cannot be written ends the session; they are removed after 30 days.
//!
//! The shell runs in its own session, so its process group is killed whole when the session
//! ends: at the panel's letting go of the socket, 15 minutes without a byte in either
//! direction, the end of the grant (the time in the request) or its revocation. Whatever
//! escaped the group (a daemon started with setsid) is killed by the unit: it runs in its own
//! cgroup and systemd kills the cgroup when the agent ends. Nothing else is left behind: no
//! user, no key, no cron entry; the socket is removed when it has been used.
//!
//! support/ itself is root's (the panel cannot swap the recordings or the socket for its own);
//! only terminal/, where the panel writes its request, is the panel's. `prepare` makes the
//! three and `mikan update` runs it (panelfs::own_dirs).

use std::fs::File;
use std::io::{ErrorKind, Read, Write};
use std::os::fd::{AsRawFd, FromRawFd, OwnedFd, RawFd};
use std::os::unix::net::{UnixListener, UnixStream};
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result, bail};
use rustix::fs::{AtFlags, FileType, Gid, Mode, OFlags, Uid};
use rustix::io::Errno;
use serde::Deserialize;

use crate::DIR;
use crate::hostreq;
use crate::ops::Install;
use crate::panelfs::{Dir, PANEL_UID};

/// support/ below data/panel or data/node, root's; terminal/ and recordings/ below it.
pub const SUPPORT_DIR: &str = "support";
const TERMINAL_DIR: &str = "terminal";
const RECORDINGS_DIR: &str = "recordings";

/// A session ends after this long without a byte either way.
const IDLE: Duration = Duration::from_secs(15 * 60);
/// How often the agent looks at the clock, the revocation marker and the idle time when
/// nothing else wakes it.
const TICK: Duration = Duration::from_millis(500);
/// How long the agent waits for the panel to connect before it gives up (no shell is
/// started for nobody).
const ACCEPT_WAIT: Duration = Duration::from_secs(60);
/// Recordings are kept this long.
const KEEP: Duration = Duration::from_secs(30 * 24 * 3600);
/// A recording stops the session here: the panel hands the file to the admin in one answer,
/// which is capped (nodeapi.MaxResponse, 8 MiB).
const MAX_RECORDING: u64 = 7 << 20;
/// A grant lasts 24 hours at most; the request may not name a later end than that and a
/// little slack.
const MAX_LIFE: u64 = 24 * 3600 + 300;
/// What `revoked` may hold.
const REVOKED_LIMIT: u64 = 4096;
/// The largest frame either side sends.
pub const FRAME_MAX: usize = 64 << 10;
/// Output waiting for the panel beyond this makes the agent stop reading the shell until
/// the panel catches up.
const OUT_LIMIT: usize = 1 << 20;

const DATA: u8 = b'D';
const RESIZE: u8 = b'R';
const HELLO: u8 = b'H';
const EXIT: u8 = b'X';

const DIR_FLAGS: OFlags = OFlags::RDONLY.union(OFlags::DIRECTORY).union(OFlags::NOFOLLOW).union(OFlags::CLOEXEC);

/// What `prepare` makes of a directory's owner.
enum Owner {
    Root,
    Panel,
    /// Root's, the panel's group reads: the recordings.
    RootGroupPanel,
}

/// Makes support/, support/terminal/ and support/recordings/ in data (data/panel or
/// data/node) as they must be: support/ root's, 0755, so that the panel cannot rename or
/// replace what is in it; terminal/ the panel's (its request and the revocation marker);
/// recordings/ root's, 0750, readable for the panel's group. Run again it changes nothing.
/// A support/ or recordings/ that is not root's (the panel made its own in place of ours)
/// is an error: the terminal stays off until somebody looks at it.
pub fn prepare(data: &Dir) -> Result<()> {
    let support = make_dir(data.fd(), SUPPORT_DIR, 0o755, Owner::Root)?;
    make_dir(&support, TERMINAL_DIR, 0o755, Owner::Panel)?;
    make_dir(&support, RECORDINGS_DIR, 0o750, Owner::RootGroupPanel)?;
    Ok(())
}

fn errno(e: Errno, what: impl FnOnce() -> String) -> anyhow::Error {
    anyhow::Error::new(std::io::Error::from(e)).context(what())
}

fn make_dir(parent: &OwnedFd, name: &str, mode: u32, owner: Owner) -> Result<OwnedFd> {
    let fd = match rustix::fs::openat(parent, name, DIR_FLAGS, Mode::empty()) {
        Ok(fd) => fd,
        Err(Errno::NOENT) => {
            match rustix::fs::mkdirat(parent, name, Mode::from_raw_mode(mode)) {
                Ok(()) | Err(Errno::EXIST) => {}
                Err(e) => return Err(errno(e, || format!("create {name}"))),
            }
            rustix::fs::openat(parent, name, DIR_FLAGS, Mode::empty()).map_err(|e| errno(e, || format!("open {name}")))?
        }
        Err(Errno::LOOP | Errno::NOTDIR) => bail!("{name} is a link or not a directory"),
        Err(e) => return Err(errno(e, || format!("open {name}"))),
    };
    let me = rustix::process::geteuid();
    if !matches!(owner, Owner::Panel) {
        let st = rustix::fs::fstat(&fd).map_err(|e| errno(e, || format!("stat {name}")))?;
        if st.st_uid != me.as_raw() {
            bail!("{name} belongs to uid {}, not to root: the support terminal stays off until it is looked at", st.st_uid);
        }
    }
    if me.is_root() {
        let (uid, gid) = match owner {
            Owner::Root => (0, 0),
            Owner::Panel => (PANEL_UID, PANEL_UID),
            Owner::RootGroupPanel => (0, PANEL_UID),
        };
        rustix::fs::fchown(&fd, Some(Uid::from_raw(uid)), Some(Gid::from_raw(gid))).map_err(|e| errno(e, || format!("chown {name}")))?;
    }
    rustix::fs::fchmod(&fd, Mode::from_raw_mode(mode)).map_err(|e| errno(e, || format!("chmod {name}")))?;
    Ok(fd)
}

/// What one run needs; production() is the real one, tests make their own.
pub struct Params {
    /// /opt/mikan.
    pub root: PathBuf,
    pub node: bool,
    /// The shell and its arguments.
    pub shell: Vec<String>,
    /// The users that may be at the other end of the socket.
    pub peers: Vec<u32>,
    pub idle: Duration,
    pub tick: Duration,
    pub accept_wait: Duration,
    pub max_recording: u64,
}

impl Params {
    pub fn production(node: bool) -> Self {
        Self {
            root: PathBuf::from(DIR),
            node,
            shell: default_shell(|p| Path::new(p).exists()),
            peers: vec![PANEL_UID],
            idle: IDLE,
            tick: TICK,
            accept_wait: ACCEPT_WAIT,
            max_recording: MAX_RECORDING,
        }
    }
}

/// root's login shell: bash where there is one, sh otherwise.
fn default_shell(exists: impl Fn(&str) -> bool) -> Vec<String> {
    if exists("/bin/bash") { vec!["/bin/bash".into(), "-l".into()] } else { vec!["/bin/sh".into()] }
}

/// How a session ended.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum End {
    /// The panel let go of the socket (the operator closed the terminal, the grant was
    /// revoked and the panel noticed first, the panel went down).
    Closed,
    Idle,
    /// The time in the request.
    Expired,
    /// `revoked` named the grant.
    Revoked,
    /// The shell ended.
    Exited,
    /// The recording is full or cannot be written.
    Recording,
    /// Nobody connected.
    NoClient,
}

impl End {
    fn as_str(self) -> &'static str {
        match self {
            End::Closed => "closed",
            End::Idle => "idle",
            End::Expired => "expired",
            End::Revoked => "revoked",
            End::Exited => "exit",
            End::Recording => "recording",
            End::NoClient => "no_client",
        }
    }
}

#[derive(Debug)]
pub struct Outcome {
    pub grant_id: u64,
    pub target: String,
    pub end: End,
    pub recording: Option<String>,
    pub seconds: u64,
    pub exit_code: Option<i32>,
    pub pid: Option<u32>,
}

impl Outcome {
    fn summary(&self) -> String {
        format!(
            "support terminal of grant {} on {}: ended ({}) after {} s{}",
            self.grant_id,
            self.target,
            self.end.as_str(),
            self.seconds,
            self.recording.as_deref().map(|r| format!(", recorded in {r}")).unwrap_or_default()
        )
    }
}

/// `mikan terminal-agent`.
pub fn agent() -> Result<()> {
    let install = Install::load()?;
    if let Some(o) = run(&Params::production(install.node))? {
        crate::out(&o.summary());
    }
    Ok(())
}

/// What the panel asked.
#[derive(Debug, PartialEq, Eq)]
struct Request {
    grant_id: u64,
    target: String,
    expires_at: u64,
    socket: String,
}

#[derive(Deserialize)]
struct RawRequest {
    grant_id: u64,
    target: String,
    expires_at: u64,
    socket: String,
}

fn now_unix() -> u64 {
    SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs()).unwrap_or(0)
}

/// A server of a grant: the panel's own, or a node by its canonical number.
fn valid_target(t: &str) -> bool {
    if t == "panel" {
        return true;
    }
    let Some(n) = t.strip_prefix("node:") else { return false };
    !n.is_empty() && n.len() <= 18 && !n.starts_with('0') && n.bytes().all(|b| b.is_ascii_digit())
}

/// A file name for the socket: plain characters and the suffix, short enough for the
/// socket's path.
fn valid_socket(s: &str) -> bool {
    let Some(base) = s.strip_suffix(".sock") else { return false };
    (4..=48).contains(&base.len()) && base.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
}

/// The request in a file's bytes; Err says why it is none. The end may not be past, and not
/// further than a grant lasts.
fn parse_request(data: &[u8], now: u64) -> std::result::Result<Request, String> {
    let r: RawRequest = serde_json::from_slice(data).map_err(|_| "not a request".to_string())?;
    if r.grant_id == 0 {
        return Err("no grant".into());
    }
    if !valid_target(&r.target) {
        return Err("not a server of a grant".into());
    }
    if !valid_socket(&r.socket) {
        return Err("not a socket name".into());
    }
    if r.expires_at <= now {
        return Err("the grant has ended".into());
    }
    if r.expires_at > now + MAX_LIFE {
        return Err("the end is further than a grant lasts".into());
    }
    Ok(Request { grant_id: r.grant_id, target: r.target, expires_at: r.expires_at, socket: r.socket })
}

/// Whether `revoked` ends the grant's session: it names the grant or all of them, or it is
/// empty. Something that is there but is not a small regular file ends it too: the panel
/// writes this file, and ending a session is always safe.
fn revoked(term: &Dir, grant: u64) -> bool {
    match term.read("revoked", REVOKED_LIMIT) {
        Ok(None) => false,
        Ok(Some(data)) => {
            let text = String::from_utf8_lossy(&data);
            let mut words = text.split(|c: char| c.is_whitespace() || c == ',').filter(|w| !w.is_empty()).peekable();
            words.peek().is_none() || words.any(|w| w == "*" || w.parse() == Ok(grant))
        }
        Err(_) => true,
    }
}

/// support/ must be root's and closed to others: what is in it is trusted with a recording
/// and a socket.
fn check_private(support: &Dir) -> Result<()> {
    let st = rustix::fs::fstat(support.fd()).context("stat support/")?;
    if st.st_uid != rustix::process::geteuid().as_raw() || st.st_mode & 0o022 != 0 {
        bail!("support/ is not root's, or others may write to it: the terminal stays off until it is looked at");
    }
    Ok(())
}

/// Takes the request and runs the session it asks for. None: nothing was asked or what was
/// is no request (it is gone either way and no shell started).
pub fn run(p: &Params) -> Result<Option<Outcome>> {
    let kind = if p.node { "node" } else { "panel" };
    let Some(data) = Dir::open(&p.root, &format!("data/{kind}"), false)? else { return Ok(None) };
    let Some(support) = data.sub(SUPPORT_DIR)? else { return Ok(None) };
    let Some(term) = support.sub(TERMINAL_DIR)? else { return Ok(None) };
    let Some(raw) = hostreq::take_bytes(&term)? else { return Ok(None) };
    check_private(&support)?;
    let req = match parse_request(&raw, now_unix()) {
        Ok(r) => r,
        Err(why) => {
            crate::out(&format!("support terminal: the request is refused: {why}"));
            return Ok(None);
        }
    };
    if revoked(&term, req.grant_id) {
        crate::out(&format!("support terminal: grant {} was revoked before its session began", req.grant_id));
        return Ok(None);
    }
    let rec_fd = make_dir(support.fd(), RECORDINGS_DIR, 0o750, Owner::RootGroupPanel)?;
    prune(&rec_fd, now_unix());
    remove_stale_sockets(&support);

    let started = Instant::now();
    let listener = listen(&support, &req.socket)?;
    let accepted = accept(&listener, &term, &req, p);
    drop(listener);
    // One session: the socket goes with the connection.
    let _ = rustix::fs::unlinkat(support.fd(), req.socket.as_str(), AtFlags::empty());
    let mut outcome = Outcome {
        grant_id: req.grant_id,
        target: req.target.clone(),
        end: End::NoClient,
        recording: None,
        seconds: 0,
        exit_code: None,
        pid: None,
    };
    let conn = match accepted {
        Accepted::Conn(c) => c,
        Accepted::End(end) => {
            outcome.end = end;
            outcome.seconds = started.elapsed().as_secs();
            return Ok(Some(outcome));
        }
    };
    session(p, &req, &term, &rec_fd, conn, &mut outcome)?;
    outcome.seconds = started.elapsed().as_secs();
    Ok(Some(outcome))
}

/// The socket, made for the panel's user: root's while it is made (nobody else may connect
/// then, which needs the write permission), the panel's and 0600 before it is told of. The
/// directory is held open and the path goes through it, so that a rename of support/ after
/// the checks leads nowhere else.
fn listen(support: &Dir, name: &str) -> Result<UnixListener> {
    let _ = rustix::fs::unlinkat(support.fd(), name, AtFlags::empty());
    let path = format!("/proc/self/fd/{}/{name}", support.fd().as_raw_fd());
    let listener = UnixListener::bind(&path).with_context(|| format!("make the socket {name}"))?;
    if rustix::process::geteuid().is_root() {
        rustix::fs::chownat(support.fd(), name, Some(Uid::from_raw(PANEL_UID)), Some(Gid::from_raw(PANEL_UID)), AtFlags::SYMLINK_NOFOLLOW)
            .map_err(|e| errno(e, || format!("chown the socket {name}")))?;
    }
    rustix::fs::chmodat(support.fd(), name, Mode::from_raw_mode(0o600), AtFlags::empty())
        .map_err(|e| errno(e, || format!("chmod the socket {name}")))?;
    Ok(listener)
}

/// Sockets of agents that died without removing theirs: one agent runs at a time, so any
/// that is there is stale.
fn remove_stale_sockets(support: &Dir) {
    for name in support.names().unwrap_or_default() {
        if valid_socket(&name) {
            let _ = rustix::fs::unlinkat(support.fd(), name.as_str(), AtFlags::empty());
        }
    }
}

enum Accepted {
    Conn(UnixStream),
    End(End),
}

/// Waits for the panel to connect, for as long as the request is good.
fn accept(listener: &UnixListener, term: &Dir, req: &Request, p: &Params) -> Accepted {
    let _ = listener.set_nonblocking(true);
    let deadline = Instant::now() + p.accept_wait;
    loop {
        match listener.accept() {
            Ok((conn, _)) => match peer_uid(&conn) {
                Ok(uid) if p.peers.contains(&uid) => return Accepted::Conn(conn),
                // Not the panel's user: the socket's mode keeps others out, this is the second lock.
                _ => continue,
            },
            Err(e) if e.kind() == ErrorKind::WouldBlock => {}
            Err(_) => return Accepted::End(End::NoClient),
        }
        if now_unix() >= req.expires_at {
            return Accepted::End(End::Expired);
        }
        if revoked(term, req.grant_id) {
            return Accepted::End(End::Revoked);
        }
        if Instant::now() >= deadline {
            return Accepted::End(End::NoClient);
        }
        let mut fds = [libc::pollfd { fd: listener.as_raw_fd(), events: libc::POLLIN, revents: 0 }];
        let _ = poll(&mut fds, p.tick);
    }
}

fn peer_uid(s: &UnixStream) -> std::io::Result<u32> {
    // SAFETY: ucred is plain data; getsockopt fills it for a connected unix socket.
    unsafe {
        let mut cred: libc::ucred = std::mem::zeroed();
        let mut len = std::mem::size_of::<libc::ucred>() as libc::socklen_t;
        if libc::getsockopt(s.as_raw_fd(), libc::SOL_SOCKET, libc::SO_PEERCRED, (&raw mut cred).cast(), &mut len) != 0 {
            return Err(std::io::Error::last_os_error());
        }
        Ok(cred.uid)
    }
}

fn poll(fds: &mut [libc::pollfd], timeout: Duration) -> std::io::Result<usize> {
    let ms = timeout.as_millis().min(i32::MAX as u128) as libc::c_int;
    loop {
        // SAFETY: fds is a valid slice of pollfd for the length given.
        let n = unsafe { libc::poll(fds.as_mut_ptr(), fds.len() as libc::nfds_t, ms) };
        if n >= 0 {
            return Ok(n as usize);
        }
        let e = std::io::Error::last_os_error();
        if e.kind() != ErrorKind::Interrupted {
            return Err(e);
        }
    }
}

// ---- frames -------------------------------------------------------------------------

/// One frame, ready to send.
fn frame(kind: u8, payload: &[u8]) -> Vec<u8> {
    let mut f = Vec::with_capacity(5 + payload.len());
    f.push(kind);
    f.extend_from_slice(&(payload.len() as u32).to_be_bytes());
    f.extend_from_slice(payload);
    f
}

/// The frames the panel sends, put together from what arrives in pieces.
#[derive(Default)]
struct FrameReader {
    buf: Vec<u8>,
}

impl FrameReader {
    /// Adds bytes and returns the frames they complete. Err: a frame that is no frame of the
    /// panel's (a type it never sends, a payload beyond FRAME_MAX): the session ends.
    fn feed(&mut self, data: &[u8]) -> std::result::Result<Vec<(u8, Vec<u8>)>, &'static str> {
        self.buf.extend_from_slice(data);
        let mut out = Vec::new();
        let mut at = 0;
        loop {
            let rest = &self.buf[at..];
            if rest.len() < 5 {
                break;
            }
            let kind = rest[0];
            if kind != DATA && kind != RESIZE {
                return Err("a frame the panel does not send");
            }
            let len = u32::from_be_bytes([rest[1], rest[2], rest[3], rest[4]]) as usize;
            if len > FRAME_MAX {
                return Err("a frame too large");
            }
            if rest.len() < 5 + len {
                break;
            }
            out.push((kind, rest[5..5 + len].to_vec()));
            at += 5 + len;
        }
        self.buf.drain(..at);
        Ok(out)
    }
}

// ---- the recording ----------------------------------------------------------------

/// An asciicast v2 file: a header line, then `[seconds, "o"|"i"|"r", data]` lines.
struct Recording {
    file: File,
    started: Instant,
    size: u64,
    max: u64,
    carry_in: Vec<u8>,
    carry_out: Vec<u8>,
}

impl Recording {
    /// Makes the file in recordings/ (never over another, never through a link) and writes
    /// the header. Returns it with its name.
    fn create(rec_fd: &OwnedFd, grant: u64, target: &str, shell: &str, size: (u16, u16), max: u64) -> Result<(Self, String)> {
        let now = now_unix();
        let mut last = None;
        for step in 0..8 {
            let name = format!("{grant}-{}.cast", now + step);
            match rustix::fs::openat(
                rec_fd,
                name.as_str(),
                OFlags::WRONLY | OFlags::CREATE | OFlags::EXCL | OFlags::NOFOLLOW | OFlags::CLOEXEC,
                Mode::from_raw_mode(0o640),
            ) {
                Ok(fd) => {
                    let file = File::from(fd);
                    if rustix::process::geteuid().is_root() {
                        rustix::fs::fchown(&file, Some(Uid::from_raw(0)), Some(Gid::from_raw(PANEL_UID))).context("chown the recording")?;
                    }
                    rustix::fs::fchmod(&file, Mode::from_raw_mode(0o640)).context("chmod the recording")?;
                    let mut rec = Self { file, started: Instant::now(), size: 0, max, carry_in: Vec::new(), carry_out: Vec::new() };
                    let header = serde_json::json!({
                        "version": 2, "width": size.0, "height": size.1, "timestamp": now,
                        "title": format!("mikan support, grant {grant}, {target}"),
                        "env": {"TERM": TERM, "SHELL": shell},
                    });
                    rec.line(&format!("{header}\n"))?;
                    return Ok((rec, name));
                }
                Err(Errno::EXIST) => last = Some(Errno::EXIST),
                Err(e) => return Err(errno(e, || format!("create the recording {name}"))),
            }
        }
        bail!("the recording's name is taken: {:?}", last)
    }

    fn line(&mut self, text: &str) -> Result<()> {
        if self.size + text.len() as u64 > self.max {
            bail!("the recording is full");
        }
        self.file.write_all(text.as_bytes()).context("write the recording")?;
        self.size += text.len() as u64;
        Ok(())
    }

    fn event(&mut self, kind: &str, text: &str) -> Result<()> {
        let t = (self.started.elapsed().as_secs_f64() * 1e6).round() / 1e6;
        let line = serde_json::to_string(&(t, kind, text))?;
        self.line(&format!("{line}\n"))
    }

    /// Bytes the operator typed ("i") or the shell printed ("o"): text as far as it is text.
    fn data(&mut self, input: bool, bytes: &[u8]) -> Result<()> {
        let carry = if input { &mut self.carry_in } else { &mut self.carry_out };
        let text = utf8_chunk(carry, bytes);
        if text.is_empty() {
            return Ok(());
        }
        self.event(if input { "i" } else { "o" }, &text)
    }

    fn finish(self) {
        let _ = self.file.sync_all();
    }
}

/// The text in a chunk of bytes: what is not UTF-8 becomes U+FFFD, and the start of a
/// character that continues in the next chunk waits in carry.
fn utf8_chunk(carry: &mut Vec<u8>, data: &[u8]) -> String {
    carry.extend_from_slice(data);
    let mut out = String::new();
    let mut at = 0;
    loop {
        match std::str::from_utf8(&carry[at..]) {
            Ok(s) => {
                out.push_str(s);
                carry.clear();
                return out;
            }
            Err(e) => {
                let good = e.valid_up_to();
                out.push_str(std::str::from_utf8(&carry[at..at + good]).unwrap_or_default());
                at += good;
                match e.error_len() {
                    Some(n) => {
                        out.push('\u{FFFD}');
                        at += n;
                    }
                    None => {
                        // Cut in the middle of a character: it is finished by the next chunk.
                        carry.drain(..at);
                        return out;
                    }
                }
            }
        }
    }
}

/// Removes recordings older than KEEP: files of the recordings' own name only, never
/// through a link.
fn prune(rec_fd: &OwnedFd, now: u64) {
    let Ok(dir) = rustix::fs::Dir::read_from(rec_fd) else { return };
    let cut = now as i64 - KEEP.as_secs() as i64;
    for entry in dir.flatten() {
        let name = entry.file_name().to_owned();
        let Ok(text) = name.to_str() else { continue };
        if !is_recording_name(text) {
            continue;
        }
        let Ok(st) = rustix::fs::statat(rec_fd, name.as_c_str(), AtFlags::SYMLINK_NOFOLLOW) else { continue };
        if FileType::from_raw_mode(st.st_mode) == FileType::RegularFile && st.st_mtime < cut {
            let _ = rustix::fs::unlinkat(rec_fd, name.as_c_str(), AtFlags::empty());
        }
    }
}

/// `<grant>-<unix time>.cast`: what the agent names a recording, and the only names the panel
/// lists.
fn is_recording_name(n: &str) -> bool {
    let Some(base) = n.strip_suffix(".cast") else { return false };
    let Some((a, b)) = base.split_once('-') else { return false };
    let digits = |s: &str| !s.is_empty() && s.len() <= 20 && s.bytes().all(|c| c.is_ascii_digit());
    digits(a) && digits(b)
}

// ---- the shell --------------------------------------------------------------------

const TERM: &str = "xterm-256color";

/// The shell on a pty, in its own session and so its own process group.
struct Shell {
    master: File,
    child: Child,
    pgid: libc::pid_t,
}

fn set_flag(fd: RawFd, flag: libc::c_int) -> std::io::Result<()> {
    // SAFETY: fcntl on a descriptor this process owns.
    unsafe {
        let cur = libc::fcntl(fd, libc::F_GETFL);
        if cur < 0 || libc::fcntl(fd, libc::F_SETFL, cur | flag) < 0 {
            return Err(std::io::Error::last_os_error());
        }
    }
    Ok(())
}

fn winsize(cols: u16, rows: u16) -> libc::winsize {
    libc::winsize { ws_row: rows, ws_col: cols, ws_xpixel: 0, ws_ypixel: 0 }
}

impl Shell {
    fn spawn(argv: &[String], grant: u64) -> Result<Self> {
        let Some((prog, args)) = argv.split_first() else { bail!("no shell") };
        let (mut m, mut s): (libc::c_int, libc::c_int) = (-1, -1);
        let ws = winsize(80, 24);
        // SAFETY: openpty fills two descriptors; the window size is a valid struct.
        if unsafe { libc::openpty(&mut m, &mut s, std::ptr::null_mut(), std::ptr::null_mut(), &ws) } != 0 {
            return Err(std::io::Error::last_os_error()).context("open a pty");
        }
        // SAFETY: both are fresh descriptors nobody else owns.
        let (master, slave) = unsafe { (OwnedFd::from_raw_fd(m), OwnedFd::from_raw_fd(s)) };
        for fd in [master.as_raw_fd(), slave.as_raw_fd()] {
            // SAFETY: fcntl on descriptors owned here.
            unsafe { libc::fcntl(fd, libc::F_SETFD, libc::FD_CLOEXEC) };
        }
        let mut cmd = Command::new(prog);
        cmd.args(args)
            .env_clear()
            .env("TERM", TERM)
            .env("HOME", "/root")
            .env("USER", "root")
            .env("LOGNAME", "root")
            .env("SHELL", prog)
            .env("LANG", "C.UTF-8")
            .env("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
            .env("MIKAN_SUPPORT_GRANT", grant.to_string())
            .current_dir(if Path::new("/root").read_dir().is_ok() { "/root" } else { "/" })
            .stdin(Stdio::from(slave.try_clone()?))
            .stdout(Stdio::from(slave.try_clone()?))
            .stderr(Stdio::from(slave));
        // SAFETY: only async-signal-safe calls (setsid, ioctl) between fork and exec. The
        // shell becomes the leader of a session of its own, with the pty as its terminal.
        unsafe {
            cmd.pre_exec(|| {
                if libc::setsid() < 0 {
                    return Err(std::io::Error::last_os_error());
                }
                if libc::ioctl(0, libc::TIOCSCTTY as _, 0) < 0 {
                    return Err(std::io::Error::last_os_error());
                }
                Ok(())
            });
        }
        let child = cmd.spawn().with_context(|| format!("start {prog}"))?;
        // Our copies of the slave end go: the master reads end-of-file when the shell's do.
        drop(cmd);
        let pgid = child.id() as libc::pid_t;
        set_flag(master.as_raw_fd(), libc::O_NONBLOCK)?;
        Ok(Self { master: File::from(master), child, pgid })
    }

    fn resize(&self, cols: u16, rows: u16) {
        let ws = winsize(cols, rows);
        // SAFETY: the master is a pty and ws is a valid winsize.
        unsafe { libc::ioctl(self.master.as_raw_fd(), libc::TIOCSWINSZ as _, &ws) };
    }

    /// Whether the shell has ended, without reaping it: its pid, and so its process group's
    /// number, stays ours until the last kill has been sent.
    fn ended(&self) -> bool {
        // SAFETY: waitid with WNOWAIT only looks; info is plain data.
        unsafe {
            let mut info: libc::siginfo_t = std::mem::zeroed();
            let r = libc::waitid(libc::P_PID, self.pgid as libc::id_t, &mut info, libc::WEXITED | libc::WNOHANG | libc::WNOWAIT);
            r == 0 && info.si_pid() != 0
        }
    }

    fn killpg(&self, sig: libc::c_int) {
        // SAFETY: kill of the process group the shell leads.
        unsafe { libc::kill(-self.pgid, sig) };
    }

    /// Ends the whole group: a hangup and a moment to leave, then a kill for whatever is
    /// left. Returns the shell's exit code (None: killed by a signal).
    fn kill(&mut self) -> Option<i32> {
        self.killpg(libc::SIGHUP);
        self.killpg(libc::SIGCONT);
        let until = Instant::now() + Duration::from_secs(1);
        while Instant::now() < until && !self.ended() {
            std::thread::sleep(Duration::from_millis(20));
        }
        self.killpg(libc::SIGKILL);
        self.child.wait().ok().and_then(|s| s.code())
    }
}

/// How much one read takes from the shell or the panel.
const CHUNK: usize = 16 << 10;

enum Pump {
    Open,
    /// The other side is gone (end of file, or an error: the pty's EIO is the shell's end).
    Gone,
}

/// Reads what is there without blocking.
fn read_some(src: &mut impl Read, buf: &mut [u8]) -> (usize, Pump) {
    match src.read(buf) {
        Ok(0) => (0, Pump::Gone),
        Ok(n) => (n, Pump::Open),
        Err(e) if matches!(e.kind(), ErrorKind::WouldBlock | ErrorKind::Interrupted) => (0, Pump::Open),
        Err(_) => (0, Pump::Gone),
    }
}

/// Writes as much of pending as goes without blocking and drops what went. false: the other
/// side is gone.
fn write_some(dst: &mut impl Write, pending: &mut Vec<u8>) -> bool {
    while !pending.is_empty() {
        match dst.write(pending) {
            Ok(0) => return false,
            Ok(n) => {
                pending.drain(..n);
            }
            Err(e) if e.kind() == ErrorKind::WouldBlock => return true,
            Err(e) if e.kind() == ErrorKind::Interrupted => {}
            Err(_) => return false,
        }
    }
    true
}

/// Tells the panel the session cannot begin.
fn refuse(conn: &mut UnixStream) {
    let _ = conn.set_write_timeout(Some(Duration::from_secs(1)));
    let _ = conn.write_all(&frame(EXIT, br#"{"reason":"error","code":null}"#));
}

/// The session: the shell, the recording and the stream to the panel, until one of the ways
/// it ends.
fn session(p: &Params, req: &Request, term: &Dir, rec_fd: &OwnedFd, conn: UnixStream, out: &mut Outcome) -> Result<()> {
    let mut conn = conn;
    // No recording, no shell: a session that cannot be written down does not begin.
    let (mut rec, rec_name) = match Recording::create(rec_fd, req.grant_id, &req.target, &p.shell[0], (80, 24), p.max_recording) {
        Ok(r) => r,
        Err(e) => {
            refuse(&mut conn);
            return Err(e);
        }
    };
    out.recording = Some(rec_name.clone());
    let mut shell = match Shell::spawn(&p.shell, req.grant_id) {
        Ok(s) => s,
        Err(e) => {
            rec.finish();
            refuse(&mut conn);
            return Err(e);
        }
    };
    out.pid = Some(shell.pgid as u32);
    conn.set_nonblocking(true)?;

    let mut frames = FrameReader::default();
    let mut to_conn: Vec<u8> = Vec::new();
    let mut to_shell: Vec<u8> = Vec::new();
    let hello = serde_json::json!({"recording": rec_name, "expires_at": req.expires_at});
    to_conn.extend(frame(HELLO, hello.to_string().as_bytes()));
    let banner = b"\r\n*** mikan support: this session is recorded and the server's owner can watch it ***\r\n";
    to_conn.extend(frame(DATA, banner));
    let _ = rec.data(false, banner);

    let mut buf = vec![0u8; CHUNK];
    let mut last = Instant::now();
    let mut shell_gone = false;
    let end = 'run: loop {
        let mut conn_events = libc::POLLIN;
        if !to_conn.is_empty() {
            conn_events |= libc::POLLOUT;
        }
        let mut shell_events = 0;
        if !shell_gone && to_conn.len() <= OUT_LIMIT {
            shell_events |= libc::POLLIN;
        }
        if !to_shell.is_empty() {
            shell_events |= libc::POLLOUT;
        }
        let mut fds = [
            libc::pollfd { fd: conn.as_raw_fd(), events: conn_events, revents: 0 },
            libc::pollfd { fd: shell.master.as_raw_fd(), events: shell_events, revents: 0 },
        ];
        if poll(&mut fds, p.tick).is_err() {
            break 'run End::Closed;
        }
        // The panel: what the operator typed, a new window size, or the end of the stream.
        if fds[0].revents & (libc::POLLIN | libc::POLLHUP | libc::POLLERR) != 0 {
            let (n, state) = read_some(&mut conn, &mut buf);
            if n > 0 {
                last = Instant::now();
                match frames.feed(&buf[..n]) {
                    Ok(list) => {
                        for (kind, payload) in list {
                            if kind == RESIZE && payload.len() == 4 {
                                let cols = u16::from_be_bytes([payload[0], payload[1]]).clamp(1, 1000);
                                let rows = u16::from_be_bytes([payload[2], payload[3]]).clamp(1, 1000);
                                shell.resize(cols, rows);
                                if rec.event("r", &format!("{cols}x{rows}")).is_err() {
                                    break 'run End::Recording;
                                }
                            } else if kind == DATA {
                                if rec.data(true, &payload).is_err() {
                                    break 'run End::Recording;
                                }
                                to_shell.extend_from_slice(&payload);
                            }
                        }
                    }
                    Err(_) => break 'run End::Closed,
                }
            }
            if matches!(state, Pump::Gone) {
                break 'run End::Closed;
            }
        }
        if !to_conn.is_empty() && !write_some(&mut conn, &mut to_conn) {
            break 'run End::Closed;
        }
        // The shell: what it printed.
        if !shell_gone && fds[1].revents & (libc::POLLIN | libc::POLLHUP | libc::POLLERR) != 0 {
            let (n, state) = read_some(&mut shell.master, &mut buf);
            if n > 0 {
                last = Instant::now();
                if rec.data(false, &buf[..n]).is_err() {
                    break 'run End::Recording;
                }
                to_conn.extend(frame(DATA, &buf[..n]));
            }
            if matches!(state, Pump::Gone) {
                shell_gone = true;
            }
        }
        if !to_shell.is_empty() && !write_some(&mut shell.master, &mut to_shell) {
            shell_gone = true;
        }
        if !to_conn.is_empty() && !write_some(&mut conn, &mut to_conn) {
            break 'run End::Closed;
        }
        if shell_gone || shell.ended() {
            // What the shell printed last is still in the pty.
            while let (n, Pump::Open) = read_some(&mut shell.master, &mut buf) {
                if n == 0 {
                    break;
                }
                let _ = rec.data(false, &buf[..n]);
                to_conn.extend(frame(DATA, &buf[..n]));
            }
            break 'run End::Exited;
        }
        if now_unix() >= req.expires_at {
            break 'run End::Expired;
        }
        if revoked(term, req.grant_id) {
            break 'run End::Revoked;
        }
        if last.elapsed() >= p.idle {
            break 'run End::Idle;
        }
    };
    out.end = end;
    out.exit_code = shell.kill();
    rec.finish();
    // Whatever is left of the output, and the reason, for the panel to show; it may be gone.
    let _ = conn.set_nonblocking(false);
    let _ = conn.set_write_timeout(Some(Duration::from_secs(1)));
    let reason = serde_json::json!({"reason": end.as_str(), "code": out.exit_code});
    to_conn.extend(frame(EXIT, reason.to_string().as_bytes()));
    let _ = conn.write_all(&to_conn);
    Ok(())
}

#[cfg(test)]
mod tests;
