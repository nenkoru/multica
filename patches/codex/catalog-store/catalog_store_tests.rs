use super::*;
use std::sync::{Arc, Barrier};

fn configure(home: &Path, root: &Path, scope: &str) {
    fs::create_dir_all(home).unwrap();
    let config = StoreConfig { root: root.to_owned(), scope: digest(scope.as_bytes()) };
    fs::write(home.join(CONFIG_FILE), serde_json::to_vec(&config).unwrap()).unwrap();
}

fn store(home: &Path) -> Store {
    Store::from_home(home, b"native-account-user-scope-collection-sku").unwrap().unwrap()
}

#[test]
fn two_new_homes_reuse_content_and_keep_independent_freshness() {
    let root = tempfile::tempdir().unwrap();
    let homes = tempfile::tempdir().unwrap();
    let a = homes.path().join("a");
    let b = homes.path().join("b");
    configure(&a, root.path(), "workspace/principal/permissions/config/auth");
    configure(&b, root.path(), "workspace/principal/permissions/config/auth");
    let first = store(&a);
    let second = store(&b);
    assert_eq!(first.directory, second.directory);
    let body = vec![b'x'; 256 * 1024];
    let now = Utc::now();
    first.write(&a.join("receipt"), &body, now).unwrap();
    let blob = first.directory.join(format!("{}.json", digest(&body)));
    let before = fs::metadata(&blob).unwrap().modified().unwrap();
    assert!(!b.join("receipt").exists()); // New run cannot read another run's catalog.
    let later = now + chrono::TimeDelta::seconds(10);
    second.write(&b.join("receipt"), &body, later).unwrap();
    assert_eq!(fs::read_dir(&first.directory).unwrap().count(), 1);
    assert_eq!(fs::metadata(blob).unwrap().modified().unwrap(), before);
    assert_eq!(first.load(&fs::read(a.join("receipt")).unwrap()).unwrap(), (body.clone(), now));
    assert_eq!(second.load(&fs::read(b.join("receipt")).unwrap()).unwrap(), (body, later));
    assert!(fs::metadata(b.join("receipt")).unwrap().len() < 1024);
}

#[test]
fn workspace_principal_permission_config_and_native_keys_are_separate() {
    let root = tempfile::tempdir().unwrap();
    let homes = tempfile::tempdir().unwrap();
    let original_home = homes.path().join("0");
    configure(&original_home, root.path(), "w1/p1/permissions1/config1/auth1");
    let original = store(&original_home);
    let receipt = original_home.join("receipt");
    original.write(&receipt, b"private catalog", Utc::now()).unwrap();
    let raw = fs::read(&receipt).unwrap();
    for (i, scope) in ["w2/p1/permissions1/config1/auth1", "w1/p2/permissions1/config1/auth1",
        "w1/p1/permissions2/config1/auth1", "w1/p1/permissions1/config2/auth1",
        "w1/p1/permissions1/config1/auth2"].iter().enumerate() {
        let home = homes.path().join(format!("other-{i}"));
        configure(&home, root.path(), scope);
        let other = store(&home);
        assert_ne!(other.directory, original.directory);
        assert!(other.load(&raw).is_err());
    }
    let other = Store::from_home(&original_home, b"changed-account-user-scope-collection-sku").unwrap().unwrap();
    assert_ne!(other.directory, original.directory);
    assert!(other.load(&raw).is_err());
}

#[test]
fn catalog_change_publishes_new_content_and_invalidation_removes_only_local_receipt() {
    let root = tempfile::tempdir().unwrap();
    let home = tempfile::tempdir().unwrap();
    configure(home.path(), root.path(), "scope");
    let store = store(home.path());
    let receipt = home.path().join("receipt");
    store.write(&receipt, b"allowed-before", Utc::now()).unwrap();
    store.write(&receipt, b"revoked-after", Utc::now()).unwrap();
    assert_eq!(store.load(&fs::read(&receipt).unwrap()).unwrap().0, b"revoked-after");
    fs::remove_file(&receipt).unwrap();
    assert!(!receipt.exists());
    assert_eq!(fs::read_dir(&store.directory).unwrap().count(), 2);
}

#[test]
fn failed_blob_publication_preserves_previous_receipt() {
    let root = tempfile::tempdir().unwrap();
    let home = tempfile::tempdir().unwrap();
    configure(home.path(), root.path(), "scope");
    let store = store(home.path());
    let receipt = home.path().join("receipt");
    store.write(&receipt, b"good", Utc::now()).unwrap();
    let before = fs::read(&receipt).unwrap();
    fs::create_dir(store.directory.join(format!("{}.json", digest(b"new")))).unwrap();
    assert!(store.write(&receipt, b"new", Utc::now()).is_err());
    assert_eq!(fs::read(&receipt).unwrap(), before);
    assert_eq!(store.load(&before).unwrap().0, b"good");
    assert!(!fs::read_dir(&store.directory).unwrap().any(|p| p.unwrap().file_name().to_string_lossy().starts_with(".tmp")));
}

#[test]
fn corruption_and_escaping_receipts_fail_closed_and_corruption_can_be_repaired() {
    let root = tempfile::tempdir().unwrap();
    let home = tempfile::tempdir().unwrap();
    configure(home.path(), root.path(), "scope");
    let store = store(home.path());
    let receipt = home.path().join("receipt");
    store.write(&receipt, b"good", Utc::now()).unwrap();
    let raw = fs::read(&receipt).unwrap();
    fs::write(store.directory.join(format!("{}.json", digest(b"good"))), b"bad").unwrap();
    assert!(store.load(&raw).is_err());
    store.write(&receipt, b"good", Utc::now()).unwrap();
    assert_eq!(store.load(&fs::read(&receipt).unwrap()).unwrap().0, b"good");
    let mut escaping: serde_json::Value = serde_json::from_slice(&raw).unwrap();
    escaping["digest"] = "../elsewhere".into();
    assert!(store.load(&serde_json::to_vec(&escaping).unwrap()).is_err());
}

#[test]
fn concurrent_publication_never_exposes_partial_catalog() {
    let root = tempfile::tempdir().unwrap();
    let home = tempfile::tempdir().unwrap();
    configure(home.path(), root.path(), "scope");
    let store = Arc::new(store(home.path()));
    let receipt = home.path().join("receipt");
    store.write(&receipt, &vec![0; 8192], Utc::now()).unwrap();
    let barrier = Arc::new(Barrier::new(5));
    std::thread::scope(|threads| {
        for i in 1..=4 {
            let store = store.clone();
            let receipt = receipt.clone();
            let barrier = barrier.clone();
            threads.spawn(move || {
                barrier.wait();
                for _ in 0..8 { store.write(&receipt, &vec![i; 8192], Utc::now()).unwrap(); }
            });
        }
        barrier.wait();
        for _ in 0..64 {
            let (body, _) = store.load(&fs::read(&receipt).unwrap()).unwrap();
            assert_eq!(body.len(), 8192);
            assert!(body.iter().all(|b| *b == body[0]));
        }
    });
}

#[test]
fn disabled_and_malformed_config_have_distinct_results() {
    let home = tempfile::tempdir().unwrap();
    assert!(Store::from_home(home.path(), b"key").unwrap().is_none());
    fs::write(home.path().join(CONFIG_FILE), b"{}").unwrap();
    assert!(Store::from_home(home.path(), b"key").is_err());
}

#[cfg(unix)]
#[test]
fn symlink_and_public_store_roots_are_rejected() {
    use std::os::unix::fs::{symlink, PermissionsExt};
    let root = tempfile::tempdir().unwrap();
    let home = tempfile::tempdir().unwrap();
    let link = home.path().join("root-link");
    symlink(root.path(), &link).unwrap();
    configure(home.path(), &link, "scope");
    assert!(Store::from_home(home.path(), b"key").is_err());
    fs::set_permissions(root.path(), fs::Permissions::from_mode(0o755)).unwrap();
    configure(home.path(), root.path(), "scope");
    assert!(Store::from_home(home.path(), b"key").is_err());
}
