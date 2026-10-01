//! Payment adapters of the marketplace (getmikan/marketplace): small services next to the
//! panel, one per payment provider, run as their own compose project so panel updates never
//! touch them. The panel has no Docker socket: it asks through files in its data directory
//! (internal/panel/addons), and `mikan update --requested` hands the request here.
//!
//!   addons/request.json  the panel: {"action": "install" | "remove", "id", "at"}
//!   addons/state.json    this: what runs where, with its token, and how the last request went

use std::collections::BTreeMap;
use std::fs;
use std::path::Path;
use std::process::{Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};

use anyhow::{Context, Result, bail};
use ed25519_dalek::VerifyingKey;
use serde::{Deserialize, Serialize};

use crate::envfile::write_private;
use crate::{docker, net, release, setup};

pub const CATALOG_URL: &str = "https://github.com/getmikan/marketplace/releases/latest/download/index.json";
/// The adapter protocol the panel and this command speak.
const PROTOCOL: u32 = 1;
/// The panel sees it as /data/panel/addons.
const STATE_DIR: &str = "/opt/mikan/data/panel/addons";
const COMPOSE_DIR: &str = "/opt/mikan/addons";
const PROJECT: &str = "mikan-addons";

#[derive(Deserialize, Debug, Clone)]
pub struct Entry {
    pub id: String,
    pub version: String,
    pub protocol: u32,
    pub image: String,
    pub digest: String,
    #[serde(default)]
    pub min_panel: String,
    #[serde(default)]
    pub name: BTreeMap<String, String>,
}

#[derive(Deserialize)]
struct Catalog {
    adapters: Vec<Entry>,
}

#[derive(Serialize, Deserialize, Debug, Clone, Default, PartialEq)]
pub struct Installed {
    pub version: String,
    pub image: String,
    pub digest: String,
    pub listen: String,
    pub token: String,
    pub status: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub error: String,
    pub at: String,
}

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct LastRequest {
    pub id: String,
    pub action: String,
    pub state: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub error: String,
    pub at: String,
}

#[derive(Serialize, Deserialize, Debug, Default)]
pub struct State {
    #[serde(default)]
    pub adapters: BTreeMap<String, Installed>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub request: Option<LastRequest>,
}

#[derive(Deserialize)]
struct Request {
    action: String,
    id: String,
}

/// Adapter ids are path, YAML and provider-name safe (internal/panel/addons.ValidID).
pub fn valid_id(id: &str) -> bool {
    let b = id.as_bytes();
    !b.is_empty() && b.len() <= 32 && b[0] != b'-' && b.iter().all(|&c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'-')
}

fn valid_digest(d: &str) -> bool {
    d.strip_prefix("sha256:").is_some_and(|h| h.len() == 64 && h.bytes().all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b)))
}

fn valid_image(i: &str) -> bool {
    i.strip_prefix("ghcr.io/")
        .is_some_and(|r| !r.is_empty() && r.bytes().all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b"._/-".contains(&b)))
}

/// Checks the signature over the catalog's exact bytes, then keeps what this panel can run:
/// protocol v1, an image by digest on ghcr.io, a panel new enough.
pub fn parse_catalog(data: &[u8], sig: &str, key: &VerifyingKey, panel: &str) -> Result<Vec<Entry>> {
    release::verify(data, sig, key, "catalog")?;
    let c: Catalog = serde_json::from_slice(data).context("the catalog is malformed")?;
    Ok(c.adapters
        .into_iter()
        .filter(|e| valid_id(&e.id) && e.protocol == PROTOCOL && valid_image(&e.image) && valid_digest(&e.digest))
        .filter(|e| e.min_panel.is_empty() || panel == "dev" || !release::newer(&e.min_panel, panel))
        .collect())
}

pub fn catalog(panel: &str) -> Result<Vec<Entry>> {
    let data = net::get(CATALOG_URL, 1 << 20).context("download the marketplace catalog")?;
    let sig = net::get(&format!("{CATALOG_URL}.sig"), 4096).context("download the catalog's signature")?;
    parse_catalog(&data, &String::from_utf8_lossy(&sig), &release::key()?, panel)
}

pub fn load_state() -> Result<State> {
    match fs::read(Path::new(STATE_DIR).join("state.json")) {
        Ok(b) => serde_json::from_slice(&b).context("addons/state.json is malformed"),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(State::default()),
        Err(e) => Err(e.into()),
    }
}

/// Only the panel (65532) reads the state: it holds the adapters' tokens.
fn save_state(s: &State) -> Result<()> {
    let dir = Path::new(STATE_DIR);
    fs::create_dir_all(dir)?;
    let path = dir.join("state.json");
    write_private(&path, serde_json::to_string_pretty(s)?.as_bytes())?;
    for p in [dir, path.as_path()] {
        let ok = Command::new("chown").args(["65532:65532"]).arg(p).status().is_ok_and(|s| s.success());
        if !ok {
            bail!("chown {} failed", p.display());
        }
    }
    Ok(())
}

/// The compose project of the adapters, hardened like the panel and with even less: no
/// capabilities at all, loopback only (the adapter refuses anything else).
pub fn compose_yaml(adapters: &BTreeMap<String, Installed>) -> String {
    let mut y = String::from("# Written by `mikan addon`: changes here are lost.\nname: mikan-addons\n\nservices:\n");
    for (id, a) in adapters {
        y.push_str(&format!(
            r#"  {id}:
    image: "{image}@{digest}"
    network_mode: host
    restart: unless-stopped
    user: "65532:65532"
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    read_only: true
    tmpfs: ["/tmp:rw,size=16m"]
    mem_limit: 128m
    pids_limit: 64
    logging:
      driver: json-file
      options: {{max-size: "5m", max-file: "2"}}
    environment:
      MIKAN_ADAPTER_LISTEN: "{listen}"
      MIKAN_ADAPTER_TOKEN: "{token}"
"#,
            image = a.image,
            digest = a.digest,
            listen = a.listen,
            token = a.token
        ));
    }
    y
}

fn compose(args: &[&str]) -> Result<()> {
    let file = format!("{COMPOSE_DIR}/compose.yml");
    let out = Command::new("docker")
        .args(["compose", "-p", PROJECT, "-f", &file])
        .args(args)
        .stdin(Stdio::null())
        .output()
        .context("docker compose")?;
    if !out.status.success() {
        bail!("docker compose {}: {}", args.join(" "), String::from_utf8_lossy(&out.stderr).trim());
    }
    Ok(())
}

/// Runs what the state lists, and nothing else.
fn up(adapters: &BTreeMap<String, Installed>) -> Result<()> {
    let dir = Path::new(COMPOSE_DIR);
    fs::create_dir_all(dir)?;
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(dir, fs::Permissions::from_mode(0o700))?;
    }
    let running: BTreeMap<String, Installed> =
        adapters.iter().filter(|(_, a)| a.status == "running").map(|(k, v)| (k.clone(), v.clone())).collect();
    if running.is_empty() {
        if dir.join("compose.yml").exists() {
            compose(&["down", "--remove-orphans"])?;
            fs::remove_file(dir.join("compose.yml"))?;
        }
        return Ok(());
    }
    write_private(&dir.join("compose.yml"), compose_yaml(&running).as_bytes())?;
    compose(&["up", "-d", "--remove-orphans"])
}

/// Waits for the adapter to answer /v1/info as itself.
fn wait_info(id: &str, a: &Installed, limit: Duration) -> Result<()> {
    let agent: ureq::Agent = ureq::Agent::config_builder().timeout_global(Some(Duration::from_secs(3))).build().into();
    let url = format!("http://{}/v1/info", a.listen);
    let start = Instant::now();
    let mut last = String::new();
    while start.elapsed() < limit {
        match agent.get(&url).header("authorization", &format!("Bearer {}", a.token)).call() {
            Ok(mut resp) => {
                let info: serde_json::Value = resp.body_mut().read_json().context("the adapter's /v1/info is malformed")?;
                if info["id"] != id || info["protocol"] != PROTOCOL {
                    bail!("the adapter answers as {} (protocol {})", info["id"], info["protocol"]);
                }
                return Ok(());
            }
            Err(e) => last = e.to_string(),
        }
        thread::sleep(Duration::from_millis(500));
    }
    bail!("the adapter does not answer: {last}")
}

fn now() -> String {
    crate::ops::now("+%Y-%m-%dT%H:%M:%SZ")
}

/// Installs an adapter from the catalog, or updates it to the catalog's build; a build that
/// does not start leaves the one before it running.
pub fn install(id: &str, panel: &str, say: &mut dyn FnMut(&str)) -> Result<()> {
    if !valid_id(id) {
        bail!("no adapter {id:?}");
    }
    let entry = catalog(panel)?.into_iter().find(|e| e.id == id).with_context(|| format!("the marketplace has no adapter {id}"))?;
    let mut state = load_state()?;
    let before = state.adapters.get(id).cloned();
    if let Some(b) = &before
        && b.digest == entry.digest
        && b.status == "running"
    {
        say(&format!("{id} {} is installed already.", b.version));
        return Ok(());
    }
    let reference = format!("{}@{}", entry.image, entry.digest);
    say(&format!("Pulling {reference}"));
    docker::pull(&reference, |_| {})?;
    // The port and the token stay across updates; the panel reads them from the state.
    let (listen, token) = match &before {
        Some(b) if !b.listen.is_empty() && !b.token.is_empty() => (b.listen.clone(), b.token.clone()),
        _ => (
            format!("127.0.0.1:{}", setup::free_port()),
            setup::token(48, b"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"),
        ),
    };
    let next = Installed {
        version: entry.version.clone(),
        image: entry.image.clone(),
        digest: entry.digest.clone(),
        listen,
        token,
        status: "running".into(),
        error: String::new(),
        at: now(),
    };
    state.adapters.insert(id.to_owned(), next.clone());
    let started = up(&state.adapters).and_then(|()| wait_info(id, &next, Duration::from_secs(30)));
    if let Err(e) = started {
        match before {
            Some(b) => {
                say(&format!("{id} {} did not start: going back to {}", entry.version, b.version));
                state.adapters.insert(id.to_owned(), b);
            }
            None => {
                state.adapters.remove(id);
            }
        }
        let back = up(&state.adapters);
        save_state(&state)?;
        back?;
        return Err(e.context(format!("{id} {} did not start", entry.version)));
    }
    save_state(&state)?;
    say(&format!("{id} {} runs on {}.", next.version, next.listen));
    Ok(())
}

/// Stops an adapter and forgets it; the panel keeps its settings and payments.
pub fn remove(id: &str, say: &mut dyn FnMut(&str)) -> Result<()> {
    let mut state = load_state()?;
    if state.adapters.remove(id).is_none() {
        bail!("{id} is not installed");
    }
    up(&state.adapters)?;
    save_state(&state)?;
    say(&format!("{id} is removed."));
    Ok(())
}

/// Whether the panel asked for something.
pub fn pending() -> bool {
    Path::new(STATE_DIR).join("request.json").exists()
}

/// Does what the panel asked for and tells it how it went.
pub fn apply(panel: &str, say: &mut dyn FnMut(&str)) -> Result<()> {
    let path = Path::new(STATE_DIR).join("request.json");
    let data = match fs::read(&path) {
        Ok(d) => d,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(e) => return Err(e.into()),
    };
    // Taken first: a request that fails is not tried again and again.
    fs::remove_file(&path)?;
    let req: Request = serde_json::from_slice(&data).context("addons/request.json is malformed")?;
    let result = match req.action.as_str() {
        "install" if valid_id(&req.id) => install(&req.id, panel, say),
        "remove" if valid_id(&req.id) => remove(&req.id, say),
        _ => Err(anyhow::anyhow!("unknown request {} {:?}", req.action, req.id)),
    };
    let mut state = load_state()?;
    state.request = Some(LastRequest {
        id: req.id,
        action: req.action,
        state: if result.is_ok() { "done" } else { "failed" }.into(),
        error: result.as_ref().err().map(|e| format!("{e:#}")).unwrap_or_default(),
        at: now(),
    });
    save_state(&state)?;
    result
}

/// `mikan addon list`: what runs here and what the marketplace offers.
pub fn list(panel: &str) -> Result<()> {
    let state = load_state()?;
    if state.adapters.is_empty() {
        println!("No payment adapters installed.");
    }
    for (id, a) in &state.adapters {
        let err = if a.error.is_empty() { String::new() } else { format!(": {}", a.error) };
        println!("{id:<16} {:<10} {:<8} {}{err}", a.version, a.status, a.listen);
    }
    match catalog(panel) {
        Ok(entries) => {
            println!("\nIn the marketplace:");
            for e in entries {
                let name = e.name.get("en").or_else(|| e.name.values().next()).cloned().unwrap_or_default();
                let mark = match state.adapters.get(&e.id) {
                    Some(a) if a.digest == e.digest => "installed",
                    Some(_) => "update",
                    None => "",
                };
                println!("{:<16} {:<10} {:<10} {name}", e.id, e.version, mark);
            }
        }
        Err(e) => println!("\nThe marketplace is out of reach: {e:#}"),
    }
    Ok(())
}

/// Stops every adapter (mikan uninstall); the state stays with the data.
pub fn down() {
    if Path::new(COMPOSE_DIR).join("compose.yml").exists() {
        let _ = compose(&["down", "--remove-orphans"]);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use base64::Engine;
    use base64::engine::general_purpose::STANDARD;
    use ed25519_dalek::{Signer, SigningKey};

    fn entry(id: &str, protocol: u32, image: &str, digest: &str, min: &str) -> String {
        format!(
            r#"{{"id":"{id}","version":"1.0.0","protocol":{protocol},"image":"{image}","digest":"{digest}","min_panel":"{min}","name":{{"en":"X"}},"homepage":"h"}}"#
        )
    }

    #[test]
    fn signed_catalog_only() {
        let signer = SigningKey::from_bytes(&[9; 32]);
        let key = signer.verifying_key();
        let d = format!("sha256:{}", "c".repeat(64));
        let data = format!(
            r#"{{"version":1,"updated":"2026-10-01T00:00:00Z","adapters":[{},{},{},{},{},{}]}}"#,
            entry("yookassa", 1, "ghcr.io/getmikan/adapter-yookassa", &d, "0.4.3"),
            entry("future", 2, "ghcr.io/getmikan/adapter-future", &d, ""),
            entry("hub", 1, "docker.io/someone/adapter", &d, ""),
            entry("tagged", 1, "ghcr.io/getmikan/adapter-x", "latest", ""),
            entry("Bad", 1, "ghcr.io/getmikan/adapter-x", &d, ""),
            entry("newer", 1, "ghcr.io/getmikan/adapter-x", &d, "9.0.0"),
        )
        .into_bytes();
        let sig = STANDARD.encode(signer.sign(&data).to_bytes());
        let got = parse_catalog(&data, &format!("{sig}\n"), &key, "0.4.3").unwrap();
        assert_eq!(got.iter().map(|e| e.id.as_str()).collect::<Vec<_>>(), ["yookassa"]);
        assert!(parse_catalog(&data, &sig, &key, "0.4.2").unwrap().is_empty());
        let mut tampered = data.clone();
        tampered[20] ^= 1;
        assert!(parse_catalog(&tampered, &sig, &key, "0.4.3").is_err());
        assert!(parse_catalog(&data, &sig, &SigningKey::from_bytes(&[8; 32]).verifying_key(), "0.4.3").is_err());
    }

    #[test]
    fn ids() {
        for ok in ["yookassa", "a", "pay-2"] {
            assert!(valid_id(ok), "{ok}");
        }
        for bad in ["", "-x", "Yoo", "a/b", "a b", "../x", &"x".repeat(33)] {
            assert!(!valid_id(bad), "{bad}");
        }
    }

    // The panel (Go) reads the same state: the field names are the contract.
    #[test]
    fn state_matches_the_panel() {
        let mut s = State::default();
        s.adapters.insert(
            "yookassa".into(),
            Installed {
                version: "1.0.0".into(),
                image: "ghcr.io/getmikan/adapter-yookassa".into(),
                digest: "sha256:1".into(),
                listen: "127.0.0.1:41873".into(),
                token: "t".into(),
                status: "running".into(),
                error: String::new(),
                at: "2026-10-01T00:00:00Z".into(),
            },
        );
        s.request = Some(LastRequest {
            id: "yookassa".into(),
            action: "install".into(),
            state: "done".into(),
            error: String::new(),
            at: "x".into(),
        });
        let v: serde_json::Value = serde_json::to_value(&s).unwrap();
        let a = &v["adapters"]["yookassa"];
        for k in ["version", "digest", "listen", "token", "status", "at"] {
            assert!(a.get(k).is_some(), "{k}");
        }
        assert_eq!(v["request"]["state"], "done");
        let go = fs::read_to_string("../internal/panel/addons/addons.go").unwrap();
        for tag in [
            "`json:\"listen\"`",
            "`json:\"token\"`",
            "`json:\"status\"`",
            "`json:\"adapters\"`",
            "`json:\"request,omitempty\"`",
            "`json:\"state\"`",
        ] {
            assert!(go.contains(tag), "the panel has no {tag}");
        }
        assert!(go.contains(&format!("const CatalogURL = \"{CATALOG_URL}\"")), "the panel's catalog URL differs");
    }

    #[test]
    fn compose_is_hardened() {
        let mut m = BTreeMap::new();
        m.insert(
            "yookassa".to_owned(),
            Installed {
                image: "ghcr.io/getmikan/adapter-yookassa".into(),
                digest: format!("sha256:{}", "c".repeat(64)),
                listen: "127.0.0.1:41873".into(),
                token: "tok".into(),
                status: "running".into(),
                ..Default::default()
            },
        );
        let y = compose_yaml(&m);
        for want in [
            "name: mikan-addons",
            "  yookassa:\n",
            &format!("image: \"ghcr.io/getmikan/adapter-yookassa@sha256:{}\"", "c".repeat(64)),
            "user: \"65532:65532\"",
            "cap_drop: [ALL]",
            "read_only: true",
            "no-new-privileges:true",
            "MIKAN_ADAPTER_LISTEN: \"127.0.0.1:41873\"",
            "MIKAN_ADAPTER_TOKEN: \"tok\"",
        ] {
            assert!(y.contains(want), "no {want} in\n{y}");
        }
        assert!(!y.contains("cap_add"), "an adapter needs no capability");
    }
}
