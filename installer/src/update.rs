//! `mikan update`: to the latest release, or to an image the admin names, with a backup
//! first. A panel already on PostgreSQL goes back to its previous image when the new one
//! does not start on an unchanged schema; otherwise it stays stopped with its data
//! preserved. Nodes always go back. The daily timer, the panel's
//! Update button and the admin's shell all come through here, one at a time (lock.rs).
//!
//! The panel and this command talk through data/panel/update, which the panel owns, so
//! everything read from there is small, plain-file data and everything written goes through
//! panelfs (no link followed, no directory trusted):
//!
//!   update/policy.json  the panel: {"auto": true} when automatic updates are on
//!   update/request      the panel: its Update button (a path unit starts `update --requested`)
//!   update/status.json  this: {"state": "running" | "ok" | "failed", "version", "from", "error", "at"}

use std::fs;
use std::path::Path;
use std::process::Command;
use std::time::Duration;

use anyhow::{Context, Result, bail};
use serde::Serialize;
use sha2::{Digest, Sha256};

use crate::envfile::EnvFile;
use crate::lock::{self, Wait};
use crate::ops::Install;
use crate::panelfs::{self, Dir};
use crate::{DIR, addon, backup, clock, docker, host, net, release, setup, signals};

/// What `mikan update` was asked for.
#[derive(clap::Args, Clone, Debug, Default)]
pub struct UpdateArgs {
    /// An image (ghcr.io/…@sha256:…) or an image archive (.tar.gz) instead of the latest release
    pub target: Option<String>,
    /// Only when automatic updates are on (the daily timer runs this)
    #[arg(long)]
    pub auto: bool,
    /// The panel's Update button asked for it (a systemd path unit runs this)
    #[arg(long, hide = true)]
    pub requested: bool,
    /// Say what is out, change nothing
    #[arg(long)]
    pub check: bool,
}

const UPDATE_REL: &str = "data/panel/update";

/// Where the panel and the host updater talk; the panel sees it as /data/panel/update.
fn update_dir(create: bool) -> Result<Option<Dir>> {
    Dir::open(Path::new(DIR), UPDATE_REL, create)
}

/// How the update stands, as the panel shows it.
#[derive(Clone, Copy, Serialize, Debug, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
enum Phase {
    Running,
    Ok,
    Failed,
}

/// Tells the panel how the update went (a node has no panel on the host to tell).
fn report(install: &Install, phase: Phase, version: &str, from: &str, error: &str) {
    if install.node {
        return;
    }
    let body = serde_json::json!({"state": phase, "version": version, "from": from, "error": error, "at": clock::rfc3339()});
    let sent = update_dir(true)
        .and_then(|d| d.context("data/panel/update is not there")?.write("status.json", body.to_string().as_bytes(), 0o644));
    if let Err(e) = sent {
        eprintln!("mikan: cannot tell the panel how the update went: {e:#}");
    }
}

/// Whether the daily check may update: a node asks its .env, a panel its policy file.
fn auto_on(install: &Install) -> bool {
    if install.node {
        return install.env.get("MIKAN_AUTO_UPDATE") == Some("1");
    }
    policy_on()
}

/// Whether the panel's settings turned automatic updates on, for the menu.
pub fn policy_on() -> bool {
    update_dir(false).ok().flatten().and_then(|d| d.read("policy.json", 4096).ok().flatten()).is_some_and(|b| policy_says_on(&b))
}

fn policy_says_on(data: &[u8]) -> bool {
    serde_json::from_slice::<serde_json::Value>(data).is_ok_and(|v| v["auto"] == true)
}

/// What the panel put at update/request.
#[derive(Debug, PartialEq, Eq)]
enum Asked {
    Nothing,
    Update,
    /// The adapters ({"do": "addons"}), not an update.
    Addons,
    /// Not a request: a link, a directory, a FIFO, a file of a size the panel never writes.
    Garbage(String),
}

/// Takes the request: whatever it is, it goes first, so the unit that watches for it does
/// not fire again and again.
fn take_request(dir: &Dir) -> Result<Asked> {
    if !dir.exists("request") {
        return Ok(Asked::Nothing);
    }
    let read = dir.read("request", 4096);
    dir.discard("request")?;
    Ok(match read {
        Ok(Some(data)) => match serde_json::from_slice::<serde_json::Value>(&data) {
            Ok(v) if v["do"] == "addons" => Asked::Addons,
            _ => Asked::Update,
        },
        Ok(None) => Asked::Nothing,
        Err(e) => Asked::Garbage(format!("{e:#}")),
    })
}

/// update/ itself may be what the panel replaced (a link, a file): the request inside it
/// would stay for ever and the path unit would start this command again and again. The
/// stand-in goes; the panel makes its directory again.
fn clear_update_dir(say: &mut dyn FnMut(&str)) {
    let Ok(Some(panel)) = Dir::open(Path::new(DIR), "data/panel", false) else { return };
    if panel.exists("update") {
        match panel.discard("update") {
            Ok(()) => say("data/panel/update was not a directory: removed it"),
            Err(e) => say(&format!("cannot remove data/panel/update: {e:#}")),
        }
    }
}

/// What an update has told the panel so far.
#[derive(Default)]
struct Attempt {
    version: String,
    from: String,
    started: bool,
}

/// Updates to the latest release (or target), upgrading the installer first when needed.
/// A panel goes back only from PostgreSQL to PostgreSQL on an unchanged schema; a node always can.
pub fn update(a: &UpdateArgs, say: &mut dyn FnMut(&str), progress: &mut dyn FnMut(f64)) -> Result<()> {
    // `--check` changes nothing, so it waits for no one.
    let _lock = if a.check {
        None
    } else {
        // The daily check comes again tomorrow; the button and the admin wait their turn.
        let wait = if a.auto && !a.requested { Wait::Skip } else { Wait::Block };
        match lock::acquire(wait, say)? {
            Some(g) => Some(g),
            None => {
                say("Another mikan operation is running: this automatic check skips its turn.");
                return Ok(());
            }
        }
    };
    let install = Install::load()?;
    if a.requested {
        let dir = match update_dir(false) {
            Ok(d) => d,
            Err(e) => {
                say(&format!("{e:#}"));
                clear_update_dir(say);
                None
            }
        };
        let Some(dir) = dir else { return Ok(()) };
        match take_request(&dir)? {
            Asked::Nothing => return Ok(()),
            // The panel wakes this unit for its payment adapters too.
            Asked::Addons => return panel_addons(say),
            Asked::Garbage(why) => {
                say(&format!("update/request is not a request, removed: {why}"));
                return Ok(());
            }
            Asked::Update => {}
        }
    }
    let mut at = Attempt::default();
    let r = update_to(a, say, progress, &mut at);
    // An adapter request that came while an update was asked for waits for it.
    if a.requested
        && addon::pending()
        && let Err(e) = addon::apply(&install.version(), say)
    {
        say(&format!("Payment adapters: {e:#}"));
    }
    // The admin pressed Update and waits for an answer, and an update that began must end
    // in one: ok, failed, never "running" for good.
    if a.requested || at.started {
        let now = Install::load().unwrap_or(install);
        match &r {
            Ok(()) => report(&now, Phase::Ok, &at.version, &at.from, ""),
            Err(e) => report(&now, Phase::Failed, &at.version, &at.from, &format!("{e:#}")),
        }
    }
    r
}

fn panel_addons(say: &mut dyn FnMut(&str)) -> Result<()> {
    let install = Install::load()?;
    install.panel_only()?;
    addon::apply(&install.version(), say)
}

fn update_to(a: &UpdateArgs, say: &mut dyn FnMut(&str), progress: &mut dyn FnMut(f64), at: &mut Attempt) -> Result<()> {
    let mut install = Install::load()?;
    // Do not converge a legacy compose file before its SQLite backup and writer stop.
    if a.auto && !a.requested && !auto_on(&install) {
        return Ok(());
    }
    let current = install.version();
    at.from.clone_from(&current);
    at.version.clone_from(&current);
    let mut manifest = None;
    let (image, version) = match a.target.as_deref() {
        Some(t) if Path::new(t).is_file() => {
            say(&format!("Loading {t}"));
            let image = docker::load(t)?;
            let v = setup::image_version(&image)?;
            (image, v)
        }
        Some(t) => {
            say(&format!("Pulling {t}"));
            docker::pull(t, &mut *progress)?;
            (t.to_owned(), setup::image_version(t)?)
        }
        None => {
            let m = release::latest()?;
            if already_current(&m.version, &current, a.check || install.node || docker::panel_healthy()) {
                say(&format!("mikan {current} is the latest release."));
                if !a.check && self_update(&m, say) {
                    follow_new_command(say);
                }
                return Ok(());
            }
            if a.check {
                say(&format!("mikan {} is out, this server runs {current}.", m.version));
                if let Some(notes) = m.notes.get("en") {
                    say(notes);
                }
                return Ok(());
            }
            say(&format!("Updating mikan {current} → {}", m.version));
            at.version.clone_from(&m.version);
            at.started = true;
            report(&install, Phase::Running, &m.version, &current, "");
            let reference = m.reference();
            docker::pull(&reference, &mut *progress)?;
            // The release names its version: an image that says another is not the one
            // that was signed for.
            let v = setup::image_version(&reference)?;
            if v.trim_start_matches('v') != m.version {
                bail!("the image is mikan {v}, the signed release says {}", m.version);
            }
            let version = m.version.clone();
            manifest = Some(m);
            (reference, version)
        }
    };
    if a.check {
        say(&format!("{image} is mikan {version}; this server runs {current}."));
        return Ok(());
    }
    at.version.clone_from(&version);
    if !at.started {
        at.started = true;
        report(&install, Phase::Running, &version, &current, "");
    }

    if let Some(m) = &manifest {
        let upgrade = self_update(m, say);
        if upgrade {
            // The new installer must prepare infrastructure before the image starts.
            // The signed image was already verified above; hand off under our lock.
            let status = Command::new(host::BIN).args(["update", &image]).env(lock::HELD_ENV, "1").status()?;
            if !status.success() {
                bail!("the upgraded installer could not finish the update; retry mikan update");
            }
            return Ok(());
        }
        if !m.min_installer.is_empty() && release::newer(&m.min_installer, crate::version()) {
            bail!(
                "this release requires installer {}; its verified upgrade failed. Run the signed one-line installer again before updating",
                m.min_installer
            );
        }
    }

    // A backup that cannot be made stops the update before changing its image or data.
    let saved = backup::backup_as("pre-update", say).context("the backup before the update failed; nothing was changed")?;
    say(&format!("Backup: {}", saved.display()));

    // From here until the new version is up or the old one is back, nothing may cut it short.
    let _critical = signals::critical();
    let root = Path::new(DIR);
    let old_image = install.env.get("MIKAN_IMAGE").unwrap_or_default().to_owned();
    let old_version = install.env.get("MIKAN_VERSION").map(str::to_owned);
    let old_compose = fs::read_to_string(root.join("compose.yaml")).context("read the current compose file")?;
    let old_env = install.env.render();
    let was_postgres = install.env.get("MIKAN_DATABASE_URL").is_some();
    if !install.node {
        // The final source snapshot must include every payment and bot update accepted
        // until the old process has shut down, including SQLite's WAL.
        docker::compose_run(&["stop", "panel"])?;
        if !was_postgres {
            match backup::snapshot_stopped(root, "pre-update") {
                Ok(final_copy) => say(&format!("Final SQLite snapshot: {}", final_copy.display())),
                Err(e) => {
                    docker::compose_run(&["up", "-d", "panel"])?;
                    return Err(e.context("the stopped SQLite source could not be saved; update aborted"));
                }
            }
        }
    }

    // Until the database migrates, the configuration from before can come back as it was.
    let prepared = (|| -> Result<()> {
        if !install.node {
            setup::postgres_env(&mut install.env)?;
        }
        install.env.set("MIKAN_IMAGE", &image)?;
        install.env.set("MIKAN_VERSION", &version)?;
        install.env.save()?;
        docker::ensure_compose(root, install.node)?;
        if !install.node {
            docker::postgres_ready()?;
        }
        Ok(())
    })();
    if let Err(e) = prepared {
        if install.node {
            return Err(e);
        }
        return Err(match restore_previous(root, &install.env, &old_env, &old_compose, was_postgres) {
            Ok(()) => e.context(format!("the update could not prepare the database; mikan {current} runs again")),
            Err(back) => e.context(format!(
                "the update could not prepare the database, and mikan {current} did not start again ({back:#}); backup: {}",
                saved.display()
            )),
        });
    }
    let mut schema = None;
    if !install.node {
        // A failed command may have committed the import before losing its response.
        // Never fall back to stale SQLite or restore a snapshot implicitly after this.
        match docker::database(&["migrate"]) {
            Ok(out) => schema = schema_change(&String::from_utf8_lossy(&out.stdout)),
            Err(e) => {
                return Err(e.context(format!(
                    "database migration did not finish; panel stays stopped, original SQLite and PostgreSQL preserved. Run mikan update again; backup: {}",
                    saved.display()
                )));
            }
        }
    }
    if let Err(e) = start(&install, root) {
        // The previous image runs on what this update left only when that was PostgreSQL
        // already and the schema did not move: going back from SQLite would lose every
        // write since the import, and an older binary refuses a newer schema.
        if !install.node && !(was_postgres && schema.is_some_and(|(from, to)| from == to)) {
            let _ = docker::compose_run(&["stop", "panel"]);
            return Err(e.context(format!("PostgreSQL is preserved and the panel is stopped. No automatic database rollback is safe; retry mikan update after fixing startup. Backup: {}", saved.display())));
        }
        say(&format!("mikan {version} did not start: going back to {current}"));
        let before = Before { image: &old_image, version: old_version.as_deref(), saved: &saved, new: &version, current: &current };
        return Err(roll_back(&mut install, &before, say, &format!("{e:#}")));
    }
    seal_when_current(root, install.node, say);
    say(&format!("mikan {version} is running."));
    if let Some(m) = manifest
        && self_update(&m, say)
    {
        follow_new_command(say);
    } else if let Err(e) = host::install_units(!install.node) {
        say(&format!("No automatic updates: {e:#}"));
    }
    Ok(())
}

/// Retry an unhealthy release with the same version, but never turn a stale signed
/// manifest into an automatic downgrade just because the current panel is unhealthy.
fn already_current(latest: &str, current: &str, healthy_or_check: bool) -> bool {
    release::newer(current, latest) || (!release::newer(latest, current) && healthy_or_check)
}

/// Starts the containers on the current .env and waits for them.
fn start(install: &Install, root: &Path) -> Result<()> {
    panelfs::own_dirs(root, !install.node)?;
    docker::compose_run(&["up", "-d"])?;
    setup::wait_ready(install.node_port(), Duration::from_secs(90))
}

/// The schema versions `mikan database migrate` reports: before and after it ran.
fn schema_change(out: &str) -> Option<(u64, u64)> {
    let line = out.lines().find_map(|l| l.trim().strip_prefix("PostgreSQL schema version: "))?;
    let (from, to) = line.split_once(" -> ")?;
    Some((from.trim().parse().ok()?, to.trim().parse().ok()?))
}

/// Puts the configuration from before the update back and starts it. A PostgreSQL password
/// made meanwhile stays: its volume may already be initialized with it, and another one on
/// the next attempt would lock the panel out of its own database.
fn restore_previous(root: &Path, now: &EnvFile, old_env: &str, old_compose: &str, was_postgres: bool) -> Result<()> {
    crate::envfile::write_private(&root.join(".env"), old_env.as_bytes())?;
    if let Some(password) = now.get("MIKAN_POSTGRES_PASSWORD") {
        let mut env = EnvFile::load(root.join(".env"))?;
        env.set("MIKAN_POSTGRES_PASSWORD", password)?;
        env.save()?;
    }
    if !was_postgres {
        // The compose file from before has no database service to stop it by later.
        let _ = docker::compose_run(&["stop", "postgres"]);
    }
    docker::put_compose(root, old_compose)?;
    docker::compose_run(&["up", "-d"])?;
    Ok(())
}

/// The previous image and version come back: a node's always, a panel's when the schema
/// it left is the one the previous version ran on.
fn roll_back(install: &mut Install, before: &Before, _say: &mut dyn FnMut(&str), why: &str) -> anyhow::Error {
    let root = Path::new(DIR);
    let (new, current, saved) = (before.new, before.current, before.saved.display());
    let back = (|| -> Result<()> {
        install.env.set("MIKAN_IMAGE", before.image)?;
        install.env.set("MIKAN_VERSION", before.version.unwrap_or(""))?;
        install.env.save()?;
        start(install, root)
    })();
    match back {
        Ok(()) => anyhow::anyhow!("mikan {new} did not start, {current} runs again (backup: {saved}): {why}"),
        Err(e) => anyhow::anyhow!(
            "mikan {new} did not start, and {current} does not start either ({e:#}). The data from before the update are in {saved}: mikan restore {saved}. {why}"
        ),
    }
}

/// What an update goes back to.
struct Before<'a> {
    image: &'a str,
    version: Option<&'a str>,
    saved: &'a Path,
    new: &'a str,
    current: &'a str,
}

/// data/ becomes root's once no container can mount all of it: with the current
/// compose.yaml. An older one (the migration failed) would be locked out of its data.
fn seal_when_current(root: &Path, node: bool, say: &mut dyn FnMut(&str)) {
    if fs::read_to_string(root.join("compose.yaml")).is_ok_and(|c| c == docker::compose_text(node))
        && let Err(e) = panelfs::seal(root)
    {
        say(&format!("data/ stays as it was: {e:#}"));
    }
}

/// Brings the server's own files to what this installer writes: the units, and
/// compose.yaml, whose change recreates the containers (they mount their own data only
/// and have limits since 0.4.4). A server stopped by its admin stays stopped.
pub fn converge(say: &mut dyn FnMut(&str)) -> Result<()> {
    let _lock = lock::acquire(Wait::Block, say)?;
    let install = Install::load()?;
    let root = Path::new(DIR);
    if !install.node && install.env.get("MIKAN_DATABASE_URL").is_none() {
        // post-update must never activate the PostgreSQL compose over a live SQLite
        // installation. `update` is the only operation that performs the cutover.
        say("This SQLite installation needs mikan update before its compose file can change.");
        return Ok(());
    }
    // Where systemd is: a server without it has no units to bring up to date.
    if Path::new("/run/systemd/system").exists() && !host::units_current() {
        match host::install_units(!install.node) {
            Ok(()) => say("The update units are the current ones."),
            Err(e) => say(&format!("No automatic updates: {e:#}")),
        }
    }
    let running = docker::services().is_ok_and(|s| s.iter().any(|s| s.state == "running"));
    let Some(old) = docker::ensure_compose(root, install.node)? else {
        if running {
            // compose.yaml is already the current one: data/ may be sealed.
            let _ = panelfs::seal(root);
        }
        return Ok(());
    };
    say("compose.yaml is the current one: each container mounts only its own data (the previous file is compose.yaml.old)");
    if !running {
        return Ok(());
    }
    let _critical = signals::critical();
    let started = start(&install, root);
    if let Err(e) = started {
        say(&format!("The containers do not start with the new compose.yaml: {e:#}; putting the previous one back"));
        docker::put_compose(root, &old)?;
        start(&install, root).context("the previous compose.yaml does not start either")?;
        bail!("the new compose.yaml did not start: {e:#}");
    }
    panelfs::seal(root)
}

/// After the command was replaced by a newer one, the newer one finishes the work with its
/// own idea of the units and compose.yaml: it runs under this command's lock.
fn follow_new_command(say: &mut dyn FnMut(&str)) {
    let done = Command::new(host::BIN).arg("post-update").env(lock::HELD_ENV, "1").status();
    if !done.is_ok_and(|s| s.success()) {
        say("The new mikan command could not finish setting up: run `mikan update` once more.");
    }
}

/// Replaces this command with the release's installer when it differs; true when it did.
fn self_update(m: &release::Manifest, say: &mut dyn FnMut(&str)) -> bool {
    let Some(asset) = m.installer() else { return false };
    // Never back to an older command: a release older than this one (a rollback of the
    // release, an old signed manifest served again, a build from a branch) is not an update.
    if release::newer(crate::version(), &m.version) {
        return false;
    }
    let sha = |b: &[u8]| Sha256::digest(b).iter().map(|x| format!("{x:02x}")).collect::<String>();
    if fs::read(host::BIN).map(|b| sha(&b)).ok().as_deref() == Some(asset.sha256.as_str()) {
        return false;
    }
    let result = net::get(&asset.url, 64 << 20).and_then(|data| {
        if sha(&data) != asset.sha256 {
            bail!("the downloaded installer does not match the release manifest");
        }
        host::replace_bin(&data, Some(&m.version))
    });
    match result {
        Ok(()) => {
            say(&format!("The mikan command is {} now too.", m.version));
            true
        }
        Err(e) => {
            say(&format!("The mikan command stays as it is: {e:#}"));
            false
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::symlink;

    #[test]
    fn schema_change_is_read_from_the_migrate_report() {
        let out = "PostgreSQL schema version: 1 -> 1\nSQLite import verified: 31 tables; original SQLite preserved\n";
        assert_eq!(schema_change(out), Some((1, 1)));
        assert_eq!(schema_change("PostgreSQL schema version: 1 -> 2\n"), Some((1, 2)));
        assert_eq!(schema_change("PostgreSQL schema is ready; no legacy SQLite database\n"), None);
        assert_eq!(schema_change("PostgreSQL schema version: one -> 2\n"), None);
    }

    #[test]
    fn failed_current_release_is_retryable_but_old_manifests_never_downgrade() {
        assert!(!already_current("0.5.0.1", "0.5.0.1", false));
        assert!(already_current("0.5.0.1", "0.5.0.1", true));
        assert!(already_current("0.5.0.0", "0.5.0.1", false));
        assert!(already_current("0.5.0.0", "0.5.0.1", true));
        assert!(!already_current("0.5.0.2", "0.5.0.1", true));
    }

    fn tmpdir(name: &str) -> std::path::PathBuf {
        let d = std::env::temp_dir().join(format!("update-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&d);
        fs::create_dir_all(&d).unwrap();
        d
    }

    fn open(d: &Path) -> Dir {
        Dir::open(d, ".", false).unwrap().unwrap()
    }

    // The panel's Update button writes {"at": …}; its adapters write {"do": "addons"}.
    #[test]
    fn the_panels_requests() {
        let d = tmpdir("requests");
        let dir = open(&d);
        assert_eq!(take_request(&dir).unwrap(), Asked::Nothing);
        for (body, want) in
            [(r#"{"at":"2026-10-01T00:00:00Z"}"#, Asked::Update), (r#"{"do":"addons"}"#, Asked::Addons), ("not json", Asked::Update)]
        {
            fs::write(d.join("request"), body).unwrap();
            assert_eq!(take_request(&dir).unwrap(), want, "{body}");
            assert!(!d.join("request").exists(), "the request must go at once");
        }
        fs::remove_dir_all(&d).unwrap();
    }

    // What the panel may plant instead of a request: it is removed and logged, never
    // followed, never waited for, and the next look finds nothing (so the path unit that
    // watches the file has nothing to fire on).
    #[test]
    fn a_planted_request_is_removed_not_obeyed() {
        let d = tmpdir("planted");
        let dir = open(&d);
        let secret = d.join("secret");
        fs::write(&secret, "root's").unwrap();
        type Plant<'a> = Box<dyn Fn(&Path) + 'a>;
        let plant: [(&str, Plant<'_>); 5] = [
            ("a directory", Box::new(|p| fs::create_dir_all(p.join("inner")).unwrap())),
            ("a link", Box::new(|p| symlink(d.join("secret"), p).unwrap())),
            ("a link to /dev/zero", Box::new(|p| symlink("/dev/zero", p).unwrap())),
            (
                "a FIFO",
                Box::new(|p| {
                    rustix::fs::mknodat(rustix::fs::CWD, p, rustix::fs::FileType::Fifo, rustix::fs::Mode::from_raw_mode(0o600), 0).unwrap()
                }),
            ),
            ("a big file", Box::new(|p| fs::write(p, vec![b'x'; 1 << 20]).unwrap())),
        ];
        for (what, make) in plant {
            let p = d.join("request");
            make(&p);
            let got = take_request(&dir).unwrap();
            assert!(matches!(got, Asked::Garbage(_)), "{what}: {got:?}");
            assert!(fs::symlink_metadata(&p).is_err(), "{what} stayed");
            assert_eq!(take_request(&dir).unwrap(), Asked::Nothing, "{what}: it is looked at again");
        }
        assert_eq!(fs::read_to_string(&secret).unwrap(), "root's");
        fs::remove_dir_all(&d).unwrap();
    }

    // Reports are plain files for the panel, written through the directory handle: a link
    // at status.json is replaced, and the contract's field names are what the panel reads.
    #[test]
    fn the_report_is_written_for_the_panel_and_replaces_a_link() {
        let d = tmpdir("report");
        let target = d.join("etc-passwd");
        fs::write(&target, "root:x:0:0").unwrap();
        symlink(&target, d.join("status.json")).unwrap();
        let dir = open(&d);
        let body = serde_json::json!({"state": Phase::Failed, "version": "0.4.4", "from": "0.4.3", "error": "x", "at": clock::rfc3339()});
        dir.write("status.json", body.to_string().as_bytes(), 0o644).unwrap();
        assert_eq!(fs::read_to_string(&target).unwrap(), "root:x:0:0", "the link's target stays");
        let v: serde_json::Value = serde_json::from_slice(&fs::read(d.join("status.json")).unwrap()).unwrap();
        assert_eq!((v["state"].as_str(), v["from"].as_str()), (Some("failed"), Some("0.4.3")));
        for p in [Phase::Running, Phase::Ok] {
            assert!(serde_json::to_string(&p).unwrap().chars().all(|c| c == '"' || c.is_ascii_lowercase()));
        }
        // the panel's Go side reads these names
        let go = fs::read_to_string("../internal/panel/updates/updates.go").unwrap();
        for tag in ["`json:\"state\"", "`json:\"version\"`", "`json:\"from\"`", "`json:\"error\"`", "`json:\"at\""] {
            assert!(go.contains(tag), "the panel has no {tag}");
        }
        assert!(go.contains("enum:\"running,ok,failed\""));
        fs::remove_dir_all(&d).unwrap();
    }

    #[test]
    fn auto_update_only_follows_the_policy_file() {
        let d = tmpdir("policy");
        fs::write(d.join("policy.json"), r#"{"auto": true}"#).unwrap();
        let dir = open(&d);
        let on = |dir: &Dir| {
            dir.read("policy.json", 4096)
                .ok()
                .flatten()
                .and_then(|b| serde_json::from_slice::<serde_json::Value>(&b).ok())
                .is_some_and(|v| v["auto"] == true)
        };
        assert!(on(&dir));
        fs::remove_file(d.join("policy.json")).unwrap();
        symlink("/etc/hostname", d.join("policy.json")).unwrap();
        assert!(!on(&dir), "a link is no policy");
        fs::remove_dir_all(&d).unwrap();
    }
}
