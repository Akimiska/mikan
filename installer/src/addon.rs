//! Addons of the marketplace (getmikan/marketplace): small services next to the panel, run
//! as their own compose project so panel updates never touch them. Payment adapters speak
//! protocol 1; bots, pages and the rest protocol 2, which also gives them a token to call the
//! panel's API for addons. The panel has no Docker socket: it asks through files in its data
//! directory (internal/panel/addons), and `mikan update --requested` hands the request here.
//!
//!   addons/request.json  the panel: {"action": "install" | "remove", "id", "at"}
//!   addons/state.json    this: what runs where, with its tokens, the address of the panel's
//!                        API for addons, and how the last request went

use std::collections::BTreeMap;
use std::fs;
use std::path::Path;
use std::process::{Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};

use anyhow::{Context, Result, bail};
use ed25519_dalek::VerifyingKey;
use serde::{Deserialize, Serialize};

use crate::lock::{self, Wait};
use crate::panelfs::Dir;
use crate::{docker, net, panelfs, release, setup};

pub const CATALOG_URL: &str = "https://github.com/getmikan/marketplace/releases/latest/download/index.json";
/// The payment adapters' protocol, and that of every other kind (internal/panel/addons).
const PROTOCOL: u32 = 1;
const PROTOCOL_2: u32 = 2;
const KINDS_2: [&str; 3] = ["bot", "ui", "generic"];
const MODES: [&str; 2] = ["http", "client"];
const PERMISSIONS: [&str; 7] = ["bot", "users:read", "users:write", "billing:invoice", "events", "kv", "ui"];
/// What an addon may ask for, in MiB; 0 is DEFAULT_MEM_MB.
const MEM_MB: std::ops::RangeInclusive<u32> = 64..=256;
const DEFAULT_MEM_MB: u32 = 128;
/// Where the panel and this command meet, below DIR; the panel sees it as
/// /data/panel/addons. What lies there is the panel's to write: root reads only its
/// requests from there, through panelfs, and keeps its own copy of the state.
const PANEL_DIR: &str = "data/panel/addons";
const COMPOSE_DIR: &str = "/opt/mikan/addons";
/// The state root trusts: the panel cannot see or change it.
const ROOT_STATE: &str = "/opt/mikan/addons/state.json";
const PROJECT: &str = "mikan-addons";

/// The panel's side of the exchange, opened without following a link; None while the
/// panel has not made it.
fn panel_dir(create: bool) -> Result<Option<Dir>> {
    Dir::open(Path::new(crate::DIR), PANEL_DIR, create)
}

#[derive(Deserialize, Debug, Clone)]
pub struct Entry {
    pub id: String,
    pub version: String,
    pub protocol: u32,
    /// Protocol 2: bot | ui | generic; protocol 1 is a payment adapter.
    #[serde(default)]
    pub kind: String,
    /// Protocol 2: http (serves /v2/*) | client (only calls the panel).
    #[serde(default)]
    pub mode: String,
    #[serde(default)]
    pub permissions: Vec<String>,
    #[serde(default)]
    pub resources: Resources,
    pub image: String,
    pub digest: String,
    #[serde(default)]
    pub min_panel: String,
    #[serde(default)]
    pub name: BTreeMap<String, String>,
}

#[derive(Deserialize, Debug, Clone, Default, PartialEq)]
pub struct Resources {
    #[serde(default)]
    pub mem_mb: u32,
}

impl Entry {
    /// Fills what protocol 1 leaves out and tells whether this host can run the entry: the
    /// right kind for its protocol, a known mode, known permissions, sane memory. The panel
    /// checks the same (Entry.normalize in internal/panel/addons).
    fn normalize(&mut self) -> bool {
        match self.protocol {
            PROTOCOL => {
                let plain = (self.kind.is_empty() || self.kind == "payment")
                    && (self.mode.is_empty() || self.mode == "http")
                    && self.permissions.is_empty()
                    && self.resources == Resources::default();
                self.kind = "payment".into();
                self.mode = "http".into();
                plain
            }
            PROTOCOL_2 => {
                if self.mode.is_empty() {
                    self.mode = "http".into();
                }
                let mut seen = std::collections::BTreeSet::new();
                KINDS_2.contains(&self.kind.as_str())
                    && MODES.contains(&self.mode.as_str())
                    && self.permissions.iter().all(|p| PERMISSIONS.contains(&p.as_str()) && seen.insert(p.as_str()))
                    && (self.resources.mem_mb == 0 || MEM_MB.contains(&self.resources.mem_mb))
            }
            _ => false,
        }
    }
}

#[derive(Deserialize)]
struct Catalog {
    adapters: Vec<Entry>,
}

/// An addon this host runs. `token` is what the panel calls the addon with; `panel_token`
/// (protocol 2) what the addon calls the panel with.
#[derive(Serialize, Deserialize, Debug, Clone, Default, PartialEq)]
pub struct Installed {
    pub version: String,
    pub image: String,
    pub digest: String,
    pub listen: String,
    pub token: String,
    /// 0 is 1: states written before protocol 2.
    #[serde(default, skip_serializing_if = "is_zero")]
    pub protocol: u32,
    /// "" is payment.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub kind: String,
    /// "" is http.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub mode: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub panel_token: String,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub mem_mb: u32,
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

fn is_zero(n: &u32) -> bool {
    *n == 0
}

impl Installed {
    fn v2(&self) -> bool {
        self.protocol == PROTOCOL_2
    }
}

#[derive(Serialize, Deserialize, Debug, Default)]
pub struct State {
    #[serde(default)]
    pub adapters: BTreeMap<String, Installed>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub request: Option<LastRequest>,
    /// The loopback address the panel serves its API for addons on: given out here once, with
    /// the first addon of protocol 2, and kept.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub panel_api: String,
    /// The version of mikan that wrote this state: the panel holds back what needs a newer host.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub host_version: String,
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

/// Where the marketplace's adapters live. A signed catalog could name another registry, but
/// what root runs from a state the panel once wrote must come from this organization.
const IMAGE_PREFIX: &str = "ghcr.io/getmikan/";

fn valid_image(i: &str) -> bool {
    i.strip_prefix(IMAGE_PREFIX).is_some_and(|r| {
        !r.is_empty()
            && !r.contains("..")
            && !r.contains("//")
            && r.bytes().all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b"._/-".contains(&b))
    })
}

/// Checks the signature over the catalog's exact bytes, then keeps what this panel can run:
/// a protocol it knows, an image by digest on ghcr.io, a panel new enough.
pub fn parse_catalog(data: &[u8], sig: &str, key: &VerifyingKey, panel: &str) -> Result<Vec<Entry>> {
    release::verify(data, sig, key, "catalog")?;
    let c: Catalog = serde_json::from_slice(data).context("the catalog is malformed")?;
    Ok(c.adapters
        .into_iter()
        .filter_map(|mut e| e.normalize().then_some(e))
        .filter(|e| valid_id(&e.id) && valid_image(&e.image) && valid_digest(&e.digest))
        .filter(|e| e.min_panel.is_empty() || panel == "dev" || !release::newer(&e.min_panel, panel))
        .collect())
}

pub fn catalog(panel: &str) -> Result<Vec<Entry>> {
    let data = net::get(CATALOG_URL, 1 << 20).context("download the marketplace catalog")?;
    let sig = net::get(&format!("{CATALOG_URL}.sig"), 4096).context("download the catalog's signature")?;
    let entries = parse_catalog(&data, &String::from_utf8_lossy(&sig), &release::key()?, panel)?;
    release::not_older(Path::new(CATALOG_SEEN), &data, "updated", "the marketplace's catalog")?;
    Ok(entries)
}

/// The newest catalog this server has accepted ("updated" of the signed index).
const CATALOG_SEEN: &str = "/opt/mikan/addons/catalog-updated";

/// The adapters as root keeps them. 0.4.3 kept them only in the panel's directory: such a
/// state is taken once, entry by entry, and only entries that pass sound() survive.
pub fn load_state() -> Result<State> {
    let root_state = Path::new(ROOT_STATE);
    // The panel's directory matters only for the one-time take-over of 0.4.3's state.
    if root_state.exists() { load_from(root_state, None) } else { load_from(root_state, panel_dir(false)?.as_ref()) }
}

/// The first read after 0.4.3 takes the panel's state and writes root's own copy at once,
/// so the panel's file is never read again.
fn load_from(root_state: &Path, panel: Option<&Dir>) -> Result<State> {
    let (raw, migrated) = match fs::read(root_state) {
        Ok(b) => (b, false),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => match panel.map(|d| d.read("state.json", 64 << 10)).transpose()?.flatten() {
            Some(b) => (b, true),
            None => return Ok(State::default()),
        },
        Err(e) => return Err(e.into()),
    };
    let mut s: State = serde_json::from_slice(&raw).context("the adapters' state is malformed")?;
    if !s.panel_api.is_empty() && !loopback(&s.panel_api) {
        eprintln!("mikan: the panel's API address {:?} in the state is not sound: ignored", s.panel_api);
        s.panel_api.clear();
    }
    s.adapters.retain(|id, a| {
        let ok = sound(id, a);
        if !ok {
            eprintln!("mikan: the adapter {id:?} in the state is not sound: ignored");
        }
        ok
    });
    if migrated {
        panelfs::write_root(root_state, serde_json::to_string_pretty(&s)?.as_bytes())?;
    }
    Ok(s)
}

/// root's copy first, then the panel's, which holds the tokens it calls adapters with.
fn save_state(s: &mut State) -> Result<()> {
    s.host_version = crate::version().to_owned();
    let data = serde_json::to_string_pretty(s)?;
    panelfs::write_root(Path::new(ROOT_STATE), data.as_bytes())?;
    let dir = panel_dir(true)?.context("the panel's directory for the adapters is not there")?;
    dir.write("state.json", data.as_bytes(), 0o600)
}

/// A loopback address on an unprivileged port.
fn loopback(addr: &str) -> bool {
    addr.strip_prefix("127.0.0.1:").and_then(|p| p.parse::<u16>().ok()).is_some_and(|p| p >= 1024)
}

fn plain_token(t: &str) -> bool {
    (16..=128).contains(&t.len()) && t.bytes().all(|b| b.is_ascii_alphanumeric())
}

/// Everything that goes into the compose file is checked here: an id, an image on ghcr.io
/// by digest, a loopback listen address, plain tokens and known words, so no value can carry
/// YAML. A payment adapter (protocol 1) carries nothing of protocol 2.
fn sound(id: &str, a: &Installed) -> bool {
    let version_ok = a.version.len() <= 32 && a.version.bytes().all(|b| b.is_ascii_alphanumeric() || b".-+".contains(&b));
    let protocol_ok = match a.protocol {
        0 | PROTOCOL => {
            (a.kind.is_empty() || a.kind == "payment")
                && (a.mode.is_empty() || a.mode == "http")
                && a.panel_token.is_empty()
                && a.mem_mb == 0
        }
        PROTOCOL_2 => {
            KINDS_2.contains(&a.kind.as_str())
                && MODES.contains(&a.mode.as_str())
                && plain_token(&a.panel_token)
                && (a.mem_mb == 0 || MEM_MB.contains(&a.mem_mb))
        }
        _ => false,
    };
    valid_id(id)
        && valid_image(&a.image)
        && valid_digest(&a.digest)
        && loopback(&a.listen)
        && plain_token(&a.token)
        && version_ok
        && protocol_ok
        && (a.status == "running" || a.status == "failed")
}

/// The compose project of the adapters, hardened like the panel and with even less: no
/// capabilities at all, loopback only (the adapter refuses anything else).
/// An addon of protocol 2 also gets the panel's API for addons; with no sound address for it
/// it is left out.
pub fn compose_yaml(adapters: &BTreeMap<String, Installed>, panel_api: &str) -> String {
    let mut y = String::from("# Written by `mikan addon`: changes here are lost.\nname: mikan-addons\n\nservices:\n");
    for (id, a) in adapters {
        if a.v2() && !loopback(panel_api) {
            continue;
        }
        let mem = if a.mem_mb == 0 { DEFAULT_MEM_MB } else { a.mem_mb };
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
    mem_limit: {mem}m
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
        if a.v2() {
            y.push_str(&format!(
                "      MIKAN_PANEL_URL: \"http://{panel_api}/panel/v2\"\n      MIKAN_PANEL_TOKEN: \"{}\"\n",
                a.panel_token
            ));
        }
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
fn up(state: &State) -> Result<()> {
    let adapters = &state.adapters;
    let dir = Path::new(COMPOSE_DIR);
    fs::create_dir_all(dir)?;
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(dir, fs::Permissions::from_mode(0o700))?;
    }
    let running: BTreeMap<String, Installed> =
        adapters.iter().filter(|(id, a)| a.status == "running" && sound(id, a)).map(|(k, v)| (k.clone(), v.clone())).collect();
    if running.is_empty() {
        if dir.join("compose.yml").exists() {
            compose(&["down", "--remove-orphans"])?;
            fs::remove_file(dir.join("compose.yml"))?;
        }
        return Ok(());
    }
    crate::envfile::write_private(&dir.join("compose.yml"), compose_yaml(&running, &state.panel_api).as_bytes())?;
    compose(&["up", "-d", "--remove-orphans"])
}

/// Waits for the addon to come up: one serving HTTP answers its info as itself (/v1/info for
/// a payment adapter, /v2/info for the rest); a client of the panel keeps running.
fn wait_started(id: &str, a: &Installed, limit: Duration) -> Result<()> {
    if a.v2() && a.mode == "client" {
        return wait_running(id, limit);
    }
    let agent: ureq::Agent = ureq::Agent::config_builder().timeout_global(Some(Duration::from_secs(3))).build().into();
    let (path, protocol) = if a.v2() { ("v2", PROTOCOL_2) } else { ("v1", PROTOCOL) };
    let url = format!("http://{}/{path}/info", a.listen);
    let start = Instant::now();
    let mut last = String::new();
    while start.elapsed() < limit {
        match agent.get(&url).header("authorization", &format!("Bearer {}", a.token)).call() {
            Ok(mut resp) => {
                let info: serde_json::Value =
                    resp.body_mut().read_json().with_context(|| format!("the addon's /{path}/info is malformed"))?;
                if info["id"] != id || info["protocol"] != protocol {
                    bail!("the addon answers as {} (protocol {})", info["id"], info["protocol"]);
                }
                return Ok(());
            }
            Err(e) => last = e.to_string(),
        }
        thread::sleep(Duration::from_millis(500));
    }
    bail!("the addon does not answer: {last}")
}

/// A client of the panel serves nothing to ask: it has to be running, and still be running a
/// few seconds later, so that one crashing at its start is caught.
fn wait_running(id: &str, limit: Duration) -> Result<()> {
    let start = Instant::now();
    let mut since: Option<Instant> = None;
    while start.elapsed() < limit {
        if running_services()?.iter().any(|s| s == id) {
            let first = *since.get_or_insert_with(Instant::now);
            if first.elapsed() >= Duration::from_secs(5) {
                return Ok(());
            }
        } else {
            since = None;
        }
        thread::sleep(Duration::from_millis(500));
    }
    bail!("the addon does not keep running")
}

fn running_services() -> Result<Vec<String>> {
    let file = format!("{COMPOSE_DIR}/compose.yml");
    let out = Command::new("docker")
        .args(["compose", "-p", PROJECT, "-f", &file, "ps", "--status", "running", "--services"])
        .stdin(Stdio::null())
        .output()
        .context("docker compose ps")?;
    if !out.status.success() {
        bail!("docker compose ps: {}", String::from_utf8_lossy(&out.stderr).trim());
    }
    Ok(String::from_utf8_lossy(&out.stdout).lines().map(str::to_owned).collect())
}

fn now() -> String {
    crate::clock::rfc3339()
}

fn new_token() -> String {
    setup::token(48, b"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
}

/// Installs an adapter from the catalog, or updates it to the catalog's build; a build that
/// does not start leaves the one before it running.
pub fn install(id: &str, panel: &str, say: &mut dyn FnMut(&str)) -> Result<()> {
    if !valid_id(id) {
        bail!("no adapter {id:?}");
    }
    let _lock = lock::acquire(Wait::Block, say)?;
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
    // The port and the tokens stay across updates; the panel reads them from the state.
    let (listen, token) = match &before {
        Some(b) if !b.listen.is_empty() && !b.token.is_empty() => (b.listen.clone(), b.token.clone()),
        _ => (format!("127.0.0.1:{}", setup::free_port()), new_token()),
    };
    let v2 = entry.protocol == PROTOCOL_2;
    let panel_token = match &before {
        _ if !v2 => String::new(),
        Some(b) if !b.panel_token.is_empty() => b.panel_token.clone(),
        _ => new_token(),
    };
    if v2 && state.panel_api.is_empty() {
        state.panel_api = format!("127.0.0.1:{}", setup::free_port());
    }
    let next = Installed {
        version: entry.version.clone(),
        image: entry.image.clone(),
        digest: entry.digest.clone(),
        listen,
        token,
        protocol: if v2 { PROTOCOL_2 } else { 0 },
        kind: if v2 { entry.kind.clone() } else { String::new() },
        mode: if v2 { entry.mode.clone() } else { String::new() },
        panel_token,
        mem_mb: entry.resources.mem_mb,
        status: "running".into(),
        error: String::new(),
        at: now(),
    };
    state.adapters.insert(id.to_owned(), next.clone());
    let started = up(&state).and_then(|()| wait_started(id, &next, Duration::from_secs(30)));
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
        let back = up(&state);
        save_state(&mut state)?;
        back?;
        return Err(e.context(format!("{id} {} did not start", entry.version)));
    }
    save_state(&mut state)?;
    say(&format!("{id} {} runs on {}.", next.version, next.listen));
    Ok(())
}

/// Stops an adapter and forgets it; the panel keeps its settings and payments.
pub fn remove(id: &str, say: &mut dyn FnMut(&str)) -> Result<()> {
    let _lock = lock::acquire(Wait::Block, say)?;
    let mut state = load_state()?;
    if state.adapters.remove(id).is_none() {
        bail!("{id} is not installed");
    }
    up(&state)?;
    save_state(&mut state)?;
    say(&format!("{id} is removed."));
    Ok(())
}

/// Whether the panel asked for something.
pub fn pending() -> bool {
    panel_dir(false).ok().flatten().is_some_and(|d| d.exists("request.json"))
}

/// Does what the panel asked for and tells it how it went.
pub fn apply(panel: &str, say: &mut dyn FnMut(&str)) -> Result<()> {
    let _lock = lock::acquire(Wait::Block, say)?;
    let Some(dir) = panel_dir(false)? else { return Ok(()) };
    // Taken first, whatever it is: a request that fails is not tried again and again.
    let data = dir.read("request.json", 4096);
    dir.discard("request.json")?;
    let data = match data {
        Ok(Some(d)) => d,
        Ok(None) => return Ok(()),
        Err(e) => {
            say(&format!("addons/request.json is not a request, removed: {e:#}"));
            return Err(e);
        }
    };
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
    save_state(&mut state)?;
    result
}

/// `mikan addon list`: what runs here and what the marketplace offers.
pub fn list(panel: &str) -> Result<()> {
    let state = load_state()?;
    if state.adapters.is_empty() {
        println!("No addons installed.");
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

/// Rebuild compose from validated state after restore; never run a compose file supplied
/// by a backup archive. Synchronize the panel's adapter tokens with root's state.
pub fn resume() -> Result<()> {
    let mut state = load_state()?;
    save_state(&mut state)?;
    up(&state)
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
    fn an_old_catalog_is_not_taken_again() {
        let d = tmpdir("catalog-seen");
        let seen = d.join("addons/catalog-updated");
        let cat = |u: &str| format!(r#"{{"version":1,"updated":"{u}","adapters":[]}}"#).into_bytes();
        let not_older = |doc: &[u8]| release::not_older(&seen, doc, "updated", "the catalog");
        not_older(&cat("2026-10-01T17:01:18Z")).unwrap();
        not_older(&cat("2026-10-01T17:01:18Z")).unwrap();
        not_older(&cat("2026-10-02T00:00:00Z")).unwrap();
        assert!(not_older(&cat("2026-10-01T17:01:18Z")).is_err(), "a rollback to a catalog of the day before");
        assert_eq!(fs::read_to_string(&seen).unwrap(), "2026-10-02T00:00:00Z");
        not_older(b"{}").unwrap();
        fs::remove_dir_all(&d).unwrap();
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
                ..Default::default()
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
            "`json:\"protocol,omitempty\"`",
            "`json:\"kind,omitempty\"`",
            "`json:\"mode,omitempty\"`",
            "`json:\"panel_token,omitempty\"`",
            "`json:\"panel_api,omitempty\"`",
            "`json:\"host_version,omitempty\"`",
        ] {
            assert!(go.contains(tag), "the panel has no {tag}");
        }
        assert!(go.contains(&format!("const CatalogURL = \"{CATALOG_URL}\"")), "the panel's catalog URL differs");
    }

    // A state written by a compromised panel cannot smuggle YAML into the compose file
    // root runs: every field that goes there is checked.
    #[test]
    fn only_sound_entries() {
        let good = Installed {
            version: "1.0.1".into(),
            image: "ghcr.io/getmikan/adapter-yookassa".into(),
            digest: format!("sha256:{}", "c".repeat(64)),
            listen: "127.0.0.1:45615".into(),
            token: "a".repeat(48),
            status: "running".into(),
            ..Default::default()
        };
        assert!(sound("yookassa", &good));
        let bad = |f: &dyn Fn(&mut Installed)| {
            let mut a = good.clone();
            f(&mut a);
            a
        };
        let cases = [
            (
                "image",
                bad(&|a| {
                    a.image = "ghcr.io/x\"
    privileged: true
    x: \""
                        .into()
                }),
            ),
            ("image elsewhere", bad(&|a| a.image = "docker.io/library/alpine".into())),
            ("another organization", bad(&|a| a.image = "ghcr.io/someone/adapter-yookassa".into())),
            ("a way out of the organization", bad(&|a| a.image = "ghcr.io/getmikan/../someone/x".into())),
            (
                "digest",
                bad(&|a| {
                    a.digest = "sha256:abc
    volumes: [\"/:/host\"]"
                        .into()
                }),
            ),
            ("listen", bad(&|a| a.listen = "0.0.0.0:45615".into())),
            ("low port", bad(&|a| a.listen = "127.0.0.1:22".into())),
            (
                "token",
                bad(&|a| {
                    a.token = format!(
                        "{}\"
    privileged: true",
                        "a".repeat(40)
                    )
                }),
            ),
            ("short token", bad(&|a| a.token = "abc".into())),
            (
                "version",
                bad(&|a| {
                    a.version = "1
"
                    .into()
                }),
            ),
            ("status", bad(&|a| a.status = "hacked".into())),
        ];
        for (what, a) in cases {
            assert!(!sound("yookassa", &a), "{what} passed");
        }
        assert!(
            !sound(
                "x:
  privileged",
                &good
            ),
            "a service name with YAML passed"
        );
        let mut m = BTreeMap::new();
        m.insert(
            "evil".to_owned(),
            bad(&|a| {
                a.image = "ghcr.io/x
    privileged: true"
                    .into()
            }),
        );
        m.insert("yookassa".to_owned(), good.clone());
        let mut s = State { adapters: m, ..Default::default() };
        s.adapters.retain(|id, a| sound(id, a));
        assert_eq!(s.adapters.keys().collect::<Vec<_>>(), ["yookassa"]);
        assert!(!compose_yaml(&s.adapters, "").contains("privileged"));
    }

    fn tmpdir(name: &str) -> std::path::PathBuf {
        let d = std::env::temp_dir().join(format!("addon-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&d);
        fs::create_dir_all(&d).unwrap();
        d
    }

    // 0.4.3 kept the state only where the panel can write it. The first read takes it entry
    // by entry, keeps what is sound and writes root's own copy; after that the panel's file
    // is never read again.
    #[test]
    fn the_old_state_is_taken_once() {
        let d = tmpdir("migrate");
        fs::create_dir_all(d.join("panel")).unwrap();
        let digest = format!("sha256:{}", "c".repeat(64));
        let entry = |image: &str| {
            format!(
                r#"{{"version":"1.0.0","image":"{image}","digest":"{digest}","listen":"127.0.0.1:45615","token":"{}","status":"running","at":"x"}}"#,
                "a".repeat(48)
            )
        };
        let state = format!(
            r#"{{"adapters":{{"yookassa":{},"elsewhere":{},"other-org":{}}}}}"#,
            entry("ghcr.io/getmikan/adapter-yookassa"),
            entry("docker.io/library/alpine"),
            entry("ghcr.io/someone/adapter")
        );
        fs::write(d.join("panel/state.json"), &state).unwrap();
        let panel = Dir::open(&d, "panel", false).unwrap().unwrap();
        let root_state = d.join("root/state.json");
        let s = load_from(&root_state, Some(&panel)).unwrap();
        assert_eq!(s.adapters.keys().collect::<Vec<_>>(), ["yookassa"]);
        assert!(root_state.exists(), "root's own copy is written at once");
        // the panel now plants an entry: root no longer looks there
        fs::write(d.join("panel/state.json"), state.replace("yookassa", "planted")).unwrap();
        let again = load_from(&root_state, Some(&panel)).unwrap();
        assert_eq!(again.adapters.keys().collect::<Vec<_>>(), ["yookassa"]);
        // no state anywhere is no adapters
        assert!(load_from(&d.join("none/state.json"), None).unwrap().adapters.is_empty());
        fs::remove_dir_all(&d).unwrap();
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
        let y = compose_yaml(&m, "");
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

    // Protocol 2 brings the other kinds, each with a mode, known permissions and sane memory;
    // a payment adapter stays protocol 1 and carries none of it. Same rules as the panel's
    // TestParseCatalogProtocol2.
    #[test]
    fn protocol_2_entries() {
        let signer = SigningKey::from_bytes(&[9; 32]);
        let d = format!("sha256:{}", "c".repeat(64));
        let e = |id: &str, protocol: u32, extra: &str| {
            format!(r#"{{"id":"{id}","version":"1.0.0","protocol":{protocol},"image":"ghcr.io/getmikan/{id}","digest":"{d}"{extra}}}"#)
        };
        let entries = [
            e("telegram", 2, r#","kind":"bot","mode":"http","permissions":["bot","kv"],"resources":{"mem_mb":128}"#),
            e("script", 2, r#","kind":"bot","mode":"client""#),
            e("page", 2, r#","kind":"ui""#),
            e("yookassa", 1, ""),
            e("no-kind", 2, ""),
            e("pay-v2", 2, r#","kind":"payment""#),
            e("odd-mode", 2, r#","kind":"bot","mode":"socket""#),
            e("root", 2, r#","kind":"bot","permissions":["admin"]"#),
            e("twice", 2, r#","kind":"bot","permissions":["kv","kv"]"#),
            e("greedy", 2, r#","kind":"bot","resources":{"mem_mb":4096}"#),
            e("tiny", 2, r#","kind":"bot","resources":{"mem_mb":8}"#),
            e("v1-bot", 1, r#","kind":"bot""#),
            e("v1-perms", 1, r#","permissions":["kv"]"#),
            e("v3", 3, r#","kind":"bot""#),
        ];
        let data = format!(r#"{{"version":1,"updated":"2026-10-11T00:00:00Z","adapters":[{}]}}"#, entries.join(",")).into_bytes();
        let sig = STANDARD.encode(signer.sign(&data).to_bytes());
        let got = parse_catalog(&data, &sig, &signer.verifying_key(), "dev").unwrap();
        assert_eq!(got.iter().map(|e| e.id.as_str()).collect::<Vec<_>>(), ["telegram", "script", "page", "yookassa"]);
        assert_eq!(got[2].mode, "http", "a protocol 2 entry without a mode serves HTTP");
        assert_eq!((got[3].kind.as_str(), got[3].mode.as_str()), ("payment", "http"));
    }

    fn bot() -> Installed {
        Installed {
            version: "1.0.0".into(),
            image: "ghcr.io/getmikan/telegram".into(),
            digest: format!("sha256:{}", "c".repeat(64)),
            listen: "127.0.0.1:45616".into(),
            token: "a".repeat(48),
            protocol: PROTOCOL_2,
            kind: "bot".into(),
            mode: "http".into(),
            panel_token: "b".repeat(48),
            mem_mb: 192,
            status: "running".into(),
            ..Default::default()
        }
    }

    // A state the panel could have touched cannot turn a payment adapter into something it
    // is not, nor smuggle YAML through the fields of protocol 2.
    #[test]
    fn protocol_2_soundness() {
        assert!(sound("telegram", &bot()));
        let bad = |f: &dyn Fn(&mut Installed)| {
            let mut a = bot();
            f(&mut a);
            a
        };
        let cases = [
            ("no panel token", bad(&|a| a.panel_token.clear())),
            ("panel token with YAML", bad(&|a| a.panel_token = format!("{}\"\n    privileged: true", "b".repeat(40)))),
            ("kind", bad(&|a| a.kind = "payment".into())),
            ("unknown kind", bad(&|a| a.kind = "bot\n    privileged: true".into())),
            ("mode", bad(&|a| a.mode = "socket".into())),
            ("memory", bad(&|a| a.mem_mb = 100_000)),
            ("protocol", bad(&|a| a.protocol = 3)),
            (
                "a payment adapter with a panel token",
                bad(&|a| {
                    a.protocol = 1;
                    a.kind.clear();
                    a.mode.clear();
                    a.mem_mb = 0;
                }),
            ),
            (
                "a payment adapter with memory",
                bad(&|a| {
                    a.protocol = 0;
                    a.kind.clear();
                    a.mode.clear();
                    a.panel_token.clear();
                    a.mem_mb = 256;
                }),
            ),
        ];
        for (what, a) in cases {
            assert!(!sound("telegram", &a), "{what} passed");
        }
    }

    // An addon of protocol 2 gets the panel's API and its own memory; without a sound address
    // for the API it is not started at all.
    #[test]
    fn compose_of_protocol_2() {
        let mut m = BTreeMap::new();
        m.insert("telegram".to_owned(), bot());
        let y = compose_yaml(&m, "127.0.0.1:41990");
        for want in [
            "  telegram:\n",
            "mem_limit: 192m",
            "MIKAN_PANEL_URL: \"http://127.0.0.1:41990/panel/v2\"",
            &format!("MIKAN_PANEL_TOKEN: \"{}\"", "b".repeat(48)),
            "network_mode: host",
            "read_only: true",
        ] {
            assert!(y.contains(want), "no {want} in\n{y}");
        }
        for api in ["", "0.0.0.0:41990", "127.0.0.1:80", "127.0.0.1:1\"\n    privileged: true"] {
            assert!(!compose_yaml(&m, api).contains("telegram"), "started with the panel's API at {api:?}");
        }
    }

    // The panel's API address comes from root's state only when it is a loopback address.
    #[test]
    fn panel_api_address() {
        let d = tmpdir("panel-api");
        let root_state = d.join("state.json");
        fs::write(&root_state, r#"{"adapters":{},"panel_api":"0.0.0.0:41990"}"#).unwrap();
        assert_eq!(load_from(&root_state, None).unwrap().panel_api, "");
        fs::write(&root_state, r#"{"adapters":{},"panel_api":"127.0.0.1:41990"}"#).unwrap();
        assert_eq!(load_from(&root_state, None).unwrap().panel_api, "127.0.0.1:41990");
        fs::remove_dir_all(&d).unwrap();
    }
}
