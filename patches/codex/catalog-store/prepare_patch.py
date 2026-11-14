#!/usr/bin/env python3
"""Fetch pinned public source, check bytes, and produce an external Codex patch."""
import hashlib
import pathlib
import subprocess
import urllib.request

HERE = pathlib.Path(__file__).resolve().parent
REVISION = "ff6aec96948b70d94983af2641a6b67c94faeff5"
REMOTE = "codex-rs/core-plugins/src/remote"


def fetch(path):
    return urllib.request.urlopen(
        f"https://raw.githubusercontent.com/openai/codex/{REVISION}/{path}", timeout=30
    ).read()


def prepare(destination):
    original = fetch(f"{REMOTE}/catalog_cache.rs").decode()
    if hashlib.sha256(original.encode()).hexdigest() != "db0e9f48e0d294c46ac1378a8e5f659f66148994805d40261fd3c0cec32c89d3":
        raise RuntimeError("pinned upstream writer checksum mismatch")
    source = original.replace(
        "use tracing::warn;", "use tracing::warn;\n\nmod catalog_store;"
    )
    start = "    let cache: RemotePluginCatalogDiskCache = match serde_json::from_slice(&bytes) {"
    replacement = """    // The binding includes native account/user/scope/collection identity and SKU.
    let binding = serde_json::to_vec(&(&cache_key, &config.product_sku)).ok()?;
    let (bytes, fetched_at) = match catalog_store::Store::from_home(codex_home, &binding) {
        Ok(Some(store)) => {
            let (bytes, fetched_at) = store.load(&bytes).ok()?;
            (bytes, Some(fetched_at))
        }
        Ok(None) => (bytes, None),
        Err(_) => return None, // Invalid opt-in configuration must fail closed.
    };
    let mut cache: RemotePluginCatalogDiskCache = match serde_json::from_slice(&bytes) {"""
    assert source.count(start) == 1
    source = source.replace(start, replacement)
    start = "    let freshness = if is_fresh(cache.fetched_at, Utc::now()) {"
    assert source.count(start) == 1
    source = source.replace(start, """    if let Some(fetched_at) = fetched_at {
        cache.fetched_at = Some(fetched_at);
    }
    let freshness = if is_fresh(cache.fetched_at, Utc::now()) {""")
    start = "    let Ok(contents) = serde_json::to_string_pretty(&RemotePluginCatalogDiskCache {"
    assert source.count(start) == 1
    source = source.replace(start, """    let Ok(binding) = serde_json::to_vec(&(&cache_key, &config.product_sku)) else {
        return;
    };
    match catalog_store::Store::from_home(codex_home, &binding) {
        Ok(Some(store)) => {
            // fetched_at remains per-home. Only unchanged typed catalog CONTENT is
            // deduplicated; changing permissions/plugins produces a different blob.
            let Ok(contents) = serde_json::to_vec(&RemotePluginCatalogDiskCache {
                schema_version: REMOTE_PLUGIN_CATALOG_DISK_CACHE_SCHEMA_VERSION,
                fetched_at: None,
                plugins: plugins.to_vec(),
            }) else {
                return;
            };
            let _ = store.write(&cache_path, &contents, Utc::now());
            return;
        }
        Ok(None) => (), // No launcher opt-in: retain upstream layout and behavior.
        Err(_) => return, // Never fall back to a full copy on failed opt-in.
    }
    let Ok(contents) = serde_json::to_string_pretty(&RemotePluginCatalogDiskCache {""")
    destination.mkdir(parents=True, exist_ok=True)
    (destination / "catalog_cache.rs").write_text(source)
    (destination / "catalog_cache_tests.rs").write_bytes(fetch(f"{REMOTE}/catalog_cache_tests.rs"))
    # The nested module's default directory is remote/catalog_cache/.
    module = destination / "catalog_cache"
    module.mkdir(exist_ok=True)
    for name in ("catalog_store.rs", "catalog_store_tests.rs"):
        (module / name).write_bytes((HERE / name).read_bytes())
    return original, source


if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("destination", type=pathlib.Path)
    opts = parser.parse_args()
    original, source = prepare(opts.destination)
    print(f"source={REVISION} original_sha256={hashlib.sha256(original.encode()).hexdigest()}")
