//! What the panel, or a node, asks of its server beside updates: a doctor run and a
//! restart of the containers. The container has no Docker socket and no shell, so it writes
//! a request file into its data directory (internal/hostchan) and a systemd path unit runs
//! the command, which takes the request away first:
//!
//!   data/<panel|node>/doctor/request    {"id": "<hex>", "at": "<RFC 3339>"}: `mikan doctor
//!                                       --requested` runs the checks and writes
//!                                       doctor/report.json, the report of `mikan doctor
//!                                       --json` with the same "id"
//!   data/<panel|node>/restart/request   the same: `mikan restart --requested` restarts the
//!                                       containers
//!
//! The container is not trusted with more than a name: everything is read and written
//! through panelfs (no link followed, no directory trusted), and the only thing taken from a
//! request is an id of plain characters, which comes back in the report so that its asker
//! knows the report is its own. Nothing in a request says what to run or where.

use std::path::Path;

use anyhow::Result;

use crate::DIR;
use crate::panelfs::Dir;

/// The largest request: what the panel writes is about sixty bytes.
const REQUEST_LIMIT: u64 = 4096;

/// The longest id taken.
const ID_LIMIT: usize = 64;

/// What was asked.
#[derive(Debug, PartialEq, Eq)]
pub struct Request {
    /// The asker's id for the answer; "" when the request carried none a report may echo.
    pub id: String,
}

/// The directory of a kind of request ("doctor", "restart") in the data of the panel or the
/// node this server runs; None while the container has not made it.
pub fn dir(node: bool, name: &str) -> Result<Option<Dir>> {
    Dir::open(Path::new(DIR), &format!("data/{}/{name}", if node { "node" } else { "panel" }), false)
}

/// Takes the request: whatever it is, it goes first, so that the unit watching for it does
/// not fire again and again. None when there is none, or when it is not one (a link, a
/// directory, a FIFO, a file of a size nobody writes): it is removed and nothing runs.
pub fn take(dir: &Dir) -> Result<Option<Request>> {
    Ok(take_bytes(dir)?.and_then(|data| parse(&data)))
}

/// The same for a request with a body of its own (the terminal's): the file's bytes, taken
/// the same way. None when there is none, or it is not a small regular file.
pub fn take_bytes(dir: &Dir) -> Result<Option<Vec<u8>>> {
    if !dir.exists("request") {
        return Ok(None);
    }
    let read = dir.read("request", REQUEST_LIMIT);
    dir.discard("request")?;
    Ok(read.ok().flatten())
}

/// The request in a file's bytes: JSON with an optional "id". Anything that is not a JSON
/// object is no request.
fn parse(data: &[u8]) -> Option<Request> {
    let v: serde_json::Value = serde_json::from_slice(data).ok()?;
    let obj = v.as_object()?;
    let id = obj.get("id").and_then(|i| i.as_str()).filter(|i| valid_id(i)).unwrap_or_default();
    Some(Request { id: id.to_owned() })
}

/// An id is a few plain characters: it is echoed into a file and into the panel's pages.
pub fn valid_id(id: &str) -> bool {
    !id.is_empty() && id.len() <= ID_LIMIT && id.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use std::os::unix::fs::symlink;

    fn tmpdir(name: &str) -> std::path::PathBuf {
        let d = std::env::temp_dir().join(format!("hostreq-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&d);
        fs::create_dir_all(&d).unwrap();
        d
    }

    #[test]
    fn a_request_names_nothing_but_an_id() {
        assert_eq!(parse(br#"{"id":"abc123","at":"2026-10-14T10:00:00Z"}"#), Some(Request { id: "abc123".into() }));
        // What a request says beside its id is not read: no command, no path.
        assert_eq!(parse(br#"{"id":"a-b_C","run":"rm -rf /","path":"/etc"}"#), Some(Request { id: "a-b_C".into() }));
        // An id that is not plain characters, or is too long, is dropped: the run happens, the id does not come back.
        for bad in [r#"{"id":"a b"}"#, r#"{"id":"../x"}"#, r#"{"id":"a\nb"}"#, r#"{"id":""}"#, r#"{"id":5}"#, r#"{}"#] {
            assert_eq!(parse(bad.as_bytes()), Some(Request { id: String::new() }), "{bad}");
        }
        let long = format!(r#"{{"id":"{}"}}"#, "a".repeat(ID_LIMIT + 1));
        assert_eq!(parse(long.as_bytes()), Some(Request { id: String::new() }));
        // Not a request at all.
        for bad in ["", "garbage", "[1]", "\"id\"", "5", "null"] {
            assert_eq!(parse(bad.as_bytes()), None, "{bad}");
        }
    }

    #[test]
    fn taking_a_request_removes_it_first() {
        let root = tmpdir("take");
        fs::create_dir_all(root.join("doctor")).unwrap();
        let dir = Dir::open(&root, "doctor", false).unwrap().unwrap();
        assert_eq!(take(&dir).unwrap(), None, "nothing asked");
        fs::write(root.join("doctor/request"), br#"{"id":"r1"}"#).unwrap();
        assert_eq!(take(&dir).unwrap(), Some(Request { id: "r1".into() }));
        assert!(!root.join("doctor/request").exists(), "the request stays and the unit fires again");
        // Not a request: removed all the same, and nothing runs for it.
        for (what, make) in [
            ("junk", Box::new(|p: &Path| fs::write(p, "junk").unwrap()) as Box<dyn Fn(&Path)>),
            ("too big", Box::new(|p: &Path| fs::write(p, vec![b'x'; REQUEST_LIMIT as usize + 1]).unwrap())),
            ("a directory", Box::new(|p: &Path| fs::create_dir(p).unwrap())),
            ("a link", Box::new(|p: &Path| symlink("/etc/passwd", p).unwrap())),
        ] {
            make(&root.join("doctor/request"));
            assert_eq!(take(&dir).unwrap(), None, "{what}");
            assert!(fs::symlink_metadata(root.join("doctor/request")).is_err(), "{what} was left");
        }
        assert!(fs::metadata("/etc/passwd").is_ok(), "the target of a planted link is untouched");
        fs::remove_dir_all(&root).unwrap();
    }

    #[test]
    fn a_missing_directory_is_no_request() {
        let root = tmpdir("none");
        assert!(Dir::open(&root, "data/panel/doctor", false).unwrap().is_none());
        fs::remove_dir_all(&root).unwrap();
    }

    #[test]
    fn ids() {
        assert!(valid_id("0a1b2c3d4e5f60718293a4b5"));
        for bad in ["", "a b", "a/b", "a.b", "é", &"a".repeat(65)] {
            assert!(!valid_id(bad), "{bad:?}");
        }
    }
}
