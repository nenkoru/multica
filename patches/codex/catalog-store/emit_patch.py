#!/usr/bin/env python3
"""Emit a git-apply compatible patch against the exact upstream revision."""
import difflib
from prepare_patch import HERE, REMOTE

generated = HERE / "harness/src/remote"
original = (generated / "legacy_catalog_cache.rs").read_text()
patched = (generated / "catalog_cache.rs").read_text()
parts = list(difflib.unified_diff(original.splitlines(True), patched.splitlines(True),
                                fromfile=f"a/{REMOTE}/catalog_cache.rs",
                                tofile=f"b/{REMOTE}/catalog_cache.rs"))
for name in ["catalog_store.rs", "catalog_store_tests.rs"]:
    contents = (HERE / name).read_text()
    parts.extend(difflib.unified_diff([], contents.splitlines(True), fromfile="/dev/null",
                                    tofile=f"b/{REMOTE}/catalog_cache/{name}"))
(HERE / "catalog-store.patch").write_text("".join(parts))
print("emitted catalog-store.patch")
