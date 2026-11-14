#!/usr/bin/env python3
import pathlib
from prepare_patch import HERE, fetch, prepare

root = HERE / "harness" / "src"
original, _ = prepare(root / "remote")
(root / "remote" / "legacy_catalog_cache.rs").write_text(original)
text = fetch("codex-rs/utils/path-utils/src/lib.rs").decode()
begin = text.index("pub fn write_atomically(")
end = text.index("\nfn normalize_for_wsl", begin)
(root / "atomic_writer.rs").write_text(
    "use std::{io, io::Write, path::Path};\nuse tempfile::NamedTempFile;\n" + text[begin:end]
)
print("prepared pinned upstream writers, atomic helper and patch-applied modules")
