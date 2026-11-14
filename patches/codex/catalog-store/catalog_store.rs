//! Optional write deduplication. A new home has no receipt and must fetch remotely.
//! The launcher supplies a scope covering workspace, principal, permissions and config.
//! No latest-catalog index, links, or cross-home cache lookup is maintained.
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::fs;
use std::io::{self, Write};
use std::path::{Path, PathBuf};

pub(super) const CONFIG_FILE: &str = ".remote_plugin_catalog_store.json";

#[derive(Deserialize, Serialize)]
pub(super) struct StoreConfig {
    pub root: PathBuf,
    pub scope: String,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Receipt {
    schema_version: u8,
    scope: String,
    binding: String,
    digest: String,
    fetched_at: DateTime<Utc>,
}

pub(super) struct Store {
    directory: PathBuf,
    scope: String,
    binding: String,
}

fn invalid() -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, "invalid catalog store binding or payload")
}

fn is_digest(value: &str) -> bool {
    value.len() == 64 && value.bytes().all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

pub(super) fn digest(contents: &[u8]) -> String {
    format!("{:x}", Sha256::digest(contents))
}

fn private_directory(path: &Path, create: bool) -> io::Result<()> {
    if create {
        let mut builder = fs::DirBuilder::new();
        #[cfg(unix)]
        {
            use std::os::unix::fs::DirBuilderExt;
            builder.mode(0o700);
        }
        match builder.create(path) {
            Ok(()) => (),
            Err(e) if e.kind() == io::ErrorKind::AlreadyExists => (),
            Err(e) => return Err(e),
        }
    }
    let metadata = fs::symlink_metadata(path)?;
    if !metadata.is_dir() || metadata.file_type().is_symlink() {
        return Err(invalid());
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if metadata.permissions().mode() & 0o077 != 0 {
            return Err(invalid());
        }
    }
    Ok(())
}

// Temporary file and publication are on the same filesystem. A receipt is only
// published after its complete, synced blob. Readers verify every blob digest.
fn publish(path: &Path, contents: &[u8]) -> io::Result<()> {
    let parent = path.parent().ok_or_else(invalid)?;
    let mut staged = tempfile::NamedTempFile::new_in(parent)?;
    staged.write_all(contents)?;
    staged.as_file().sync_all()?;
    staged.persist(path).map_err(|e| e.error)?;
    #[cfg(unix)]
    fs::File::open(parent)?.sync_all()?;
    Ok(())
}

impl Store {
    pub(super) fn from_home(home: &Path, binding: &[u8]) -> io::Result<Option<Self>> {
        let path = home.join(CONFIG_FILE);
        let raw = match fs::read(&path) {
            Ok(raw) if raw.len() <= 4096 => raw,
            Ok(_) => return Err(invalid()),
            Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(None),
            Err(e) => return Err(e),
        };
        let config: StoreConfig = serde_json::from_slice(&raw).map_err(|_| invalid())?;
        if !config.root.is_absolute() || !is_digest(&config.scope)
            || config.root.components().any(|p| matches!(p, std::path::Component::ParentDir))
        {
            return Err(invalid());
        }
        // Existing root is provisioned by the launcher, never silently chmod-ed.
        private_directory(&config.root, false)?;
        let directory = config.root.join(&config.scope);
        private_directory(&directory, true)?;
        let binding = digest(binding);
        let directory = directory.join(&binding);
        private_directory(&directory, true)?;
        Ok(Some(Self { directory, scope: config.scope, binding }))
    }

    pub(super) fn load(&self, raw: &[u8]) -> io::Result<(Vec<u8>, DateTime<Utc>)> {
        let receipt: Receipt = serde_json::from_slice(raw).map_err(|_| invalid())?;
        if receipt.schema_version != 2 || receipt.scope != self.scope
            || receipt.binding != self.binding || !is_digest(&receipt.digest)
        {
            return Err(invalid());
        }
        let path = self.directory.join(format!("{}.json", receipt.digest));
        if !fs::symlink_metadata(&path)?.is_file() {
            return Err(invalid());
        }
        let contents = fs::read(path)?;
        if digest(&contents) != receipt.digest {
            return Err(invalid());
        }
        Ok((contents, receipt.fetched_at))
    }

    pub(super) fn write(&self, receipt_path: &Path, contents: &[u8], fetched_at: DateTime<Utc>) -> io::Result<()> {
        let hash = digest(contents);
        let blob = self.directory.join(format!("{hash}.json"));
        match fs::symlink_metadata(&blob) {
            Ok(m) if !m.is_file() => return Err(invalid()),
            Ok(_) => {
                if digest(&fs::read(&blob)?) != hash {
                    publish(&blob, contents)?;
                }
            },
            Err(e) if e.kind() == io::ErrorKind::NotFound => publish(&blob, contents)?,
            Err(e) => return Err(e),
        }
        let receipt = Receipt { schema_version: 2, scope: self.scope.clone(),
            binding: self.binding.clone(), digest: hash, fetched_at };
        let raw = serde_json::to_vec(&receipt).map_err(|_| invalid())?;
        fs::create_dir_all(receipt_path.parent().ok_or_else(invalid)?)?;
        publish(receipt_path, &raw)
    }
}

#[cfg(test)]
#[path = "catalog_store_tests.rs"]
mod tests;
