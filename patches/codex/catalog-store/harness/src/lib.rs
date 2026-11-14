// Compile the PINNED, unmodified/patch-applied catalog writer, with small type
// adapters instead of the entire app-server graph. This is not a full Codex build.
extern crate self as codex_login;
extern crate self as codex_utils_path;

pub struct AuthHeaders(http::HeaderMap);
impl AuthHeaders {
    pub fn new(headers: http::HeaderMap) -> Self { Self(headers) }
}
pub enum CodexAuth { Headers(AuthHeaders) }
impl CodexAuth {
    pub fn get_account_id(&self) -> Option<String> { None }
    pub fn get_chatgpt_user_id(&self) -> Option<String> { None }
    pub fn is_workspace_account(&self) -> bool { false }
}
pub mod test_support {
    pub fn test_http_client_factory() {}
}

// Fetched verbatim from codex-utils-path, same pinned source revision.
include!("atomic_writer.rs");

mod remote {
    use serde::{Deserialize, Serialize};
    pub(super) type RemotePluginDirectoryItem = serde_json::Value;
    #[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
    pub(super) enum RemotePluginScope { Global, User, Workspace }
    pub(super) struct RemotePluginServiceConfig {
        pub chatgpt_base_url: String,
        pub product_sku: String,
    }
    impl RemotePluginServiceConfig {
        pub fn new(chatgpt_base_url: String, _: (), product_sku: Option<String>) -> Self {
            Self { chatgpt_base_url, product_sku: product_sku.unwrap_or_default() }
        }
    }
    pub(super) mod catalog_cache;
    pub(super) mod legacy_catalog_cache;

pub fn probe(root: &std::path::Path, patched: bool) -> serde_json::Value {
    use crate::{AuthHeaders, CodexAuth};
    use std::{fs, os::unix::fs::MetadataExt};
    fn allocated(root: &std::path::Path) -> u64 {
        fs::read_dir(root).unwrap().map(|entry| {
            let entry = entry.unwrap();
            let info = entry.metadata().unwrap();
            if info.is_dir() { allocated(&entry.path()) } else { info.blocks() * 512 }
        }).sum()
    }
    let payload: Vec<_> = (0..1024).map(|i| serde_json::json!({
        "id": format!("synthetic-{i}"), "scope": "Global", "description": "synthetic".repeat(128)
    })).collect();
    let config = RemotePluginServiceConfig::new("https://synthetic.invalid".into(), (), None);
    let auth = CodexAuth::Headers(AuthHeaders::new(http::HeaderMap::new()));
    fs::create_dir_all(root).unwrap();
    let store = root.join("store");
    fs::create_dir(&store).unwrap();
    use std::os::unix::fs::PermissionsExt;
    fs::set_permissions(&store, fs::Permissions::from_mode(0o700)).unwrap();
    let mut points = vec![allocated(root)];
    for name in ["run-one", "run-two"] {
        let home = root.join(name);
        fs::create_dir(&home).unwrap();
        if patched {
            fs::write(home.join(".remote_plugin_catalog_store.json"), serde_json::to_vec(&serde_json::json!({
                "root": store, "scope": "a".repeat(64)
            })).unwrap()).unwrap();
            // No receipt means a fresh fetch is required before writing this response.
            assert!(catalog_cache::load_cached_directory_plugins(&home, &config, &auth, RemotePluginScope::Global, None).is_none());
            catalog_cache::write_cached_directory_plugins(&home, &config, &auth, RemotePluginScope::Global, None, &payload);
            let read = catalog_cache::load_cached_directory_plugins(&home, &config, &auth, RemotePluginScope::Global, None).unwrap();
            assert_eq!(read.plugins, payload);
            catalog_cache::remove_cached_directory_plugins(&home, &config, &auth, RemotePluginScope::Global, Some("unrelated"));
        } else {
            legacy_catalog_cache::write_cached_directory_plugins(&home, &config, &auth, RemotePluginScope::Global, None, &payload);
            let read = legacy_catalog_cache::load_cached_directory_plugins(&home, &config, &auth, RemotePluginScope::Global, None).unwrap();
            assert_eq!(read.plugins, payload);
        }
        points.push(allocated(root));
    }
    let first_delta = points[1] - points[0];
    let second_delta = points[2] - points[1];
    if patched { assert!(second_delta <= 8192, "repeated full catalog allocation: {second_delta}"); }
    else { assert!(second_delta >= 1024 * 1024, "baseline failed to reproduce full per-home write"); }
    serde_json::json!({"synthetic": true, "patched": patched, "allocated_points": points,
        "first_delta": first_delta, "second_delta": second_delta,
        "payload_plugins": payload.len(), "full_codex_build": false, "live_process_measurement": false})
}
}

pub fn probe(root: &std::path::Path, patched: bool) -> serde_json::Value {
    remote::probe(root, patched)
}
