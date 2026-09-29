//! mikan: installs the mikan VPN panel, or a node of one, and manages it from the
//! server's shell. Without a command it opens the installer on a fresh server and the
//! menu on an installed one; every menu action is a command too.

mod docker;
mod envfile;
mod host;
mod net;
mod ops;
mod release;
mod setup;
mod sites;
mod system;
mod tui;

use std::path::PathBuf;
use std::process::ExitCode;

use clap::{Parser, Subcommand};

/// Where mikan lives on the server.
pub const DIR: &str = "/opt/mikan";

/// This installer's version: the release tag it was built from, "dev" otherwise.
pub fn version() -> &'static str {
    option_env!("MIKAN_VERSION").map(|v| v.trim_start_matches('v')).unwrap_or("dev")
}

#[derive(Parser)]
#[command(
    name = "mikan",
    version = version(),
    about = "mikan VPN panel: the installer and the server's menu",
    after_help = "Without a command: the installer on a fresh server, the menu on an installed one."
)]
struct Cli {
    #[command(subcommand)]
    cmd: Option<Cmd>,
}

#[derive(Subcommand)]
enum Cmd {
    /// Install the panel, or with --join a node of another panel
    Install(setup::Options),
    /// Containers, version, health and whether an update is out
    Status,
    /// Follow the logs (Ctrl+C stops)
    Logs {
        #[arg(value_parser = ["panel", "node"])]
        service: Option<String>,
    },
    /// Print the panel's link
    Url,
    /// Set a new admin password; all sessions end
    ResetPassword,
    /// Give the panel a new secret link
    ResetPath,
    /// Turn off the admin's 2FA (a lost phone)
    #[command(name = "disable-2fa")]
    Disable2fa,
    /// The panel's nodes: list, add, key, set
    #[command(disable_help_flag = true)]
    Node {
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
    /// Inbounds: list, add, set; a new port is opened in ufw
    #[command(disable_help_flag = true)]
    Inbound {
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
    /// REALITY camouflage sites: scan, check, apply
    #[command(disable_help_flag = true)]
    Targets {
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
    /// Back up the database, certificates and settings to /opt/mikan/backups
    Backup,
    /// Replace the data with a backup's
    Restore {
        file: PathBuf,
        /// Do not ask
        #[arg(long, short = 'y')]
        yes: bool,
    },
    /// Update to the latest release; goes back when the new version does not start
    Update(ops::UpdateArgs),
    /// Node: take a new join key from the panel's Nodes page
    Join { key: String },
    /// Restart the containers
    Restart,
    /// Stop mikan and remove the command; the data stays in /opt/mikan
    Uninstall {
        /// Do not ask
        #[arg(long, short = 'y')]
        yes: bool,
    },
}

fn main() -> ExitCode {
    let cli = Cli::parse();
    let result = match cli.cmd {
        None => tui::start(),
        Some(Cmd::Install(o)) => setup::install(o),
        Some(Cmd::Status) => ops::status(),
        Some(Cmd::Logs { service }) => ops::logs(service.as_deref()),
        Some(Cmd::Url) => ops::admin(&["url"]),
        Some(Cmd::ResetPassword) => ops::admin(&["reset-password"]),
        Some(Cmd::ResetPath) => ops::admin(&["reset-path"]),
        Some(Cmd::Disable2fa) => ops::admin(&["disable-2fa"]),
        Some(Cmd::Node { args }) => passthrough("node", &args),
        Some(Cmd::Targets { args }) => passthrough("targets", &args),
        Some(Cmd::Inbound { args }) => ops::inbound(&args),
        Some(Cmd::Backup) => ops::backup().map(|f| println!("Backup: {}", f.display())),
        Some(Cmd::Restore { file, yes }) => {
            if yes || ops::confirm(&format!("Replace the current data with {}?", file.display())) {
                ops::restore(&file).map(|()| println!("Restored from {}.", file.display()))
            } else {
                Ok(())
            }
        }
        Some(Cmd::Update(a)) => ops::update(&a, &mut |l| println!("{l}"), &mut progress_line()),
        Some(Cmd::Join { key }) => ops::join(&key),
        Some(Cmd::Restart) => ops::restart(),
        Some(Cmd::Uninstall { yes }) => {
            if yes || ops::confirm("Stop mikan and remove the mikan command? The data stays in /opt/mikan.") {
                ops::uninstall().map(|()| println!("Done. The data and backups stay in {DIR}; remove them with: rm -rf {DIR}"))
            } else {
                Ok(())
            }
        }
    };
    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("mikan: {e:#}");
            ExitCode::FAILURE
        }
    }
}

fn passthrough(cmd: &str, args: &[String]) -> anyhow::Result<()> {
    let mut full = vec![cmd];
    full.extend(args.iter().map(String::as_str));
    ops::admin(&full)
}

/// Prints the pull's progress in quarters.
fn progress_line() -> impl FnMut(f64) {
    let mut last = -1;
    move |p| {
        let q = (p * 4.0) as i32;
        if q != last {
            last = q;
            println!("  {}%", q * 25);
        }
    }
}
