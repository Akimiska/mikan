//! REALITY camouflage sites. The panel's `mikan admin targets` finds and checks them from
//! the node itself; the installer points every REALITY inbound at one.

use std::thread;
use std::time::Duration;

use anyhow::{Context, Result};
use serde::Deserialize;

use crate::docker;

/// A site as the panel's scan checked it (internal/scan.Result).
#[derive(Deserialize, Debug, Clone, Default, PartialEq)]
pub struct Site {
    pub dest: String,
    pub sni: String,
    #[serde(default)]
    pub rtt_ms: u32,
    #[serde(default)]
    pub error: String,
    #[serde(default)]
    pub ok: bool,
}

/// Where a REALITY inbound points now.
#[derive(Deserialize, Debug, Clone, Default)]
pub struct Target {
    pub inbound: String,
    pub dest: String,
    pub sni: String,
}

/// What `mikan admin targets scan --json` prints.
#[derive(Deserialize, Debug, Clone, Default)]
pub struct Scan {
    pub ip: String,
    pub scanned: u32,
    #[serde(default)]
    pub self_steal: Option<Site>,
    #[serde(default)]
    pub results: Vec<Site>,
    #[serde(default)]
    pub current: Vec<Target>,
}

pub fn scan() -> Result<Scan> {
    let out = docker::check(docker::admin(&["targets", "scan", "--json"], None)?)?;
    serde_json::from_slice(&out.stdout).context("read the panel's scan")
}

pub fn check(site: &Site) -> Result<Site> {
    let out = docker::check(docker::admin(&["targets", "check", "--dest", &site.dest, "--sni", &site.sni, "--json"], None)?)?;
    serde_json::from_slice(&out.stdout).context("read the panel's check")
}

/// Said when the camouflage is someone else's site: hosters and filters notice a name that
/// does not lead to the server's address (GitHub issue #67).
pub const FOREIGN: &str = "⚠ This is someone else's site next to your server: hosters and filters notice a name \
    that does not lead to the server's address. With a domain of your own pointed here, pick it in mikan → REALITY sites.";

/// Points every REALITY inbound at site; returns what the panel reports. own is the
/// panel's own domain, when it has one: anything else gets the FOREIGN note.
pub fn apply(site: &Site, own: Option<&Site>) -> Result<String> {
    let mut args = vec!["targets", "apply", "--all", "--dest", &site.dest, "--sni", &site.sni];
    // Only the own domain fails the check here (neighbors are listed when they pass): its
    // certificate comes within minutes, and clients do not need it to connect.
    if !site.ok {
        args.push("--force");
    }
    let out = docker::check(docker::admin(&args, None)?)?;
    let mut msg = String::from_utf8_lossy(&out.stderr).trim().to_owned();
    if !own.is_some_and(|o| o.dest == site.dest && o.sni == site.sni) {
        msg.push('\n');
        msg.push_str(FOREIGN);
    } else if !site.ok {
        msg.push_str(&format!("\nThe certificate of {} is on its way: the panel gets it by itself.", site.sni));
    }
    Ok(msg)
}

/// The site to take: the panel's own domain whenever there is one, its certificate ready
/// or not, else the fastest neighbor; None keeps the current sites.
pub fn best(scan: &Scan) -> Option<Site> {
    scan.self_steal.clone().or_else(|| scan.results.first().cloned())
}

/// The domain's Let's Encrypt certificate comes a few seconds after the panel starts:
/// until then its HTTPS does not pass. Waits up to a minute.
pub fn wait_own(own: &Site) -> Option<Site> {
    for _ in 0..12 {
        if let Ok(s) = check(own)
            && s.ok
        {
            return Some(s);
        }
        thread::sleep(Duration::from_secs(5));
    }
    None
}

/// The SNI step of an install without questions.
pub fn auto(mut say: impl FnMut(&str)) {
    say("▸ REALITY camouflage");
    let mut s = match scan() {
        Ok(s) => s,
        Err(e) => {
            say(&format!("  the scan failed, the default site stays: {e:#}"));
            return;
        }
    };
    if let Some(own) = s.self_steal.clone().filter(|o| !o.ok) {
        say(&format!("  waiting for the certificate of {}", own.sni));
        if let Some(ready) = wait_own(&own) {
            s.self_steal = Some(ready);
        }
    }
    let Some(site) = best(&s) else {
        say("  no site next to the server passed the checks: the default site stays");
        return;
    };
    match apply(&site, s.self_steal.as_ref()) {
        Ok(msg) => msg.lines().for_each(|l| say(&format!("  {l}"))),
        Err(e) => say(&format!("  {e:#}")),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn picks_own_domain_then_fastest() {
        let json = r#"{"node":1,"ip":"203.0.113.10","scanned":253,"from_node":true,
            "self_steal":{"dest":"127.0.0.1:21355","sni":"vpn.example.com","ip":"127.0.0.1","rtt_ms":0,"tls13":true,"h2":true,"x25519":true,"cert_valid":false,"dns_match":false,"ok":false},
            "results":[{"dest":"203.0.113.45:443","sni":"shop.example.net","ip":"203.0.113.45","rtt_ms":3,"tls13":true,"h2":true,"x25519":true,"cert_valid":true,"dns_match":true,"ok":true}],
            "current":[{"inbound":"vless-xhttp","enabled":true,"dest":"www.microsoft.com:443","sni":"www.microsoft.com"}]}"#;
        let mut s: Scan = serde_json::from_str(json).unwrap();
        // Even before its certificate: someone else's domain next door gets the server
        // noticed (GitHub issue #67).
        assert_eq!(best(&s).unwrap().sni, "vpn.example.com");
        s.self_steal = None;
        assert_eq!(best(&s).unwrap().sni, "shop.example.net");
        s.results.clear();
        assert!(best(&s).is_none());
    }
}
