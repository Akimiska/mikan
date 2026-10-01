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
    let ok = Command::new("sysctl").arg("--system").stdout(Stdio::null()).stderr(Stdio::null()).status().is_ok_and(|s| s.success());
    if !ok {
        bail!("sysctl failed");
    }
    Ok(())
}

/// HTTPS ports the panel moves a blocked inbound to on its own (internal/panel/autotune
/// Pool): the node cannot open them in the firewall itself.
pub const POOL: [u16; 12] = [2053, 2083, 2087, 2096, 2443, 3443, 4443, 5443, 6443, 7443, 8443, 9443];

/// The ufw rules of an install: the panel's port (or a node's API port), 80 for Let's
/// Encrypt, 443 and the pool over TCP and UDP.
pub fn rules(panel_port: Option<u16>, node_api: Option<u16>) -> Vec<String> {
    let mut r = Vec::new();
    if let Some(p) = panel_port {
        r.push(format!("{p}/tcp"));
        r.push("80/tcp".into());
    }
    if let Some(p) = node_api {
        r.push(format!("{p}/tcp"));
    }
    for p in std::iter::once(443).chain(POOL) {
        r.push(format!("{p}/tcp"));
        r.push(format!("{p}/udp"));
    }
    r
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

pub fn allow(rule: &str) -> Result<()> {
    let ok = Command::new("ufw").args(["allow", rule]).stdout(Stdio::null()).stderr(Stdio::null()).status().is_ok_and(|s| s.success());
    if !ok {
        bail!("ufw allow {rule} failed");
    }
    Ok(())
}

pub const BIN: &str = "/usr/local/bin/mikan";

/// Puts this binary at /usr/local/bin/mikan, the server's command, unless it runs from there.
pub fn install_self() -> Result<()> {
    let me = std::env::current_exe()?;
    if fs::canonicalize(&me).ok() == fs::canonicalize(BIN).ok() {
        return Ok(());
    }
    replace_bin(&fs::read(&me)?)
}

/// Replaces /usr/local/bin/mikan at once; the running process keeps its old copy.
pub fn replace_bin(data: &[u8]) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;
    let tmp = format!("{BIN}.new");
    fs::write(&tmp, data)?;
    fs::set_permissions(&tmp, fs::Permissions::from_mode(0o755))?;
    fs::rename(&tmp, BIN).with_context(|| format!("replace {BIN}"))
}

const UNITS: [(&str, &str); 4] = [
    (
        "mikan-update.service",
        "[Unit]
Description=mikan: update when automatic updates are on
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/mikan update --auto
",
    ),
    (
        "mikan-update.timer",
        "[Unit]
Description=mikan: look for an update once a day

[Timer]
OnCalendar=*-*-* 03:00:00
RandomizedDelaySec=3h
Persistent=true

[Install]
WantedBy=timers.target
",
    ),
    (
        "mikan-update-request.service",
        "[Unit]
Description=mikan: update asked for in the panel
After=docker.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/mikan update --requested
",
    ),
    (
        "mikan-update-request.path",
        "[Unit]
Description=mikan: wait for an update request from the panel

[Path]
PathExists=/opt/mikan/data/panel/update/request
Unit=mikan-update-request.service

[Install]
WantedBy=paths.target
",
    ),
];

/// The update timer, and for a panel the watch on its update button.
pub fn install_units(panel: bool) -> Result<()> {
    if !Path::new("/run/systemd/system").exists() {
        bail!("no systemd");
    }
    for (name, text) in UNITS {
        fs::write(format!("/etc/systemd/system/{name}"), text)?;
    }
    systemctl(&["daemon-reload"])?;
    systemctl(&["enable", "--now", "mikan-update.timer"])?;
    if panel {
        systemctl(&["enable", "--now", "mikan-update-request.path"])?;
    }
    Ok(())
}

pub fn remove_units() {
    let _ = systemctl(&["disable", "--now", "mikan-update.timer", "mikan-update-request.path"]);
    for (name, _) in UNITS {
        let _ = fs::remove_file(format!("/etc/systemd/system/{name}"));
    }
    let _ = systemctl(&["daemon-reload"]);
}

fn systemctl(args: &[&str]) -> Result<()> {
    let ok = Command::new("systemctl").args(args).stdout(Stdio::null()).stderr(Stdio::null()).status().is_ok_and(|s| s.success());
    if !ok {
        bail!("systemctl {} failed", args.join(" "));
    }
    Ok(())
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

    #[test]
    fn firewall_rules() {
        let r = rules(Some(21355), None);
        assert_eq!(&r[..4], ["21355/tcp", "80/tcp", "443/tcp", "443/udp"]);
        assert!(r.contains(&"9443/udp".to_string()));
        assert_eq!(rules(None, Some(25305))[0], "25305/tcp");
    }
}
