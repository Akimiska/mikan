//! What the server has: root, the OS, memory, disk, the clock, free ports, the package
//! manager. Each check says what is wrong and what to do about it.

use std::fs;
use std::net::{Ipv4Addr, Ipv6Addr};
use std::process::{Command, Stdio};

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Level {
    Ok,
    Warn,
    Error,
}

#[derive(Clone, Debug)]
pub struct Check {
    pub label: &'static str,
    pub level: Level,
    pub detail: String,
}

impl Check {
    fn new(label: &'static str, level: Level, detail: impl Into<String>) -> Self {
        Self { label, level, detail: detail.into() }
    }
}

/// Runs a command and returns its stdout when it succeeds.
pub fn output(cmd: &str, args: &[&str]) -> Option<String> {
    let out = Command::new(cmd).args(args).stdin(Stdio::null()).stderr(Stdio::null()).output().ok()?;
    out.status.success().then(|| String::from_utf8_lossy(&out.stdout).into_owned())
}

pub fn is_root() -> bool {
    fs::read_to_string("/proc/self/status")
        .ok()
        .and_then(|s| s.lines().find_map(|l| l.strip_prefix("Uid:").map(|r| r.split_whitespace().nth(1) == Some("0"))))
        .unwrap_or(false)
}

pub struct Os {
    pub id: String,
    pub version: String,
    pub pretty: String,
}

pub fn os() -> Option<Os> {
    parse_os_release(&fs::read_to_string("/etc/os-release").ok()?)
}

fn parse_os_release(text: &str) -> Option<Os> {
    let get = |key: &str| {
        text.lines().find_map(|l| l.strip_prefix(key)?.strip_prefix('=')).map(|v| v.trim_matches('"').to_owned()).unwrap_or_default()
    };
    let os = Os { id: get("ID"), version: get("VERSION_ID"), pretty: get("PRETTY_NAME") };
    (!os.id.is_empty()).then_some(os)
}

/// Ubuntu 22.04+ and Debian 12+ are what the installer is tested on.
fn os_supported(os: &Os) -> bool {
    let major: u32 = os.version.split('.').next().and_then(|v| v.parse().ok()).unwrap_or(0);
    match os.id.as_str() {
        "ubuntu" => major >= 22,
        "debian" => major >= 12,
        _ => false,
    }
}

pub fn memory_mb() -> Option<u64> {
    let text = fs::read_to_string("/proc/meminfo").ok()?;
    let kb: u64 = text.lines().find_map(|l| l.strip_prefix("MemTotal:"))?.split_whitespace().next()?.parse().ok()?;
    Some(kb / 1024)
}

/// Free space for Docker's images, in MB.
pub fn disk_free_mb() -> Option<u64> {
    let dir = if fs::metadata("/var/lib/docker").is_ok() { "/var/lib/docker" } else { "/var/lib" };
    parse_df(&output("df", &["-Pk", dir])?)
}

fn parse_df(text: &str) -> Option<u64> {
    let kb: u64 = text.lines().nth(1)?.split_whitespace().nth(3)?.parse().ok()?;
    Some(kb / 1024)
}

/// None when the system does not say (no timedatectl).
pub fn clock_synced() -> Option<bool> {
    output("timedatectl", &["show", "-p", "NTPSynchronized", "--value"]).map(|v| v.trim() == "yes")
}

/// A fresh VPS keeps apt busy for minutes (the hoster's setup, unattended upgrades).
/// Who keeps the package manager busy: whoever holds one of its locks, or a running
/// apt, dpkg or unattended-upgrade. Read from /proc, so it needs neither fuser (Debian
/// images come without psmisc) nor pgrep. Matching names alone is not enough:
/// unattended-upgrades keeps `unattended-upgrade-shutdown --wait-for-signal` running
/// for good, and its name is cut to the same "unattended-upgr" as the real run.
pub fn dpkg_holder() -> Option<String> {
    use std::os::unix::fs::MetadataExt;
    let locks = ["/var/lib/dpkg/lock-frontend", "/var/lib/dpkg/lock", "/var/lib/apt/lists/lock", "/var/cache/apt/archives/lock"];
    let inodes: Vec<u64> = locks.iter().filter_map(|p| fs::metadata(p).ok()).map(|m| m.ino()).collect();
    if let Some(pid) = fs::read_to_string("/proc/locks").ok().and_then(|t| lock_holder(&t, &inodes)) {
        return Some(match pid {
            Some(pid) => format!("{} pid {pid}", proc_name(pid).unwrap_or_else(|| "a process".into())),
            None => "a package manager".into(),
        });
    }
    let procs = fs::read_dir("/proc").ok()?;
    procs.flatten().filter_map(|e| e.file_name().to_str()?.parse::<u32>().ok()).find_map(|pid| {
        let comm = proc_name(pid)?;
        let cmdline = fs::read(format!("/proc/{pid}/cmdline")).unwrap_or_default();
        package_process(&comm, &String::from_utf8_lossy(&cmdline)).then(|| format!("{comm} pid {pid}"))
    })
}

/// The pid holding a lock on one of the inodes in /proc/locks text: Some(None) for an
/// open-file-description lock, which has no owner pid.
fn lock_holder(text: &str, inodes: &[u64]) -> Option<Option<u32>> {
    text.lines().find_map(|l| {
        let f: Vec<&str> = l.split_whitespace().collect();
        // "1: POSIX  ADVISORY  WRITE 1234 fd:01:131090 0 EOF"; blocked waiters start with "->".
        let at = f.iter().position(|s| s.matches(':').count() == 2)?;
        if f.get(1) == Some(&"->") {
            return None;
        }
        let ino: u64 = f[at].rsplit(':').next()?.parse().ok()?;
        inodes.contains(&ino).then(|| f.get(at - 1).and_then(|p| p.parse::<u32>().ok()).filter(|&p| p > 0))
    })
}

fn package_process(comm: &str, cmdline: &str) -> bool {
    match comm {
        "apt-get" | "apt" | "aptitude" | "dpkg" => true,
        "unattended-upgr" => !cmdline.contains("unattended-upgrade-shutdown"),
        _ => false,
    }
}

fn proc_name(pid: u32) -> Option<String> {
    fs::read_to_string(format!("/proc/{pid}/comm")).ok().map(|s| s.trim().to_owned())
}

/// Half-configured packages left by the hoster's image: every apt call fails until
/// `dpkg --configure -a` finishes them.
pub fn dpkg_unfinished() -> bool {
    output("dpkg", &["--audit"]).is_some_and(|s| !s.trim().is_empty())
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Proto {
    Tcp,
    Udp,
}

impl Proto {
    pub fn name(self) -> &'static str {
        match self {
            Proto::Tcp => "tcp",
            Proto::Udp => "udp",
        }
    }
}

/// Who listens on a port: None when it is free, Some("?") when ss cannot name the process.
pub fn port_owner(port: u16, proto: Proto) -> Option<String> {
    let flags = match proto {
        Proto::Tcp => "-Hlntp",
        Proto::Udp => "-Hlnup",
    };
    let filter = format!("sport = :{port}");
    let out = output("ss", &[flags, &filter])?;
    if out.trim().is_empty() {
        return None;
    }
    Some(process_name(&out).unwrap_or_else(|| "?".into()))
}

fn process_name(ss: &str) -> Option<String> {
    let start = ss.find("users:((\"")? + 9;
    let rest = &ss[start..];
    Some(rest[..rest.find('"')?].to_owned())
}

/// The source address of the default route: the server's IPv4 when it is not behind NAT.
pub fn route_ipv4() -> Option<Ipv4Addr> {
    let out = output("ip", &["-4", "route", "get", "1.1.1.1"])?;
    let mut words = out.split_whitespace();
    while let Some(w) = words.next() {
        if w == "src" {
            return words.next()?.parse().ok();
        }
    }
    None
}

/// The server's public IPv6 addresses, to tell a domain's AAAA record that points here.
pub fn global_ipv6() -> Vec<Ipv6Addr> {
    let Some(out) = output("ip", &["-6", "-o", "addr", "show", "scope", "global"]) else { return Vec::new() };
    out.lines()
        .filter_map(|l| {
            let mut w = l.split_whitespace();
            w.find(|&x| x == "inet6")?;
            w.next()?.split('/').next()?.parse().ok()
        })
        .collect()
}

pub fn ufw_active() -> bool {
    output("ufw", &["status"]).is_some_and(|s| s.contains("Status: active"))
}

/// The ports every install needs: the protocols listen on 443 and 8443, TCP and UDP.
pub const VPN_PORTS: [(u16, Proto); 4] = [(443, Proto::Tcp), (443, Proto::Udp), (8443, Proto::Tcp), (8443, Proto::Udp)];

/// The checks before an install; node is an install of a node for another panel.
pub fn checks(node: bool) -> Vec<Check> {
    let mut out = Vec::new();
    out.push(if is_root() {
        Check::new("Root", Level::Ok, "running as root")
    } else {
        Check::new("Root", Level::Error, "run as root: sudo mikan install")
    });
    out.push(match os() {
        Some(o) if os_supported(&o) => Check::new("System", Level::Ok, o.pretty),
        Some(o) => Check::new("System", Level::Warn, format!("{}: tested on Ubuntu 22.04+ and Debian 12+", o.pretty)),
        None => Check::new("System", Level::Warn, "unknown: tested on Ubuntu 22.04+ and Debian 12+"),
    });
    out.push(Check::new("Architecture", Level::Ok, std::env::consts::ARCH));
    out.push(match memory_mb() {
        Some(mb) if mb < 400 => Check::new("Memory", Level::Warn, format!("{mb} MB: Docker needs about 300 MB, mikan 60 MB more")),
        Some(mb) if mb >= 1024 => Check::new("Memory", Level::Ok, format!("{:.1} GB", mb as f64 / 1024.0)),
        Some(mb) => Check::new("Memory", Level::Ok, format!("{mb} MB")),
        None => Check::new("Memory", Level::Warn, "unknown"),
    });
    out.push(match disk_free_mb() {
        Some(mb) if mb < 1024 => Check::new("Disk", Level::Error, format!("{mb} MB free: Docker and mikan need at least 1 GB")),
        Some(mb) if mb < 3072 => {
            Check::new("Disk", Level::Warn, format!("{:.1} GB free: tight for updates and backups", mb as f64 / 1024.0))
        }
        Some(mb) => Check::new("Disk", Level::Ok, format!("{:.0} GB free", mb as f64 / 1024.0)),
        None => Check::new("Disk", Level::Warn, "unknown"),
    });
    out.push(match clock_synced() {
        Some(true) => Check::new("Clock", Level::Ok, "synchronized"),
        Some(false) => Check::new("Clock", Level::Warn, "not synchronized: TLS and REALITY need the right time (timedatectl set-ntp true)"),
        None => Check::new("Clock", Level::Warn, "cannot tell: keep it synchronized, TLS and REALITY need the right time"),
    });
    let taken: Vec<String> =
        VPN_PORTS.iter().filter_map(|&(p, proto)| port_owner(p, proto).map(|who| format!("{p}/{} ({who})", proto.name()))).collect();
    out.push(if taken.is_empty() {
        Check::new("Ports 443, 8443", Level::Ok, "free")
    } else {
        Check::new(
            "Ports 443, 8443",
            Level::Error,
            format!("taken: {}. Stop what holds them (an old panel, a web server) and check again", taken.join(", ")),
        )
    });
    if !node {
        out.push(match port_owner(80, Proto::Tcp) {
            None => Check::new("Port 80", Level::Ok, "free for Let's Encrypt"),
            Some(who) => {
                Check::new("Port 80", Level::Warn, format!("taken ({who}): Let's Encrypt cannot issue a certificate for a domain"))
            }
        });
    }
    out.push(match crate::docker::version() {
        Some(v) if crate::docker::compose_ok() => Check::new("Docker", Level::Ok, format!("{v} with compose")),
        Some(v) => Check::new("Docker", Level::Error, format!("{v} without compose v2: update Docker")),
        None => Check::new("Docker", Level::Ok, "not installed: the installer sets it up (get.docker.com)"),
    });
    if let Some(who) = dpkg_holder() {
        out.push(Check::new("Packages", Level::Warn, format!("busy, {who}: the installer waits for it")));
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn os_release() {
        let os = parse_os_release("PRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nID=ubuntu\n").unwrap();
        assert_eq!((os.id.as_str(), os.version.as_str(), os.pretty.as_str()), ("ubuntu", "24.04", "Ubuntu 24.04.1 LTS"));
        assert!(os_supported(&os));
        let old = Os { id: "debian".into(), version: "11".into(), pretty: String::new() };
        assert!(!os_supported(&old));
    }

    #[test]
    fn package_locks() {
        let locks = "1: POSIX  ADVISORY  WRITE 4242 fd:01:131090 0 EOF\n\
                     1: -> POSIX  ADVISORY  WRITE 5151 fd:01:131090 0 EOF\n\
                     2: OFDLCK ADVISORY  WRITE -1 fd:01:131091 0 EOF\n\
                     3: FLOCK  ADVISORY  WRITE 777 00:19:999 0 EOF\n";
        assert_eq!(lock_holder(locks, &[131090]), Some(Some(4242)));
        assert_eq!(lock_holder(locks, &[131091]), Some(None));
        assert_eq!(lock_holder(locks, &[555]), None);
        assert_eq!(lock_holder("", &[131090]), None);

        // The idle helper unattended-upgrades keeps running must not read as busy.
        assert!(!package_process(
            "unattended-upgr",
            "/usr/bin/python3\0/usr/share/unattended-upgrades/unattended-upgrade-shutdown\0--wait-for-signal\0"
        ));
        assert!(package_process("unattended-upgr", "/usr/bin/python3\0/usr/bin/unattended-upgrade\0"));
        assert!(package_process("apt-get", "apt-get\0install\0"));
        assert!(!package_process("aptd", ""));
    }

    #[test]
    fn ss_and_df() {
        let ss = "LISTEN 0 4096 0.0.0.0:443 0.0.0.0:* users:((\"nginx\",pid=812,fd=6),(\"nginx\",pid=811,fd=6))\n";
        assert_eq!(process_name(ss).as_deref(), Some("nginx"));
        assert_eq!(process_name("UNCONN 0 0 0.0.0.0:443 0.0.0.0:*"), None);
        let df = "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/vda1 25623780 4830532 19720516 20% /\n";
        assert_eq!(parse_df(df), Some(19258));
    }
}
