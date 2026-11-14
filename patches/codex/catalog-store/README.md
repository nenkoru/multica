# RUSL-3771: external Codex catalog writer patch (disabled)

Codex CLI 0.159.2 writes an entire remote plugin catalog into every isolated
`CODEX_HOME`. These are Codex files, not a Multica-generated catalog. The native
writer is `codex-rs/core-plugins/src/remote/catalog_cache.rs`, upstream tag
`rust-v0.159.2`, commit `ff6aec96948b70d94983af2641a6b67c94faeff5`.
The pinned writer SHA256 is
`db0e9f48e0d294c46ac1378a8e5f659f66148994805d40261fd3c0cec32c89d3`.
`remote.rs::fetch_directory_plugins_for_scope_with_cache` calls this writer after
fetching a directory response. The root is the caller's Codex home; no public
configuration for a separate shared disk-catalog root was found in this source.

Installed executable evidence, 2026-11-14: package and CLI report 0.159.2,
binary SHA256 `1748767b230ebfc3d4ab7e4e254920d0c0ad9691fd8c11f190e7d44511a4a92e`.
The binary contains both `core-plugins/src/remote/catalog_cache.rs` and
`cache/remote_plugin_catalog`. A read-only stat of this issue's existing catalog
showed 73,729,460 logical / 73,736,192 allocated bytes. This is an existing-file
measurement, **not net growth attributable to a traced process**. No extra live
Codex/model sessions were created: Mika's 23:38 МСК resource correction forbids
two additional full live catalogs near the snapshot floor.

## Change and scope

`catalog-store.patch` is an external source patch, stored in the Multica fork for
review and reproducible application. It changes no running daemon, config,
credentials, sessions, deployed binary, or cleanup policy. Without the explicit
test-home file `.remote_plugin_catalog_store.json`, upstream behavior is retained.
No production integration creates that file.

An opted-in home keeps a small local receipt with its own `fetched_at`. Identical
typed catalog contents, without that separate freshness timestamp, are stored
once under `root / scope_sha256 / native_binding_sha256 / content_sha256.json`.
Changed contents are never coerced to match an older catalog. No file or
directory links are used for reuse.

The launcher's scope covers workspace, **effective principal**, permission,
effective-config and auth fingerprints. `configure_store.py` requires every
component and refuses existing homes; it is an isolated-test provisioning tool,
not a daemon hook. Never put credentials themselves in its arguments. An actual
integration must compute fingerprints in memory from the effective task inputs,
including user/workspace permission revisions; missing inputs must keep opt-in
disabled. Native binding additionally covers service URL, account, user,
workspace-account flag, catalog scope, collection and product SKU.

The confirmed Multica integration seam is
`server/internal/daemon/execenv/execenv.go:650,950`, where both preparation paths
call `prepareCodexHomeWithOpts`. `TaskContextForEnv` already carries task
`InitiatorType`/`InitiatorID`; preparation also has `WorkspaceID`. These identify
actor inputs without using the human's name/email or assuming the runtime
owner is the actor. Their mapping to the actual authenticated principal, and a
stable effective plugin-policy/config fingerprint, still need an explicit
adapter contract; session-store keys, incidental run paths and
filenames cannot substitute for it. No such adapter is enabled by this PR.

There is no shared "latest catalog" index: a new home has no receipt, cannot load
another home's response, and must fetch from the service. Deduplication happens
**after** a successful response. Scope/config/binding changes make existing
receipts miss. Explicit invalidation deletes only the home-local receipt, and
never other runs' blobs. The upstream three-hour freshness policy within one
home remains; this patch does not promise immediate same-run revocation beyond
upstream invalidation. Cross-run reuse adds no stale-response window.

Blobs and receipts are staged, synced and atomically renamed; the blob commits
before the receipt. Readers verify scope, binding and SHA256. Failed publication
leaves an old receipt valid or a safe miss. Concurrent publishers can commit the
same content, without exposing partial data. Root/scope directories are private;
the configured root and blob entries must not be symlinks. This is logical cache
isolation inside an existing OS account, not a new security boundary against a
malicious process with the same uid.

## Reproduction and verification

```sh
python3 prepare_harness.py
python3 emit_patch.py
python3 -m unittest -v test_scope.py
kjob run --cpu 2 --mem 2Gi --timeout 15m --image python \
  --artifact baseline.json --artifact patched.json --artifact harness/Cargo.lock \
  -- python3 cluster_check.py
```

The harness compiles the exact fetched original and patch-applied writer plus
the exact upstream atomic-write helper. Small auth/service/type adapters remove
the app-server dependency graph; plugin payloads are synthetic JSON. This is
**not a full `codex-core-plugins` compilation**. Eight upstream tests run across
the two writers, eight new Rust tests cover the store, and two Python tests
cover scope construction.

`cluster_check.py` also exercises a real partial staged write failure in a
separate subprocess: RLIMIT_FSIZE=32KiB, SIGXFSZ ignored, a 128KiB response.
The previous catalog remains readable and no partial staging file remains.

On 2026-11-14, kjob's node/pod external DNS failed before compilation. The same
synthetic files were transferred through the documented ru-exec file tools and
run synchronously on buvg01, with `CARGO_BUILD_JOBS=2`, Rust 1.90.0. No GPU or
model calls. Result: 16 Rust + 2 Python tests PASS. The baseline-check command
failed as expected with `RED: unchanged scope wrote another complete catalog`.

| ru-exec synthetic allocated bytes | Before | After first home | After second home | Second delta |
| --- | ---: | ---: | ---: | ---: |
| Pinned writer | 0 | 1,273,856 | 2,547,712 | 1,273,856 |
| Patched writer, same explicit scope | 0 | 1,249,280 | 1,257,472 | 8,192 |

These use `st_blocks * 512` over synthetic regular files, including test-home
config and receipts, excluding directory blocks. They are not observations of
new live hetzner catalogs, nor proof of disk space reclaimed in RUSL-3768.
The same compiled harness was SHA256-verified and run locally on hetzner-fsn1
against isolated synthetic data only: both rows and deltas matched exactly.

The patched **real upstream package** subsequently compiled with Rust 1.95.0:
`cargo +1.95.0 test --locked -p codex-core-plugins remote::catalog_cache` passed
12 tests, with 458 unrelated tests filtered, in 0.17s after compilation. This
check uses the actual auth/service/catalog types, not harness adapters. The
release tag's workspace version is 0.159.2 while its lockfile records internal
packages as 0.0.0. Cargo's resolver refreshed that isolated fixture's internal
versions/references; the external `(name, version, source)` sets were verified
unchanged. Existing system OpenSSL headers/libraries were selected explicitly
with `OPENSSL_INCLUDE_DIR=/usr/include` and
`OPENSSL_LIB_DIR=/usr/lib/x86_64-linux-gnu` because pkg-config is absent.
No system packages were installed. The cold build reached its 480s bound; the
single warm continuation completed in 79s. No Codex CLI release build or live
model session is claimed.

Independent review RUSL-3774 accepted the source/repro checkpoint at Multica
head `5813ad0fdf293d8b010fac81421ff8b6d9dbfa05`: reviewer reran 16 Rust + 2 Python
checks and added three writer checks, all 19 Rust tests passed. The production
source patch remains byte-identical after supplemental test/documentation
updates: SHA256 `13a96beb68461b974b4c050fb99266dc2295f847a76fde7b9425892cd35c425e`.
That verdict grants no production approval.

## Executable next step and release gate

Owner: fork maintainer; independent reviewer: Ревьюер (DSH); acceptance: Mika.
Apply and test in an isolated upstream checkout through kjob/ru-exec:

```sh
git clone --depth 1 --branch rust-v0.159.2 https://github.com/openai/codex.git codex-test
git -C codex-test apply --check /absolute/path/to/catalog-store.patch
git -C codex-test apply /absolute/path/to/catalog-store.patch
cd codex-test/codex-rs
cargo test -p codex-core-plugins remote::catalog_cache
```

Do not enable in production until release review, a Codex CLI release build,
the real Multica task-scope adapter, scoped store lifecycle/budget
review, current RUSL-3254 pause checks, and a bounded normal-run live measurement
with adequate headroom. CAS content changes can retain old blobs: no automatic
cleanup or retention change is introduced here. The source/repro checkpoint
does not claim the entire incident resolved or a deployable release accepted.

Rollback: opt-in is absent in production. Preserve Codex 0.159.2 with the exact
binary SHA above and Multica v0.8.1-rusl2760 (reported daemon commit 01fedcfd6).
For an isolated experiment, stop using the experimental binary/config and use a
fresh home with the original release. Retain the scoped store and receipts for
inspection; no destructive rollback or cleanup is required. PR base is
`custom/RUSL-2760-v081` at `5c46f9a6767945af777d61645223edc532fb656e`;
RUSL-3752/3763 checkouts and delivery remain untouched.
