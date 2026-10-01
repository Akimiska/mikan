//! The install: what the admin chose (Plan) and the steps that carry it out. The TUI and
//! the plain mode drive the same steps; progress goes out as events.

use std::fs::{self, File};
use std::io::Read;
use std::path::Path;
use std::process::{Command, Stdio};
use std::sync::mpsc::Sender;
use std::thread;
use std::time::{Duration, Instant};

use anyhow::{Context, Result, anyhow, bail};

use crate::envfile::{EnvFile, write_private};
use crate::system::{self, Proto};
use crate::{DIR, docker, host, release};

/// The flags of `mikan install`; the installer asks for what they leave out.
#[derive(clap::Args, Clone, Debug, Default)]
pub struct Options {
    /// Default language of the panel: en or ru
    #[arg(long, value_parser = ["en", "ru"])]
    pub lang: Option<String>,
    /// Public IPv4 address of the server (detected by default)
    #[arg(long)]
    pub host: Option<String>,
    /// Domain that points to this server, for a Let's Encrypt certificate
    #[arg(long)]
    pub domain: Option<String>,
    /// Email for Let's Encrypt
    #[arg(long)]
    pub email: Option<String>,
    /// Panel port (random from 20000–60000 by default)
    #[arg(long)]
    pub port: Option<u16>,
    /// Leave ufw alone
    #[arg(long)]
    pub no_firewall: bool,
    /// Leave the kernel settings alone (BBR, UDP buffers)
    #[arg(long)]
    pub no_tune: bool,
    /// Install a node of another panel with the key from its Nodes page
    #[arg(long, value_name = "KEY")]
    pub join: Option<String>,
    /// An image instead of the latest release, for tests
    #[arg(long, value_name = "IMAGE", conflicts_with = "image_tar")]
    pub image: Option<String>,
    /// An image archive (docker save | gzip) instead of the latest release
    #[arg(long, value_name = "FILE")]
    pub image_tar: Option<String>,
    /// Ask nothing: take the flags and the defaults
    #[arg(long, short = 'y')]
    pub yes: bool,
    /// Replace a Docker without compose v2 (the distribution's docker.io) with Docker from
    /// get.docker.com; images, volumes and containers stay. The interactive installer asks.
    #[arg(long)]
    pub replace_docker: bool,
    /// The old installer's flag; --join is enough now
    #[arg(long, hide = true)]
    pub node: bool,
}

#[derive(Clone, Debug)]
pub struct Plan {
    pub lang: String,
    pub host: String,
    pub domain: String,
    pub email: String,
    pub port: u16,
    pub firewall: bool,
    pub tune: bool,
    pub join: Option<String>,
    pub image: Option<String>,
    pub image_tar: Option<String>,
    /// The admin agreed to replace a Docker without compose v2.
    pub replace_docker: bool,
}

impl Plan {
    /// The plan the flags make; host stays empty for the caller to detect or ask.
    pub fn from(o: &Options) -> Self {
        // Over SSH the admin's own locale usually comes along: a Russian one suggests Russian.
        let locale =
            ["LC_ALL", "LC_MESSAGES", "LANG"].iter().find_map(|v| std::env::var(v).ok().filter(|s| !s.is_empty())).unwrap_or_default();
        let lang = if locale.starts_with("ru") { "ru" } else { "en" };
        Self {
            lang: o.lang.clone().unwrap_or_else(|| lang.into()),
            host: o.host.clone().unwrap_or_default(),
            domain: o.domain.clone().unwrap_or_default().trim().to_lowercase(),
            email: o.email.clone().unwrap_or_default().trim().to_owned(),
            port: o.port.unwrap_or_else(free_port),
            firewall: !o.no_firewall,
            tune: !o.no_tune,
            join: o.join.clone().map(|k| k.trim().to_owned()),
            image: o.image.clone(),
            image_tar: o.image_tar.clone(),
            replace_docker: o.replace_docker,
        }
    }

    pub fn node(&self) -> bool {
        self.join.is_some()
    }
}

/// A free port for the panel, from 20000–60000.
pub fn free_port() -> u16 {
    loop {
        let mut b = [0u8; 2];
        urandom(&mut b);
        let p = 20000 + u16::from_le_bytes(b) % 40001;
        if system::port_owner(p, Proto::Tcp).is_none() {
            return p;
        }
    }
}

fn urandom(buf: &mut [u8]) {
    File::open("/dev/urandom").and_then(|mut f| f.read_exact(buf)).expect("read /dev/urandom");
}

/// A random string over alphabet, each character equally likely.
pub fn token(n: usize, alphabet: &[u8]) -> String {
    let limit = 256 - 256 % alphabet.len();
    let mut out = String::with_capacity(n);
    while out.len() < n {
        let mut b = [0u8; 64];
        urandom(&mut b);
        for x in b.iter().map(|&x| x as usize).filter(|&x| x < limit) {
            if out.len() < n {
                out.push(alphabet[x % alphabet.len()] as char);
            }
        }
    }
    out
}

const ALNUM: &[u8] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
const LOWER: &[u8] = b"abcdefghijklmnopqrstuvwxyz";
const LOWER_DIGITS: &[u8] = b"abcdefghijklmnopqrstuvwxyz0123456789";

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Step {
    Packages,
    Docker,
    Image,
    Files,
    Bootstrap,
    System,
    Start,
}

impl Step {
    pub fn all(node: bool) -> Vec<Step> {
        let mut s = vec![Step::Packages, Step::Docker, Step::Image, Step::Files];
        if !node {
            s.push(Step::Bootstrap);
        }
        s.extend([Step::System, Step::Start]);
        s
    }

    pub fn label(self) -> &'static str {
        match self {
            Step::Packages => "Package manager",
            Step::Docker => "Docker",
            Step::Image => "mikan image",
            Step::Files => "Files in /opt/mikan",
            Step::Bootstrap => "Admin and secret links",
            Step::System => "Network tuning and firewall",
            Step::Start => "Start",
        }
    }
}

pub enum Event {
    Start(Step),
    Progress(Step, f64),
    Note(Step, String),
    Log(String),
    Done(Step),
    Failed(Step, String),
    Finished(Outcome),
}

#[derive(Clone, Debug)]
pub struct Outcome {
    pub version: String,
    /// A node's API port; None for a panel.
    pub node_port: Option<u16>,
    pub url: String,
    pub login: String,
    pub password: String,
}

/// Runs the install and reports through tx; the last event is Finished or Failed.
pub fn execute(plan: Plan, tx: Sender<Event>) {
    if let Err((step, e)) = run(&plan, &tx) {
        let _ = tx.send(Event::Failed(step, format!("{e:#}")));
    }
}

type StepResult<T> = std::result::Result<T, (Step, anyhow::Error)>;

fn step<T>(tx: &Sender<Event>, s: Step, f: impl FnOnce() -> Result<T>) -> StepResult<T> {
    let _ = tx.send(Event::Start(s));
    let v = f().map_err(|e| (s, e))?;
    let _ = tx.send(Event::Done(s));
    Ok(v)
}

fn note(tx: &Sender<Event>, s: Step, text: impl Into<String>) {
    let _ = tx.send(Event::Note(s, text.into()));
}

fn run(plan: &Plan, tx: &Sender<Event>) -> StepResult<()> {
    let node = plan.node();
    step(tx, Step::Packages, || {
        let start = Instant::now();
        let mut shown = String::new();
        while let Some(who) = system::dpkg_holder() {
            if start.elapsed() > Duration::from_secs(600) {
                bail!("the package manager has been busy for 10 minutes, {who}: wait for it to finish and run the installer again");
            }
            if who != shown {
                note(tx, Step::Packages, format!("busy, {who}: waiting for it"));
                shown = who;
            }
            thread::sleep(Duration::from_secs(5));
        }
        if system::dpkg_unfinished() {
            note(tx, Step::Packages, "finishing the hoster's half-done package setup");
            let _ = Command::new("dpkg")
                .args(["--configure", "-a", "--force-confdef", "--force-confold"])
                .env("DEBIAN_FRONTEND", "noninteractive")
                .output();
        }
        note(tx, Step::Packages, "free");
        Ok(())
    })?;

    step(tx, Step::Docker, || {
        let log = |l: &str| {
            let _ = tx.send(Event::Log(l.to_owned()));
        };
        match docker::version() {
            Some(v) if docker::compose_ok() => {
                note(tx, Step::Docker, format!("Docker {v}"));
                return Ok(());
            }
            Some(v) => {
                // The distribution's Docker (Ubuntu 22.04's docker.io 24.0.7) has no compose v2.
                if !plan.replace_docker {
                    bail!("Docker {v} has no compose v2: agree to replace it in the installer, or add --replace-docker");
                }
                note(tx, Step::Docker, format!("Docker {v} has no compose v2: replacing it from get.docker.com"));
                docker::replace(log)?;
            }
            None => {
                note(tx, Step::Docker, "installing from get.docker.com");
                docker::install(log)?;
            }
        }
        note(tx, Step::Docker, format!("Docker {} installed", docker::version().unwrap_or_default()));
        Ok(())
    })?;

    let (image, version) = step(tx, Step::Image, || {
        let progress = |p| {
            let _ = tx.send(Event::Progress(Step::Image, p));
        };
        let image = if let Some(tar) = &plan.image_tar {
            note(tx, Step::Image, format!("loading {tar}"));
            docker::load(tar)?
        } else if let Some(image) = &plan.image {
            note(tx, Step::Image, format!("pulling {image}"));
            docker::pull(image, progress)?;
            image.clone()
        } else {
            let m = release::latest()?;
            note(tx, Step::Image, format!("mikan {}, signed release", m.version));
            docker::pull(&m.reference(), progress)?;
            m.reference()
        };
        let version = image_version(&image)?;
        note(tx, Step::Image, format!("mikan {version}"));
        Ok((image, version))
    })?;

    let api_port = step(tx, Step::Files, || {
        let api_port = match &plan.join {
            Some(key) => {
                let p = node_port(&image, key)?;
                if let Some(who) = system::port_owner(p, Proto::Tcp) {
                    bail!("port {p}, where the panel will reach the node, is taken ({who})");
                }
                Some(p)
            }
            None => None,
        };
        write_files(plan, &image, &version, api_port)?;
        note(tx, Step::Files, DIR);
        Ok(api_port)
    })?;

    let creds = if node {
        None
    } else {
        Some(step(tx, Step::Bootstrap, || {
            let c = Credentials::new();
            let port = plan.port.to_string();
            let mut args = vec!["bootstrap", "--public-host", plan.host.as_str(), "--port", port.as_str()];
            args.extend(["--admin-path", c.admin_path.as_str(), "--sub-path", c.sub_path.as_str(), "--username", c.login.as_str()]);
            args.extend(["--password-stdin", "--lang", plan.lang.as_str()]);
            if !plan.domain.is_empty() {
                args.extend(["--domain", plan.domain.as_str()]);
            }
            if !plan.email.is_empty() {
                args.extend(["--email", plan.email.as_str()]);
            }
            docker::check(docker::admin_once(&args, Some(&format!("{}\n", c.password)))?)?;
            note(tx, Step::Bootstrap, format!("login {}", c.login));
            Ok(c)
        })?)
    };

    step(tx, Step::System, || {
        let mut done = Vec::new();
        if plan.tune {
            done.push(match host::tune() {
                Ok(()) => "BBR on".to_string(),
                Err(e) => format!("no BBR ({e})"),
            });
        }
        if plan.firewall {
            let panel_port = (!node).then_some(plan.port);
            done.push(if host::open(&host::rules(panel_port, api_port))? { "ports open in ufw" } else { "ufw is off" }.into());
        }
        host::install_self()?;
        done.push(match host::install_units(!node) {
            Ok(()) => "daily update check".into(),
            Err(e) => format!("no update timer ({e})"),
        });
        note(tx, Step::System, done.join(" · "));
        Ok(())
    })?;

    step(tx, Step::Start, || {
        docker::compose_run(&["up", "-d"])?;
        wait_ready(api_port, Duration::from_secs(90))?;
        note(tx, Step::Start, "running");
        Ok(())
    })?;

    let outcome = match creds {
        Some(c) => {
            let host = if plan.domain.is_empty() { &plan.host } else { &plan.domain };
            Outcome {
                version,
                node_port: None,
                url: format!("https://{host}:{}/{}/", plan.port, c.admin_path),
                login: c.login,
                password: c.password,
            }
        }
        None => Outcome { version, node_port: api_port, url: String::new(), login: String::new(), password: String::new() },
    };
    let _ = tx.send(Event::Finished(outcome));
    Ok(())
}

struct Credentials {
    admin_path: String,
    sub_path: String,
    login: String,
    password: String,
}

impl Credentials {
    fn new() -> Self {
        Self {
            admin_path: token(24, ALNUM),
            sub_path: token(12, ALNUM),
            login: token(1, LOWER) + &token(11, LOWER_DIGITS),
            password: token(32, ALNUM),
        }
    }
}

/// The version the image reports.
pub fn image_version(image: &str) -> Result<String> {
    let out = Command::new("docker").args(["run", "--rm", image, "version"]).stdin(Stdio::null()).output()?;
    let v = String::from_utf8_lossy(&out.stdout).trim().to_owned();
    if !out.status.success() || v.is_empty() {
        bail!("the image does not start: {}", String::from_utf8_lossy(&out.stderr).trim());
    }
    Ok(v)
}

/// The image checks a node's join key and names the port the panel will connect to. The
/// key goes through the environment, not the command line.
pub fn node_port(image: &str, key: &str) -> Result<u16> {
    let out = Command::new("docker")
        .env("MIKAN_NODE_JOIN", key)
        .args(["run", "--rm", "-e", "MIKAN_NODE_JOIN", "--entrypoint", "/usr/local/bin/mikan-node", image, "key-port"])
        .stdin(Stdio::null())
        .output()?;
    let port = String::from_utf8_lossy(&out.stdout).trim().parse().ok();
    match port {
        Some(p) if out.status.success() => Ok(p),
        _ => bail!("the join key does not fit: copy it from the panel's Nodes page again"),
    }
}

fn write_files(plan: &Plan, image: &str, version: &str, api_port: Option<u16>) -> Result<()> {
    let dir = Path::new(DIR);
    if dir.join(".env").exists() {
        bail!("mikan is already installed in {DIR}");
    }
    for sub in ["data/node", "backups"] {
        fs::create_dir_all(dir.join(sub))?;
    }
    let mut env = EnvFile::new(dir.join(".env"));
    env.set("MIKAN_IMAGE", image)?;
    env.set("MIKAN_VERSION", version)?;
    env.set("MIKAN_UFW", if plan.firewall { "1" } else { "0" })?;
    let compose = match (&plan.join, api_port) {
        (Some(key), Some(port)) => {
            env.set("MIKAN_MODE", "node")?;
            env.set("NODE_API_PORT", &port.to_string())?;
            env.set("MIKAN_NODE_JOIN", key)?;
            docker::NODE_COMPOSE
        }
        _ => {
            fs::create_dir_all(dir.join("data/panel"))?;
            env.set("PANEL_PORT", &plan.port.to_string())?;
            docker::PANEL_COMPOSE
        }
    };
    write_private(&dir.join("compose.yaml"), compose.as_bytes())?;
    env.save()?;
    own_data()
}

/// The containers run as 65532; a bind mount does not take the image's ownership.
pub fn own_data() -> Result<()> {
    let data = Path::new(DIR).join("data");
    let ok = Command::new("chown").args(["-R", "65532:65532"]).arg(&data).status().is_ok_and(|s| s.success());
    if !ok {
        bail!("chown {} failed", data.display());
    }
    use std::os::unix::fs::PermissionsExt;
    fs::set_permissions(&data, fs::Permissions::from_mode(0o700))?;
    Ok(())
}

/// Waits for the panel to answer, or for a node's API port to listen.
pub fn wait_ready(node_port: Option<u16>, limit: Duration) -> Result<()> {
    let start = Instant::now();
    loop {
        let ready = match node_port {
            Some(p) => system::port_owner(p, Proto::Tcp).is_some(),
            None => docker::panel_healthy(),
        };
        if ready {
            return Ok(());
        }
        if start.elapsed() > limit {
            let logs = docker::compose(&["logs", "--tail", "30"])
                .output()
                .map(|o| String::from_utf8_lossy(&o.stdout).into_owned())
                .unwrap_or_default();
            return Err(anyhow!("mikan did not start in {} s. Its last logs:\n{}", limit.as_secs(), logs.trim_end()));
        }
        thread::sleep(Duration::from_secs(1));
    }
}

/// What a finished install tells the admin, for the terminal.
pub fn summary(o: &Outcome) -> String {
    match o.node_port {
        Some(p) => format!(
            "mikan {} node is running and waits for its panel on port {p}.\nThe panel connects within 30 seconds: see its Nodes page.\nCommands on this server: mikan (menu), mikan status, mikan update",
            o.version
        ),
        None => format!(
            "mikan {} is running.\n\n  Panel     {}\n  Login     {}\n  Password  {}   ← shown once, keep it in a password manager\n\nCommands on this server: mikan (menu), mikan status, mikan update",
            o.version, o.url, o.login, o.password
        ),
    }
}

/// `mikan install`: the TUI in a terminal, the plain mode with --yes or without one. On
/// an installed server it opens the menu, or with --yes updates: the one-line install
/// is also how a server of 0.3.8 and before moves to this installer.
pub fn install(opts: Options) -> Result<()> {
    if Path::new(DIR).join(".env").exists() {
        if crate::tui::interactive() && !opts.yes {
            return crate::tui::menu();
        }
        println!("mikan is already installed in {DIR}: updating it.");
        let args = crate::ops::UpdateArgs { target: opts.image_tar.or(opts.image), ..Default::default() };
        return crate::ops::update(&args, &mut |l| println!("{l}"), &mut |_| {});
    }
    if crate::tui::interactive() && !opts.yes {
        return crate::tui::wizard(opts);
    }
    plain(opts)
}

/// The install without the TUI: every step as a line.
fn plain(opts: Options) -> Result<()> {
    let mut plan = Plan::from(&opts);
    let checks = system::checks(plan.node());
    for c in &checks {
        println!("{} {:<16} {}", mark(c.level), c.label, c.detail);
    }
    if checks.iter().any(|c| c.level == system::Level::Error) {
        bail!("fix the problems above and run the installer again");
    }
    if system::docker_to_replace(&checks).is_some() && !plan.replace_docker {
        bail!("this Docker has no compose v2: add --replace-docker to replace it from get.docker.com (images and containers stay)");
    }
    if !plan.node() {
        if plan.host.is_empty() {
            plan.host = crate::net::public_ipv4().context("cannot tell the server's IP: pass --host")?.to_string();
        }
        println!("Server address: {}", plan.host);
        if !plan.domain.is_empty() {
            if !crate::net::valid_domain(&plan.domain) {
                bail!("--domain {:?} is not a domain name", plan.domain);
            }
            let ip = plan.host.parse().context("--domain needs --host to be the server's IPv4 address")?;
            let check = crate::net::check_domain(&plan.domain, ip);
            for n in &check.notes {
                println!("{} {}", mark(n.level), n.text);
            }
            if check.level() == system::Level::Error {
                bail!("the domain does not lead to this server; fix its DNS or install without --domain");
            }
        }
        if !plan.email.is_empty() && !crate::net::valid_email(&plan.email) {
            bail!("--email {:?} is not an email address", plan.email);
        }
    }
    let (tx, rx) = std::sync::mpsc::channel();
    let p = plan.clone();
    thread::spawn(move || execute(p, tx));
    let mut last = -1i64;
    for ev in rx {
        match ev {
            Event::Start(s) => println!("▸ {}", s.label()),
            Event::Note(_, t) => println!("  {t}"),
            Event::Progress(_, p) => {
                let pct = (p * 100.0) as i64;
                if pct / 25 != last / 25 {
                    println!("  {pct}%");
                    last = pct;
                }
            }
            Event::Log(_) | Event::Done(_) => {}
            Event::Failed(s, e) => bail!("{}: {e}", s.label()),
            Event::Finished(o) => {
                if o.node_port.is_none() {
                    crate::sites::auto(|l| println!("{l}"));
                }
                println!("\n{}", summary(&o));
                return Ok(());
            }
        }
    }
    bail!("the install stopped")
}

fn mark(l: system::Level) -> &'static str {
    match l {
        system::Level::Ok => "✓",
        system::Level::Warn => "!",
        system::Level::Error => "✗",
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn tokens() {
        let t = token(32, ALNUM);
        assert_eq!(t.len(), 32);
        assert!(t.bytes().all(|b| b.is_ascii_alphanumeric()));
        assert_ne!(t, token(32, ALNUM));
        let c = Credentials::new();
        assert!(c.login.as_bytes()[0].is_ascii_lowercase() && c.login.len() == 12);
        assert!(c.admin_path.len() == 24 && c.sub_path.len() == 12);
        let p = free_port();
        assert!((20000..=60000).contains(&p));
    }

    #[test]
    fn steps() {
        assert_eq!(Step::all(false).len(), 7);
        assert!(!Step::all(true).contains(&Step::Bootstrap));
    }
}
