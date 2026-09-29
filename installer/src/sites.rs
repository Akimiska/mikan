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

/// Points every REALITY inbound at site; returns what the panel reports.
pub fn apply(site: &Site) -> Result<String> {
    let out = docker::check(docker::admin(&["targets", "apply", "--all", "--dest", &site.dest, "--sni", &site.sni], None)?)?;
    Ok(String::from_utf8_lossy(&out.stderr).trim().to_owned())
}

/// The site to take: the panel's own domain once its certificate is there, else the
/// fastest neighbor; None keeps the current sites.
pub fn best(scan: &Scan) -> Option<Site> {
    scan.self_steal.clone().filter(|s| s.ok).or_else(|| scan.results.first().cloned())
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
    match apply(&site) {
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
        assert_eq!(best(&s).unwrap().sni, "shop.example.net");
        s.self_steal.as_mut().unwrap().ok = true;
        assert_eq!(best(&s).unwrap().sni, "vpn.example.com");
        s.self_steal = None;
        s.results.clear();
        assert!(best(&s).is_none());
    }
}
