use super::*;

use std::fs;
use std::os::unix::fs::{MetadataExt, PermissionsExt, symlink};
use std::thread::JoinHandle;

fn is_root() -> bool {
    rustix::process::geteuid().is_root()
}

fn euid() -> u32 {
    rustix::process::geteuid().as_raw()
}

fn tmpdir(name: &str) -> PathBuf {
    let d = std::env::temp_dir().join(format!("term-{name}-{}", std::process::id()));
    let _ = fs::remove_dir_all(&d);
    fs::create_dir_all(&d).unwrap();
    d
}

/// A server's directories as `mikan update` leaves them, and the parameters of a run over
/// them with a fake shell, a short idle time and no wait worth the name.
struct Rig {
    root: PathBuf,
    p: Params,
}

impl Rig {
    fn new(name: &str, shell: &[&str]) -> Self {
        let root = tmpdir(name);
        fs::create_dir_all(root.join("data/panel")).unwrap();
        let data = Dir::open(&root, "data/panel", false).unwrap().unwrap();
        prepare(&data).unwrap();
        let p = Params {
            root: root.clone(),
            node: false,
            shell: shell.iter().map(|s| s.replace("{root}", &root.display().to_string())).collect(),
            peers: vec![euid()],
            idle: Duration::from_secs(60),
            tick: Duration::from_millis(40),
            accept_wait: Duration::from_secs(20),
            max_recording: MAX_RECORDING,
        };
        Self { root, p }
    }

    fn support(&self) -> PathBuf {
        self.root.join("data/panel/support")
    }

    fn request_path(&self) -> PathBuf {
        self.support().join("terminal/request")
    }

    fn ask(&self, grant: u64, target: &str, lives: i64, socket: &str) {
        let at = (now_unix() as i64 + lives) as u64;
        let body = serde_json::json!({"grant_id": grant, "target": target, "expires_at": at, "socket": socket});
        fs::write(self.request_path(), body.to_string()).unwrap();
    }

    fn recordings(&self) -> Vec<String> {
        let mut v: Vec<String> =
            fs::read_dir(self.support().join("recordings")).unwrap().map(|e| e.unwrap().file_name().into_string().unwrap()).collect();
        v.sort();
        v
    }

    /// The agent runs on its own thread, as the unit would run it.
    fn start(&self) -> JoinHandle<Result<Option<Outcome>>> {
        let p = Params {
            root: self.p.root.clone(),
            node: self.p.node,
            shell: self.p.shell.clone(),
            peers: self.p.peers.clone(),
            idle: self.p.idle,
            tick: self.p.tick,
            accept_wait: self.p.accept_wait,
            max_recording: self.p.max_recording,
        };
        std::thread::spawn(move || run(&p))
    }

    /// The socket as the panel's side connects to it: waited for until the agent has made it.
    fn connect(&self, socket: &str) -> Client {
        let path = self.support().join(socket);
        for _ in 0..400 {
            if let Ok(s) = UnixStream::connect(&path) {
                s.set_read_timeout(Some(Duration::from_secs(10))).unwrap();
                return Client { s, text: String::new(), ended: None };
            }
            std::thread::sleep(Duration::from_millis(25));
        }
        panic!("no socket at {}", path.display());
    }
}

impl Drop for Rig {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.root);
    }
}

/// The panel's end of a session.
struct Client {
    s: UnixStream,
    /// Everything the shell printed so far.
    text: String,
    /// The reason of the X frame, once it came.
    ended: Option<String>,
}

impl Client {
    fn send(&mut self, kind: u8, payload: &[u8]) {
        self.s.write_all(&frame(kind, payload)).unwrap();
    }

    /// The next frame; None at the end of the stream.
    fn next(&mut self) -> Option<(u8, Vec<u8>)> {
        let mut head = [0u8; 5];
        self.s.read_exact(&mut head).ok()?;
        let len = u32::from_be_bytes([head[1], head[2], head[3], head[4]]) as usize;
        let mut payload = vec![0u8; len];
        self.s.read_exact(&mut payload).ok()?;
        match head[0] {
            DATA => self.text.push_str(&String::from_utf8_lossy(&payload)),
            EXIT => {
                self.ended = serde_json::from_slice::<serde_json::Value>(&payload).ok().and_then(|v| v["reason"].as_str().map(String::from))
            }
            _ => {}
        }
        Some((head[0], payload))
    }

    /// Reads until the shell has printed needle; false when the stream ended first.
    fn until(&mut self, needle: &str) -> bool {
        while !self.text.contains(needle) {
            if self.next().is_none() {
                return false;
            }
        }
        true
    }

    /// Reads to the end of the stream.
    fn drain(&mut self) {
        while self.next().is_some() {}
    }
}

/// A process that is there and running: a zombie nobody has reaped is as good as gone.
fn alive(pid: u32) -> bool {
    match fs::read_to_string(format!("/proc/{pid}/stat")) {
        Ok(s) => s.rsplit(") ").next().is_some_and(|rest| !rest.starts_with('Z') && !rest.starts_with('X')),
        Err(_) => false,
    }
}

fn wait_dead(pid: u32) -> bool {
    for _ in 0..100 {
        if !alive(pid) {
            return true;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    false
}

fn join(h: JoinHandle<Result<Option<Outcome>>>) -> Outcome {
    h.join().unwrap().unwrap().expect("a session ran")
}

/// A way to make a request no good, with what is wrong with it.
type Case<'a> = (&'a str, Box<dyn Fn() + 'a>);

fn assert_nothing_left(rig: &Rig) {
    let mut names: Vec<String> = fs::read_dir(rig.support()).unwrap().map(|e| e.unwrap().file_name().into_string().unwrap()).collect();
    names.sort();
    assert_eq!(names, ["recordings", "terminal"], "the socket or something else is left in support/");
    assert!(!rig.request_path().exists(), "the request is left for the unit to fire again");
}

// What the panel may ask for, and no more: a shape of each field, a time that is not past
// and not beyond a grant.
#[test]
fn requests_are_checked() {
    let now = 1_800_000_000;
    let ok = |extra: &str| format!(r#"{{"grant_id":7,"target":"panel","expires_at":{},"socket":"t-001.sock"{extra}}}"#, now + 600);
    let want = Request { grant_id: 7, target: "panel".into(), expires_at: now + 600, socket: "t-001.sock".into() };
    assert_eq!(parse_request(ok("").as_bytes(), now), Ok(want));
    // What a request says beside these is not read: no command, no path.
    assert!(parse_request(ok(r#","run":"rm -rf /","shell":"/bin/evil","cwd":"/etc""#).as_bytes(), now).is_ok());
    for target in ["node:12", "node:1"] {
        let body = ok("").replace("\"panel\"", &format!("\"{target}\""));
        assert!(parse_request(body.as_bytes(), now).is_ok(), "{target}");
    }
    for target in [
        "",
        "root",
        "node:",
        "node:0",
        "node:012",
        "node:+5",
        "node:-1",
        "node:1x",
        "node:1234567890123456789",
        "Panel",
        "panel ",
        "node:5/../x",
    ] {
        let body = ok("").replace("\"panel\"", &format!("\"{target}\""));
        assert!(parse_request(body.as_bytes(), now).is_err(), "target {target:?}");
    }
    for socket in [
        "",
        ".sock",
        "a.sock",
        "abc.sock",
        "x/../y.sock",
        "../abc-def.sock",
        "/etc/passwd.sock",
        "ab cd.sock",
        "abcd.sock/",
        "abcd",
        "abcd.soc",
        "ab\ncd.sock",
        &format!("{}.sock", "a".repeat(49)),
    ] {
        let body = ok("").replace("t-001.sock", &socket.replace('\n', "\\n"));
        assert!(parse_request(body.as_bytes(), now).is_err(), "socket {socket:?}");
    }
    assert!(
        parse_request(format!(r#"{{"grant_id":0,"target":"panel","expires_at":{},"socket":"t-001.sock"}}"#, now + 60).as_bytes(), now)
            .is_err(),
        "grant 0"
    );
    // The end: past, now, and further than a grant lasts are refused.
    for at in [now - 1, now, now + MAX_LIFE + 1] {
        let body = format!(r#"{{"grant_id":7,"target":"panel","expires_at":{at},"socket":"t-001.sock"}}"#);
        assert!(parse_request(body.as_bytes(), now).is_err(), "expires_at {at}");
    }
    assert!(
        parse_request(
            format!(r#"{{"grant_id":7,"target":"panel","expires_at":{},"socket":"t-001.sock"}}"#, now + MAX_LIFE).as_bytes(),
            now
        )
        .is_ok()
    );
    for junk in
        ["", "garbage", "[]", "null", "5", r#"{"grant_id":"7"}"#, r#"{"grant_id":-1,"target":"panel","expires_at":1,"socket":"a-b.sock"}"#]
    {
        assert!(parse_request(junk.as_bytes(), now).is_err(), "{junk:?}");
    }
}

// The whole of a session with a fake shell: the stream works both ways, the window size
// arrives, the recording holds what was typed and printed, the socket is the panel's and
// closed to others, and when the panel lets go the process group is killed whole and nothing
// stays on the disk but the recording.
#[test]
fn a_session_runs_is_recorded_and_leaves_nothing() {
    let rig = Rig::new("session", &["sh", "-c", "sleep 300 & echo bg=$!; echo ready; exec cat"]);
    rig.ask(7, "panel", 600, "t-session.sock");
    let h = rig.start();
    // Who may connect: the panel's user alone (root's agent made it, the panel's it is now).
    // The socket goes when the connection is made, so it is looked at first.
    let path = rig.support().join("t-session.sock");
    let mut meta = None;
    for _ in 0..200 {
        if let Ok(m) = fs::symlink_metadata(&path)
            && m.permissions().mode() & 0o777 == 0o600
            && (!is_root() || m.uid() == PANEL_UID)
        {
            meta = Some(m);
            break;
        }
        std::thread::sleep(Duration::from_millis(25));
    }
    let meta = meta.expect("the socket is closed to everybody but the panel's user");
    if is_root() {
        assert_eq!((meta.uid(), meta.gid()), (PANEL_UID, PANEL_UID));
    }
    let mut c = rig.connect("t-session.sock");
    assert!(c.until("ready"), "the shell printed: {:?}", c.text);
    assert!(c.text.contains("this session is recorded"), "the operator is told: {:?}", c.text);
    let bg: u32 = c.text.split("bg=").nth(1).unwrap().split_whitespace().next().unwrap().parse().unwrap();
    assert!(alive(bg), "the background process runs");
    c.send(DATA, b"hello there\n");
    assert!(c.until("hello there"), "{:?}", c.text);
    c.send(RESIZE, &[0, 100, 0, 30]);
    c.send(DATA, "прив\u{0451}т".as_bytes());
    std::thread::sleep(Duration::from_millis(150));
    drop(c);

    let out = join(h);
    assert_eq!(out.end, End::Closed);
    assert_eq!((out.grant_id, out.target.as_str()), (7, "panel"));
    assert!(wait_dead(bg), "the background process outlived the session");
    assert!(wait_dead(out.pid.unwrap()), "the shell outlived the session");
    assert_nothing_left(&rig);

    // The recording: asciicast v2, named for the grant, readable by the panel's group only.
    let names = rig.recordings();
    assert_eq!(names.len(), 1, "{names:?}");
    assert_eq!(names[0], out.recording.clone().unwrap());
    assert!(is_recording_name(&names[0]) && names[0].starts_with("7-"));
    let path = rig.support().join("recordings").join(&names[0]);
    let meta = fs::metadata(&path).unwrap();
    assert_eq!(meta.permissions().mode() & 0o777, 0o640);
    if is_root() {
        assert_eq!((meta.uid(), meta.gid()), (0, PANEL_UID), "root's, the panel's group reads");
    }
    let text = fs::read_to_string(&path).unwrap();
    let mut lines = text.lines();
    let head: serde_json::Value = serde_json::from_str(lines.next().unwrap()).unwrap();
    assert_eq!(head["version"], 2);
    assert!(head["timestamp"].as_u64().unwrap() > 1_700_000_000);
    assert!(head["title"].as_str().unwrap().contains("grant 7"));
    let (mut typed, mut printed, mut resized) = (String::new(), String::new(), Vec::new());
    for line in lines {
        let ev: serde_json::Value = serde_json::from_str(line).unwrap();
        assert!(ev[0].is_f64() || ev[0].is_u64());
        let data = ev[2].as_str().unwrap().to_string();
        match ev[1].as_str().unwrap() {
            "i" => typed += &data,
            "o" => printed += &data,
            "r" => resized.push(data),
            other => panic!("event {other}"),
        }
    }
    assert!(typed.contains("hello there\n") && typed.contains("прив\u{0451}т"), "input: {typed:?}");
    assert!(printed.contains("ready") && printed.contains("hello there") && printed.contains("recorded"), "output: {printed:?}");
    assert_eq!(resized, ["100x30"]);
}

#[test]
fn the_end_of_the_grant_ends_the_session() {
    let rig = Rig::new("expired", &["sh", "-c", "echo up; exec sleep 300"]);
    rig.ask(3, "panel", 2, "t-exp.sock");
    let h = rig.start();
    let mut c = rig.connect("t-exp.sock");
    assert!(c.until("up"));
    let t = Instant::now();
    c.drain();
    assert_eq!(c.ended.as_deref(), Some("expired"), "the panel is told why");
    let out = join(h);
    assert_eq!(out.end, End::Expired);
    assert!(t.elapsed() < Duration::from_secs(10), "it ended at the grant's end, not later");
    assert!(wait_dead(out.pid.unwrap()));
    assert_nothing_left(&rig);
}

#[test]
fn silence_ends_the_session() {
    let mut rig = Rig::new("idle", &["sh", "-c", "echo up; exec sleep 300"]);
    rig.p.idle = Duration::from_millis(500);
    rig.ask(4, "panel", 600, "t-idle.sock");
    let h = rig.start();
    let mut c = rig.connect("t-idle.sock");
    assert!(c.until("up"));
    c.drain();
    assert_eq!(c.ended.as_deref(), Some("idle"));
    let out = join(h);
    assert_eq!(out.end, End::Idle);
    assert!(wait_dead(out.pid.unwrap()));
    assert_nothing_left(&rig);
}

// A grant revoked in the panel ends its session even if the panel cannot close the socket
// (it is down); a marker that names other grants does not.
#[test]
fn the_revocation_marker_ends_the_session_of_its_grant_only() {
    let rig = Rig::new("revoked", &["sh", "-c", "echo up; exec cat"]);
    rig.ask(9, "panel", 600, "t-rev.sock");
    let marker = rig.support().join("terminal/revoked");
    // An old marker for other grants (and a word that is no id): the session starts and stays.
    fs::write(&marker, "3\n4,5 garbage 90").unwrap();
    let h = rig.start();
    let mut c = rig.connect("t-rev.sock");
    assert!(c.until("up"));
    std::thread::sleep(Duration::from_millis(300));
    c.send(DATA, b"still here\n");
    assert!(c.until("still here"), "an old marker ended another grant's session: {:?}", c.text);
    let t = Instant::now();
    fs::write(&marker, "3\n9\n").unwrap();
    c.drain();
    assert_eq!(c.ended.as_deref(), Some("revoked"));
    assert!(t.elapsed() < Duration::from_secs(5));
    let out = join(h);
    assert_eq!(out.end, End::Revoked);
    assert!(wait_dead(out.pid.unwrap()));
    assert_nothing_left(&rig);
}

// Nothing runs for a request that is already no good: no shell, no socket, no recording.
#[test]
fn a_request_that_is_no_good_starts_no_shell() {
    let rig = Rig::new("refused", &["sh", "-c", "touch {root}/ran"]);
    let ran = || rig.root.join("ran").exists();
    let sock = rig.support().join("t-no.sock");
    let marker = rig.support().join("terminal/revoked");
    let cases: Vec<Case> = vec![
        ("an end in the past", Box::new(|| rig.ask(5, "panel", -5, "t-no.sock"))),
        (
            "a grant named in the marker",
            Box::new(|| {
                rig.ask(5, "panel", 600, "t-no.sock");
                fs::write(&marker, "5").unwrap();
            }),
        ),
        (
            "an empty marker",
            Box::new(|| {
                rig.ask(5, "panel", 600, "t-no.sock");
                fs::write(&marker, "").unwrap();
            }),
        ),
        (
            "everything revoked",
            Box::new(|| {
                rig.ask(5, "panel", 600, "t-no.sock");
                fs::write(&marker, "*").unwrap();
            }),
        ),
        (
            "a marker that is a directory",
            Box::new(|| {
                rig.ask(5, "panel", 600, "t-no.sock");
                fs::create_dir(&marker).unwrap();
            }),
        ),
        ("a bad target", Box::new(|| rig.ask(5, "root", 600, "t-no.sock"))),
        ("a socket name that is a path", Box::new(|| rig.ask(5, "panel", 600, "../escape.sock"))),
        ("junk", Box::new(|| fs::write(rig.request_path(), "rm -rf /").unwrap())),
        ("a link", Box::new(|| symlink("/etc/passwd", rig.request_path()).unwrap())),
        ("a request too large", Box::new(|| fs::write(rig.request_path(), vec![b' '; 5000]).unwrap())),
    ];
    for (what, make) in cases {
        let _ = fs::remove_dir_all(&marker);
        let _ = fs::remove_file(&marker);
        let _ = fs::remove_file(rig.request_path());
        make();
        let r = run(&rig.p).unwrap();
        assert!(r.is_none(), "{what}: a session ran");
        assert!(!ran(), "{what}: the shell ran");
        assert!(!sock.exists(), "{what}: a socket was made");
        assert!(!rig.request_path().exists() && fs::symlink_metadata(rig.request_path()).is_err(), "{what}: the request stays");
        assert!(rig.recordings().is_empty(), "{what}: a recording was made");
    }
    assert!(fs::metadata("/etc/passwd").is_ok());
    // And when there is no request at all, nothing happens either.
    assert!(run(&rig.p).unwrap().is_none());
}

#[test]
fn no_shell_is_started_for_nobody() {
    let mut rig = Rig::new("nobody", &["sh", "-c", "touch {root}/ran"]);
    rig.p.accept_wait = Duration::from_millis(400);
    rig.ask(6, "panel", 600, "t-none.sock");
    let out = run(&rig.p).unwrap().unwrap();
    assert_eq!(out.end, End::NoClient);
    assert!(out.pid.is_none() && out.recording.is_none());
    assert!(!rig.support().join("ran").exists() && !rig.root.join("ran").exists());
    assert!(rig.recordings().is_empty());
    assert_nothing_left(&rig);
}

// Only the panel's user is let in: a connection of another is dropped and the agent keeps
// waiting, then gives up.
#[test]
fn another_user_at_the_socket_is_not_the_panel() {
    let mut rig = Rig::new("peer", &["sh", "-c", "touch {root}/ran; exec cat"]);
    rig.p.peers = vec![euid().wrapping_add(4242)];
    rig.p.accept_wait = Duration::from_millis(800);
    rig.ask(6, "panel", 600, "t-peer.sock");
    let h = rig.start();
    let mut c = rig.connect("t-peer.sock");
    assert!(c.next().is_none(), "a stranger was given a session");
    let out = join(h);
    assert_eq!(out.end, End::NoClient);
    assert!(!rig.root.join("ran").exists());
    assert_nothing_left(&rig);
}

#[test]
fn the_shell_ending_ends_the_session() {
    let rig = Rig::new("exit", &["sh", "-c", "echo bye; exit 3"]);
    rig.ask(8, "panel", 600, "t-exit.sock");
    let h = rig.start();
    let mut c = rig.connect("t-exit.sock");
    c.drain();
    assert!(c.text.contains("bye"), "{:?}", c.text);
    assert_eq!(c.ended.as_deref(), Some("exit"));
    let out = join(h);
    assert_eq!((out.end, out.exit_code), (End::Exited, Some(3)));
    assert_nothing_left(&rig);
}

// A session that cannot be written down does not go on.
#[test]
fn a_full_recording_ends_the_session() {
    let mut rig = Rig::new("full", &["sh", "-c", "exec cat"]);
    rig.p.max_recording = 700;
    rig.ask(2, "panel", 600, "t-full.sock");
    let h = rig.start();
    let mut c = rig.connect("t-full.sock");
    for _ in 0..40 {
        // the peer may be gone already
        if c.s.write_all(&frame(DATA, &[b'x'; 200])).is_err() {
            break;
        }
        std::thread::sleep(Duration::from_millis(20));
    }
    c.drain();
    assert_eq!(c.ended.as_deref(), Some("recording"));
    let out = join(h);
    assert_eq!(out.end, End::Recording);
    let size = fs::metadata(rig.support().join("recordings").join(out.recording.unwrap())).unwrap().len();
    assert!(size <= 700, "the recording is {size} bytes");
    assert!(wait_dead(out.pid.unwrap()));
    assert_nothing_left(&rig);
}

// No recording, no shell.
#[test]
fn a_recording_that_cannot_be_made_stops_the_session_before_the_shell() {
    let rig = Rig::new("norec", &["sh", "-c", "touch {root}/ran; exec cat"]);
    // Every name for this second and the next ones is taken.
    let rec = rig.support().join("recordings");
    for step in 0..16 {
        fs::write(rec.join(format!("1-{}.cast", now_unix() + step)), "").unwrap();
    }
    // a name that is a dangling link is taken too: the recording is never made through a link
    rig.ask(1, "panel", 600, "t-norec.sock");
    let h = rig.start();
    let mut c = rig.connect("t-norec.sock");
    c.drain();
    assert_eq!(c.ended.as_deref(), Some("error"));
    assert!(h.join().unwrap().is_err());
    assert!(!rig.root.join("ran").exists(), "the shell ran without a recording");
}

// support/ is root's: where it is not (the panel made its own), nothing is made or run.
#[test]
fn a_support_directory_that_is_not_roots_is_refused() {
    if !is_root() {
        return;
    }
    let rig = Rig::new("foreign", &["sh", "-c", "touch {root}/ran; exec cat"]);
    std::os::unix::fs::chown(rig.support(), Some(PANEL_UID), Some(PANEL_UID)).unwrap();
    rig.ask(1, "panel", 600, "t-foreign.sock");
    assert!(run(&rig.p).is_err());
    assert!(!rig.support().join("t-foreign.sock").exists());
    assert!(!rig.root.join("ran").exists());
    // prepare says so too: it does not take a directory the panel owns for its own.
    let data = Dir::open(&rig.root, "data/panel", false).unwrap().unwrap();
    assert!(prepare(&data).is_err());
    // Writable by others is as bad.
    std::os::unix::fs::chown(rig.support(), Some(0), Some(0)).unwrap();
    fs::set_permissions(rig.support(), fs::Permissions::from_mode(0o777)).unwrap();
    rig.ask(1, "panel", 600, "t-foreign.sock");
    assert!(run(&rig.p).is_err());
}

#[test]
fn the_layout_is_root_for_support_and_the_panel_for_its_requests() {
    let d = tmpdir("layout");
    fs::create_dir_all(d.join("data/panel")).unwrap();
    let data = Dir::open(&d, "data/panel", false).unwrap().unwrap();
    prepare(&data).unwrap();
    prepare(&data).unwrap();
    let mode = |p: &str| fs::metadata(d.join("data/panel").join(p)).unwrap().permissions().mode() & 0o777;
    assert_eq!((mode("support"), mode("support/terminal"), mode("support/recordings")), (0o755, 0o755, 0o750));
    if is_root() {
        let own = |p: &str| {
            let m = fs::metadata(d.join("data/panel").join(p)).unwrap();
            (m.uid(), m.gid())
        };
        assert_eq!(own("support"), (0, 0), "the panel cannot rename or replace what is in support/");
        assert_eq!(own("support/terminal"), (PANEL_UID, PANEL_UID), "the panel writes its request here");
        assert_eq!(own("support/recordings"), (0, PANEL_UID), "root writes, the panel's group reads");
    }
    // A link where a directory should be is refused, not followed.
    fs::remove_dir(d.join("data/panel/support/terminal")).unwrap();
    symlink("/tmp", d.join("data/panel/support/terminal")).unwrap();
    assert!(prepare(&data).is_err());
    fs::remove_dir_all(&d).unwrap();
}

// mikan update gives data/panel to the panel's user with all it holds; support/ stays root's
// (and the recordings in it), terminal/ is made the panel's again.
#[test]
fn handing_data_to_the_panel_leaves_support_to_root() {
    if !is_root() {
        return;
    }
    let d = tmpdir("own");
    fs::create_dir_all(d.join("data/panel/tls")).unwrap();
    fs::create_dir_all(d.join("data/node")).unwrap();
    fs::write(d.join("data/panel/tls/k"), "k").unwrap();
    crate::panelfs::own_dirs(&d, true).unwrap();
    crate::panelfs::own_dirs(&d, true).unwrap();
    let uid = |p: &str| fs::metadata(d.join(p)).unwrap().uid();
    assert_eq!(uid("data/panel/tls/k"), PANEL_UID);
    assert_eq!(uid("data/panel/support"), 0);
    assert_eq!(uid("data/panel/support/recordings"), 0);
    assert_eq!(uid("data/panel/support/terminal"), PANEL_UID);
    assert_eq!(uid("data/node/support"), 0, "a node's too");
    fs::remove_dir_all(&d).unwrap();
}

#[test]
fn old_recordings_go_and_only_recordings() {
    let d = tmpdir("prune");
    let outside = d.join("outside.txt");
    fs::write(&outside, "mine").unwrap();
    let rec = d.join("recordings");
    fs::create_dir(&rec).unwrap();
    let age = |name: &str, days: u64| {
        let f = fs::File::create(rec.join(name)).unwrap();
        f.set_modified(SystemTime::now() - Duration::from_secs(days * 24 * 3600)).unwrap();
    };
    age("1-100.cast", 31);
    age("2-200.cast", 400);
    age("3-300.cast", 29);
    age("4-400.cast", 0);
    age("notes.txt", 400);
    age("5-x.cast", 400);
    // a link named like a recording is no recording: it stays, and so does its target
    symlink(&outside, rec.join("6-600.cast")).unwrap();
    let dirfd = rustix::fs::openat(rustix::fs::CWD, &rec, DIR_FLAGS, Mode::empty()).unwrap();
    prune(&dirfd, now_unix());
    let mut left: Vec<String> = fs::read_dir(&rec).unwrap().map(|e| e.unwrap().file_name().into_string().unwrap()).collect();
    left.sort();
    assert_eq!(left, ["3-300.cast", "4-400.cast", "5-x.cast", "6-600.cast", "notes.txt"]);
    assert_eq!(fs::read_to_string(&outside).unwrap(), "mine");
    fs::remove_dir_all(&d).unwrap();
}

#[test]
fn recording_names() {
    for ok in ["7-1800000000.cast", "123-4.cast"] {
        assert!(is_recording_name(ok), "{ok}");
    }
    for bad in
        ["", ".cast", "7.cast", "7-.cast", "-7.cast", "a-1.cast", "7-1.cast.gz", "7-1-2.cast", "../7-1.cast", "7-1.CAST", "7 -1.cast"]
    {
        assert!(!is_recording_name(bad), "{bad:?}");
    }
}

#[test]
fn frames_are_put_together_from_pieces_and_a_wrong_one_is_refused() {
    let mut r = FrameReader::default();
    let mut wire = frame(DATA, b"ls\n");
    wire.extend(frame(RESIZE, &[0, 80, 0, 24]));
    wire.extend(frame(DATA, b""));
    let mut got = Vec::new();
    for b in &wire {
        got.extend(r.feed(&[*b]).unwrap());
    }
    assert_eq!(got, [(DATA, b"ls\n".to_vec()), (RESIZE, vec![0, 80, 0, 24]), (DATA, vec![])]);
    // The biggest frame passes, one byte more does not.
    let mut r = FrameReader::default();
    assert_eq!(r.feed(&frame(DATA, &vec![b'x'; FRAME_MAX])).unwrap().len(), 1);
    let mut r = FrameReader::default();
    let mut head = vec![DATA];
    head.extend_from_slice(&((FRAME_MAX as u32) + 1).to_be_bytes());
    assert!(r.feed(&head).is_err(), "a frame beyond the limit is refused before its payload is read");
    // The agent's own frame types are not what the panel sends.
    for kind in [HELLO, EXIT, b'Z', 0] {
        assert!(FrameReader::default().feed(&frame(kind, b"x")).is_err(), "{kind}");
    }
}

#[test]
fn text_for_the_recording() {
    let mut carry = Vec::new();
    // a character cut between chunks is whole in the text
    let bytes = "щ".as_bytes();
    assert_eq!(utf8_chunk(&mut carry, &[b'a', bytes[0]]), "a");
    assert_eq!(utf8_chunk(&mut carry, &[bytes[1], b'b']), "щb");
    assert!(carry.is_empty());
    // what is not text becomes the replacement character, and the text goes on
    assert_eq!(utf8_chunk(&mut carry, &[b'x', 0xff, b'y', 0xc3, 0x28, b'z']), "x\u{FFFD}y\u{FFFD}(z");
    assert!(carry.is_empty());
    assert_eq!(utf8_chunk(&mut carry, b""), "");
}

#[test]
fn the_shell_is_a_login_shell_of_root() {
    assert_eq!(default_shell(|_| true), ["/bin/bash", "-l"]);
    assert_eq!(default_shell(|_| false), ["/bin/sh"]);
    let p = Params::production(false);
    assert_eq!(p.peers, [PANEL_UID], "only the panel's user reaches the socket");
    assert_eq!((p.idle, p.accept_wait), (Duration::from_secs(900), Duration::from_secs(60)));
    assert_eq!(p.root, Path::new("/opt/mikan"));
}
