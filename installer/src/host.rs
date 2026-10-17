//! What mikan changes on the host besides /opt/mikan: kernel tuning, ufw rules, the
//! update timer and the mikan command itself.

use std::fs;
use std::path::Path;
use std::process::{Command, Stdio};

use anyhow::{Context, Result, bail};

use crate::system;

pub const SYSCTL: &str = "/etc/sysctl.d/99-mikan.conf";

const SYSCTL_CONF: &str = "# mikan: BBR for the TCP protocols, bigger UDP buffers for Hysteria2 and TUIC (QUIC)
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
";

pub fn tune() -> Result<()> {
    fs::write(SYSCTL, SYSCTL_CONF)?;
    crate::system::run_quiet("sysctl", &["--system"])
}

/// HTTPS ports the panel moves a blocked inbound to on its own (internal/panel/autotune
/// Pool): the node cannot open them in the firewall itself.
pub const POOL: [u16; 15] = [2053, 2083, 2087, 2096, 2443, 3443, 4443, 5443, 6443, 7443, 8443, 9443, 10443, 11443, 12443];

/// The ufw rules of an install: the panel's port (or a node's API port), 80 for the
/// certificate authority's HTTP-01 check, 443 and the pool over TCP and UDP. A node needs 80
/// too: the panel orders its public certificate, and the CA comes to the node's port 80.
pub fn rules(panel_port: Option<u16>, node_api: Option<u16>) -> Vec<String> {
    let mut r = Vec::new();
    if let Some(p) = panel_port {
        r.push(format!("{p}/tcp"));
    }
    if let Some(p) = node_api {
        r.push(format!("{p}/tcp"));
    }
    r.push(ACME_RULE.into());
    r.extend(pool_rules());
    r
}

/// Port 80, where the certificate authority checks the server (HTTP-01).
pub const ACME_RULE: &str = "80/tcp";

/// What `mikan update` opens again on a server with ufw on: port 80 (nodes installed before
/// they got public certificates lack it) and the pool, which may have grown.
pub fn update_rules() -> Vec<String> {
    std::iter::once(ACME_RULE.to_string()).chain(pool_rules()).collect()
}

/// 443 and the pool over TCP and UDP: what the protocols and the panel's moves need open.
pub fn pool_rules() -> Vec<String> {
    std::iter::once(443).chain(POOL).flat_map(|p| [format!("{p}/tcp"), format!("{p}/udp")]).collect()
}

/// Opens the rules when ufw is on; returns whether it was.
pub fn open(rules: &[String]) -> Result<bool> {
    if !system::ufw_active() {
        return Ok(false);
    }
    for rule in rules {
        allow(rule)?;
    }
    Ok(true)
}

/// Whether a rule is a port or a range of ports over tcp or udp, like 8443/tcp or
/// 20000:20010/udp: all `ufw allow` takes from here, so that nothing a container printed
/// can become an option or another command.
pub fn valid_rule(rule: &str) -> bool {
    let Some((ports, proto)) = rule.split_once('/') else { return false };
    if proto != "tcp" && proto != "udp" {
        return false;
    }
    let port = |p: &str| p.len() <= 5 && p.bytes().all(|b| b.is_ascii_digit()) && p.parse::<u32>().is_ok_and(|n| (1..=65535).contains(&n));
    match ports.split_once(':') {
        Some((a, b)) => port(a) && port(b) && a.parse::<u32>().unwrap_or(0) <= b.parse::<u32>().unwrap_or(0),
        None => port(ports),
    }
}

pub fn allow(rule: &str) -> Result<()> {
    if !valid_rule(rule) {
        bail!("{rule:?} is not a firewall rule like 8443/tcp");
    }
    ufw(&["allow", rule])
}

/// A node's API port for its panel's address only.
pub fn allow_from(ip: std::net::IpAddr, port: u16) -> Result<()> {
    let ip = ip.to_string();
    let port = port.to_string();
    ufw(&["allow", "from", &ip, "to", "any", "port", &port, "proto", "tcp"])
}

fn ufw(args: &[&str]) -> Result<()> {
    crate::system::run_quiet("ufw", args)
}

pub const BIN: &str = "/usr/local/bin/mikan";

/// Puts this binary at /usr/local/bin/mikan, the server's command, unless it runs from there.
pub fn install_self() -> Result<()> {
    let me = std::env::current_exe()?;
    if fs::canonicalize(&me).ok() == fs::canonicalize(BIN).ok() {
        return Ok(());
    }
    replace_bin(&fs::read(&me)?, None)
}

/// Replaces /usr/local/bin/mikan at once; the running process keeps its old copy. The new
/// file is on the disk, not only in the page cache (a power cut must not leave the command,
/// which both update units run, empty), and it runs and reports the release it is meant to
/// be before it takes the working one's place.
pub fn replace_bin(data: &[u8], release: Option<&str>) -> Result<()> {
    replace_at(Path::new(BIN), data, release)
}

fn replace_at(bin: &Path, data: &[u8], release: Option<&str>) -> Result<()> {
    use std::io::Write;
    use std::os::unix::fs::OpenOptionsExt;
    let mut tmp = bin.as_os_str().to_owned();
    tmp.push(".new");
    let tmp = std::path::PathBuf::from(tmp);
    let _ = fs::remove_file(&tmp);
    let mut f =
        fs::OpenOptions::new().write(true).create_new(true).mode(0o755).open(&tmp).with_context(|| format!("create {}", tmp.display()))?;
    let checked = f.write_all(data).and_then(|()| f.sync_all()).map_err(anyhow::Error::from).and_then(|()| {
        drop(f);
        let out = version_of(&tmp)?;
        match release {
            Some(v) if !out.contains(v) => bail!("the downloaded command is {:?}, not release {v}", out.trim()),
            _ => Ok(()),
        }
    });
    if let Err(e) = checked {
        let _ = fs::remove_file(&tmp);
        return Err(e);
    }
    fs::rename(&tmp, bin).with_context(|| format!("replace {}", bin.display()))?;
    if let Some(dir) = bin.parent().and_then(|d| fs::File::open(d).ok()) {
        let _ = dir.sync_all();
    }
    Ok(())
}

/// What the command at path says to --version; it must run and succeed.
fn version_of(path: &Path) -> Result<String> {
    let mut last = None;
    for _ in 0..5 {
        match Command::new(path).arg("--version").stdin(Stdio::null()).output() {
            Ok(o) if o.status.success() => return Ok(String::from_utf8_lossy(&o.stdout).into_owned()),
            Ok(o) => bail!("the downloaded command does not run ({})", o.status),
            // A file only just written can still be busy for a moment when another thread forked.
            Err(e) if e.kind() == std::io::ErrorKind::ExecutableFileBusy => {
                last = Some(e);
                std::thread::sleep(std::time::Duration::from_millis(100));
            }
            Err(e) => return Err(e).context("run the downloaded command"),
        }
    }
    Err(last.map_or_else(|| anyhow::anyhow!("the downloaded command does not run"), Into::into))
}

const UNIT_NAMES: [&str; 10] = [
    "mikan-update.service",
    "mikan-update.timer",
    "mikan-update-request.service",
    "mikan-update-request.path",
    "mikan-doctor-request.service",
    "mikan-doctor-request.path",
    "mikan-restart-request.service",
    "mikan-restart-request.path",
    "mikan-terminal-request.service",
    "mikan-terminal-request.path",
];

/// The path units, which are enabled and started; the timer too.
const WATCHES: [&str; 5] = [
    "mikan-update.timer",
    "mikan-update-request.path",
    "mikan-doctor-request.path",
    "mikan-restart-request.path",
    "mikan-terminal-request.path",
];

/// The unit files of a panel or of a node: the same, except that each path unit watches
/// the request of the one the server runs (the panel's Update button, or the panel asking
/// a node over its API: data/panel/update/request and data/node/update/request; the same
/// for a doctor run and a restart, hostreq.rs).
fn units(panel: bool) -> Vec<(&'static str, String)> {
    let (dir, from) = if panel { ("panel", "the panel") } else { ("node", "the panel this node belongs to") };
    vec![
        (
            UNIT_NAMES[0],
            "[Unit]
Description=mikan: update when automatic updates are on
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
# A oneshot unit has no time limit by default: an update stuck on a dead connection
# would keep the timer from ever starting it again. Two hours: a database move alone may
# take an hour (DATABASE_LIMIT), with the pull and the backups around it.
TimeoutStartSec=2h
ExecStart=/usr/local/bin/mikan update --auto
"
            .into(),
        ),
        (
            UNIT_NAMES[1],
            "[Unit]
Description=mikan: look for an update once a day

[Timer]
OnCalendar=*-*-* 03:00:00
RandomizedDelaySec=3h
Persistent=true

[Install]
WantedBy=timers.target
"
            .into(),
        ),
        (
            UNIT_NAMES[2],
            "[Unit]
Description=mikan: update asked for in the panel
After=docker.service

[Service]
Type=oneshot
TimeoutStartSec=2h
ExecStart=/usr/local/bin/mikan update --requested
"
            .into(),
        ),
        (
            UNIT_NAMES[3],
            format!(
                "[Unit]
Description=mikan: wait for an update request from {from}

[Path]
PathExists=/opt/mikan/data/{dir}/update/request
Unit=mikan-update-request.service

[Install]
WantedBy=paths.target
"
            ),
        ),
        (
            UNIT_NAMES[4],
            "[Unit]
Description=mikan: run the server check the panel asked for
After=docker.service

[Service]
Type=oneshot
# The checks that wait for the network end within a minute; a stuck one must not hold the
# unit (and so the next request) for ever.
TimeoutStartSec=5min
ExecStart=/usr/local/bin/mikan doctor --requested
"
            .into(),
        ),
        (
            UNIT_NAMES[5],
            format!(
                "[Unit]
Description=mikan: wait for a server check request from {from}

[Path]
PathExists=/opt/mikan/data/{dir}/doctor/request
Unit=mikan-doctor-request.service

[Install]
WantedBy=paths.target
"
            ),
        ),
        (
            UNIT_NAMES[6],
            "[Unit]
Description=mikan: restart the containers as the panel asked
After=docker.service

[Service]
Type=oneshot
# It waits its turn behind an update (the one lock), which may take two hours.
TimeoutStartSec=2h
ExecStart=/usr/local/bin/mikan restart --requested
"
            .into(),
        ),
        (
            UNIT_NAMES[7],
            format!(
                "[Unit]
Description=mikan: wait for a restart request from {from}

[Path]
PathExists=/opt/mikan/data/{dir}/restart/request
Unit=mikan-restart-request.service

[Install]
WantedBy=paths.target
"
            ),
        ),
        (
            UNIT_NAMES[8],
            "[Unit]
Description=mikan: one support terminal session, opened by the panel for a live grant

[Service]
Type=simple
# The agent ends the session by itself (the grant's end, the panel letting go, 15 minutes
# of silence). This is the backstop for one that is stuck: no grant lasts longer than 24
# hours. systemd kills the whole cgroup when the agent ends, so a process the shell left
# behind does not outlive the session.
RuntimeMaxSec=25h
TimeoutStartSec=1min
KillMode=control-group
SendSIGKILL=yes
TimeoutStopSec=10s
ExecStart=/usr/local/bin/mikan terminal-agent
"
            .into(),
        ),
        (
            UNIT_NAMES[9],
            format!(
                "[Unit]
Description=mikan: wait for a support terminal request from {from}

[Path]
PathExists=/opt/mikan/data/{dir}/support/terminal/request
Unit=mikan-terminal-request.service

[Install]
WantedBy=paths.target
"
            ),
        ),
    ]
}

/// Whether the unit files on disk are the ones this installer writes for a panel or a node
/// (a server updated by an older command has the older ones; a node before 0.5.0.2 has no
/// request unit at all).
pub fn units_current(panel: bool) -> bool {
    units_current_in(Path::new("/etc/systemd/system"), panel, terminal_off())
}

/// The support terminal's units, which a server with MIKAN_SUPPORT_ACCESS=off in its .env
/// does not have: nothing there can start a shell for an operator.
const TERMINAL_UNITS: [&str; 2] = ["mikan-terminal-request.service", "mikan-terminal-request.path"];

/// Whether the server's .env turns support access off (MIKAN_SUPPORT_ACCESS=off).
fn terminal_off() -> bool {
    crate::envfile::EnvFile::load(Path::new(crate::DIR).join(".env")).is_ok_and(|e| e.get("MIKAN_SUPPORT_ACCESS") == Some("off"))
}

/// The units a server should have: all of them, or all but the terminal's (terminal_off).
fn wanted(panel: bool, terminal_off: bool) -> Vec<(&'static str, String)> {
    units(panel).into_iter().filter(|(name, _)| !(terminal_off && TERMINAL_UNITS.contains(name))).collect()
}

fn units_current_in(dir: &Path, panel: bool, terminal_off: bool) -> bool {
    wanted(panel, terminal_off).iter().all(|(name, text)| fs::read_to_string(dir.join(name)).is_ok_and(|have| have == *text))
        && !(terminal_off && TERMINAL_UNITS.iter().any(|name| dir.join(name).exists()))
}

/// The update timer and the watches on the requests of a panel (its Update button, a server
/// check, a restart) or of a node (the panel asking over the node API). Run again, it
/// leaves a server as it is: the files are written only where they differ, the units
/// that are enabled stay so.
pub fn install_units(panel: bool) -> Result<()> {
    if !Path::new("/run/systemd/system").exists() {
        bail!("no systemd");
    }
    let off = terminal_off();
    if off {
        // Turned off after the units were installed: they go, a session running ends.
        let _ = systemctl(&["disable", "--now", TERMINAL_UNITS[1], TERMINAL_UNITS[0]]);
    }
    write_units(Path::new("/etc/systemd/system"), panel, off)?;
    systemctl(&["daemon-reload"])?;
    for name in WATCHES {
        if !(off && TERMINAL_UNITS.contains(&name)) {
            systemctl(&["enable", "--now", name])?;
        }
    }
    Ok(())
}

/// Writes the unit files into dir where they are not there or differ, and removes the
/// terminal's when it is off; returns how many it wrote. A second run writes none.
fn write_units(dir: &Path, panel: bool, terminal_off: bool) -> Result<usize> {
    if terminal_off {
        for name in TERMINAL_UNITS {
            match fs::remove_file(dir.join(name)) {
                Err(e) if e.kind() != std::io::ErrorKind::NotFound => return Err(e).with_context(|| format!("remove {name}")),
                _ => {}
            }
        }
    }
    let mut written = 0;
    for (name, text) in wanted(panel, terminal_off) {
        let path = dir.join(name);
        if fs::read_to_string(&path).is_ok_and(|have| have == text) {
            continue;
        }
        fs::write(&path, text).with_context(|| format!("write {}", path.display()))?;
        written += 1;
    }
    Ok(written)
}

pub fn remove_units() {
    let mut disable = vec!["disable", "--now"];
    disable.extend(WATCHES);
    let _ = systemctl(&disable);
    for name in UNIT_NAMES {
        let _ = fs::remove_file(format!("/etc/systemd/system/{name}"));
    }
    let _ = systemctl(&["daemon-reload"]);
}

fn systemctl(args: &[&str]) -> Result<()> {
    crate::system::run_quiet("systemctl", args)
}

#[cfg(test)]
mod tests {
    use super::*;

    // The panel moves a blocked inbound and puts a cascade relay on a port of this pool;
    // the installer must have opened them all.
    #[test]
    fn pool_matches_the_panel() {
        let go = fs::read_to_string("../internal/panel/domain/ports.go").unwrap();
        let start = go.find("var PortPool = []int{").expect("domain.PortPool") + "var PortPool = []int{".len();
        let list = &go[start..start + go[start..].find('}').unwrap()];
        let pool: Vec<u16> = list.split(',').map(|p| p.trim().parse().unwrap()).collect();
        assert_eq!(pool, POOL);
    }

    // What a container prints is not a command: only a port (range) and a protocol pass.
    #[test]
    fn only_port_rules_reach_ufw() {
        for ok in ["443/tcp", "8443/udp", "20000:20010/tcp", "1/tcp", "65535/udp"] {
            assert!(valid_rule(ok), "{ok}");
        }
        for bad in [
            "",
            "443",
            "443/icmp",
            "0/tcp",
            "65536/tcp",
            "99999/tcp",
            "-1/tcp",
            "443/tcp;ls",
            "443/tcp --force",
            "--help/tcp",
            "443:/tcp",
            ":443/tcp",
            "20010:20000/tcp",
            "4 43/tcp",
            "443/TCP",
            "443/tcp\n",
            "1e3/tcp",
            "+443/tcp",
            "443/tcp/udp",
        ] {
            assert!(!valid_rule(bad), "{bad:?}");
        }
        assert!(allow("--help").is_err());
    }

    fn tmpdir(name: &str) -> std::path::PathBuf {
        let d = std::env::temp_dir().join(format!("host-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&d);
        fs::create_dir_all(&d).unwrap();
        d
    }

    // The command is replaced only by a file that runs and is the release meant: a
    // truncated or wrong download leaves the working one.
    #[test]
    fn the_command_is_replaced_only_by_a_working_release() {
        use std::os::unix::fs::PermissionsExt;
        let d = tmpdir("bin");
        let bin = d.join("mikan");
        fs::write(&bin, "#!/bin/sh\necho mikan 0.4.3\n").unwrap();
        fs::set_permissions(&bin, fs::Permissions::from_mode(0o755)).unwrap();
        let old = fs::read(&bin).unwrap();
        let new = b"#!/bin/sh\necho mikan 0.4.4\n";
        for (what, data, release) in [
            ("wrong release", &new[..], Some("0.4.5")),
            ("does not run", &b"#!/bin/sh\nexit 3\n"[..], None),
            ("truncated", &new[..8], Some("0.4.4")),
            ("empty", &b""[..], None),
        ] {
            assert!(replace_at(&bin, data, release).is_err(), "{what} replaced the command");
            assert_eq!(fs::read(&bin).unwrap(), old, "{what}");
            assert!(!d.join("mikan.new").exists(), "{what} left its file");
        }
        replace_at(&bin, new, Some("0.4.4")).unwrap();
        assert_eq!(fs::read(&bin).unwrap(), new);
        assert_eq!(fs::metadata(&bin).unwrap().permissions().mode() & 0o777, 0o755);
        fs::remove_dir_all(&d).unwrap();
    }

    // A oneshot unit has no time limit unless it is given one.
    #[test]
    fn update_units_have_a_time_limit() {
        for panel in [true, false] {
            for (name, text) in units(panel) {
                if name.ends_with(".service") {
                    assert!(text.contains("TimeoutStartSec="), "{name}");
                }
            }
        }
    }

    // A panel's path units watch the panel's requests, a node's the node's: the same units,
    // the path units apart.
    #[test]
    fn the_request_unit_watches_the_directory_of_what_the_server_runs() {
        let (panel, node) = (units(true), units(false));
        assert_eq!(panel.len(), UNIT_NAMES.len());
        let unit = |all: &[(&str, String)], name: &str| all.iter().find(|(n, _)| *n == name).unwrap().1.clone();
        for (kind, command) in [("update", "update --requested"), ("doctor", "doctor --requested"), ("restart", "restart --requested")] {
            let path_unit = format!("mikan-{kind}-request.path");
            let (p, n) = (unit(&panel, &path_unit), unit(&node, &path_unit));
            assert!(p.contains(&format!("\nPathExists=/opt/mikan/data/panel/{kind}/request\n")), "{kind}");
            assert!(n.contains(&format!("\nPathExists=/opt/mikan/data/node/{kind}/request\n")), "{kind}");
            assert!(!p.contains("data/node") && !n.contains("data/panel"), "{kind}");
            // both run the same command, which finds out by itself whose request it is
            let service = unit(&node, &format!("mikan-{kind}-request.service"));
            assert!(service.contains(&format!("ExecStart=/usr/local/bin/mikan {command}\n")), "{kind}");
            assert!(n.contains(&format!("Unit=mikan-{kind}-request.service")), "{kind}");
        }
        for ((pn, pt), (nn, nt)) in panel.iter().zip(&node) {
            assert_eq!(pn, nn);
            assert!(UNIT_NAMES.contains(pn), "{pn}");
            if !pn.ends_with(".path") {
                assert_eq!(pt, nt, "{pn} is the same on a panel and on a node");
            }
        }
        // what is enabled is a unit that exists
        assert!(WATCHES.iter().all(|w| UNIT_NAMES.contains(w)));
    }

    // The terminal's request lies deeper (support/terminal/), the agent is the one fixed
    // command, it runs apart from the others and is enabled like them.
    #[test]
    fn the_terminal_unit_watches_its_own_request_and_runs_the_agent() {
        let (panel, node) = (units(true), units(false));
        let unit = |all: &[(&str, String)], name: &str| all.iter().find(|(n, _)| *n == name).unwrap().1.clone();
        let (p, n) = (unit(&panel, "mikan-terminal-request.path"), unit(&node, "mikan-terminal-request.path"));
        assert!(p.contains("\nPathExists=/opt/mikan/data/panel/support/terminal/request\n") && !p.contains("data/node"));
        assert!(n.contains("\nPathExists=/opt/mikan/data/node/support/terminal/request\n") && !n.contains("data/panel"));
        assert!(p.contains("Unit=mikan-terminal-request.service"));
        let service = unit(&panel, "mikan-terminal-request.service");
        assert_eq!(service, unit(&node, "mikan-terminal-request.service"));
        assert!(service.contains("\nExecStart=/usr/local/bin/mikan terminal-agent\n"));
        // A session is a long-running process whose whole cgroup goes with it, and that is
        // stopped at 25 hours whatever happens (a grant lasts 24 at most).
        for want in ["Type=simple", "KillMode=control-group", "SendSIGKILL=yes", "RuntimeMaxSec=25h"] {
            assert!(service.contains(want), "{want}");
        }
        assert!(WATCHES.contains(&"mikan-terminal-request.path"));
    }

    // Run again, the install leaves the files alone: a server that is up to date is not
    // rewritten, one with an older unit set (no doctor or restart unit, a changed text) gets
    // what is missing and nothing else.
    #[test]
    fn writing_the_units_is_idempotent() {
        let d = tmpdir("write-units");
        assert_eq!(write_units(&d, true, false).unwrap(), UNIT_NAMES.len());
        assert_eq!(write_units(&d, true, false).unwrap(), 0, "a second run rewrites nothing");
        assert!(units_current_in(&d, true, false));
        // An installer of 0.5.1.0 wrote the update units only.
        for gone in
            ["mikan-doctor-request.service", "mikan-doctor-request.path", "mikan-restart-request.service", "mikan-restart-request.path"]
        {
            fs::remove_file(d.join(gone)).unwrap();
        }
        assert!(!units_current_in(&d, true, false), "an old install is not current: mikan update brings the new units");
        assert_eq!(write_units(&d, true, false).unwrap(), 4);
        assert!(units_current_in(&d, true, false));
        // A changed file is put back, the others stay.
        fs::write(d.join("mikan-doctor-request.path"), "[Path]\nPathExists=/tmp/x\n").unwrap();
        assert_eq!(write_units(&d, true, false).unwrap(), 1);
        assert!(units_current_in(&d, true, false));
        // The same directory for a node: its own watches, the files that are the same stay.
        let before = fs::read_to_string(d.join("mikan-update.timer")).unwrap();
        assert_eq!(write_units(&d, false, false).unwrap(), 4, "only the four path units differ");
        assert_eq!(fs::read_to_string(d.join("mikan-update.timer")).unwrap(), before);
        assert!(fs::read_to_string(d.join("mikan-restart-request.path")).unwrap().contains("data/node/restart/request"));
        fs::remove_dir_all(&d).unwrap();
    }

    // The units that answer a container run mikan with a flag nothing in a request can change.
    #[test]
    fn the_new_units_run_a_fixed_command() {
        for panel in [true, false] {
            for (name, text) in units(panel) {
                if name.starts_with("mikan-doctor-request")
                    || name.starts_with("mikan-restart-request")
                    || name.starts_with("mikan-terminal-request")
                {
                    assert!(!text.contains("$") && !text.contains("%"), "{name}: nothing is expanded from outside");
                }
            }
        }
    }

    // A server updated by an older command has the older units: a node with none of the
    // request unit, a panel's file on a node, are not current; each mode's own are.
    #[test]
    fn units_are_current_for_the_mode_they_were_written_for() {
        let d = tmpdir("units");
        assert!(!units_current_in(&d, true, false) && !units_current_in(&d, false, false), "nothing written yet");
        let write = |panel: bool| {
            for (name, text) in units(panel) {
                fs::write(d.join(name), text).unwrap();
            }
        };
        write(false);
        assert!(units_current_in(&d, false, false));
        assert!(!units_current_in(&d, true, false), "a node's units are not a panel's");
        write(true);
        assert!(units_current_in(&d, true, false) && !units_current_in(&d, false, false));
        // a node of 0.5.0.1: the timer and the services, no path unit
        write(false);
        fs::remove_file(d.join("mikan-update-request.path")).unwrap();
        assert!(!units_current_in(&d, false, false), "a node without the request unit gets it on its next update");
        // an older text of the path unit
        write(false);
        let old = fs::read_to_string(d.join("mikan-update-request.path")).unwrap().replace("data/node/", "data/panel/");
        fs::write(d.join("mikan-update-request.path"), old).unwrap();
        assert!(!units_current_in(&d, false, false));
        fs::remove_dir_all(&d).unwrap();
    }

    #[test]
    fn firewall_rules() {
        let r = rules(Some(21355), None);
        assert_eq!(&r[..4], ["21355/tcp", "80/tcp", "443/tcp", "443/udp"]);
        assert!(r.contains(&"9443/udp".to_string()));
        assert_eq!(rules(None, Some(25305))[0], "25305/tcp");
        // A node gets 80 for its certificate, also when its API port opens for the panel alone.
        assert!(rules(None, Some(25305)).contains(&"80/tcp".to_string()));
        assert!(rules(None, None).contains(&"80/tcp".to_string()));
        let u = update_rules();
        assert_eq!(u[0], "80/tcp");
        assert!(u.contains(&"12443/udp".to_string()) && u.iter().all(|r| valid_rule(r)));
    }

    // MIKAN_SUPPORT_ACCESS=off: the terminal's units are neither written nor left behind.
    #[test]
    fn support_access_off_keeps_the_terminal_units_away() {
        let d = std::env::temp_dir().join(format!("mikan-units-off-{}", std::process::id()));
        fs::create_dir_all(&d).unwrap();
        write_units(&d, true, false).unwrap();
        assert!(d.join(TERMINAL_UNITS[1]).exists());
        assert!(!units_current_in(&d, true, true), "turned off with the units still there");
        assert_eq!(write_units(&d, true, true).unwrap(), 0, "the other units are the current ones already");
        assert!(TERMINAL_UNITS.iter().all(|n| !d.join(n).exists()));
        assert!(units_current_in(&d, true, true));
        assert!(!units_current_in(&d, true, false), "turned on again: the terminal's units come back");
        assert_eq!(write_units(&d, true, false).unwrap(), 2);
        let _ = fs::remove_dir_all(&d);
    }
}
