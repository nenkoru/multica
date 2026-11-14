#!/usr/bin/env python3
"""Explicit experimental opt-in for a NEW, isolated test home. No credentials read.

Production integration is deliberately absent until independent review and build.
"""
import argparse
import hashlib
import json
import os
import pathlib
import tempfile


def scope_digest(workspace, principal, permissions_digest, config_digest, auth_digest):
    for value in (permissions_digest, config_digest, auth_digest):
        if len(value) != 64 or any(c not in "0123456789abcdef" for c in value):
            raise ValueError("scope fingerprints must be lowercase SHA256 digests")
    if not workspace or not principal:
        raise ValueError("workspace and effective principal must be explicit")
    value = ["catalog-store-v1", workspace, principal, permissions_digest, config_digest, auth_digest]
    return hashlib.sha256(json.dumps(value, separators=(",", ":")).encode()).hexdigest()


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--home", required=True, type=pathlib.Path)
    p.add_argument("--root", required=True, type=pathlib.Path)
    p.add_argument("--workspace", required=True)
    p.add_argument("--principal", required=True)
    p.add_argument("--permissions-digest", required=True)
    p.add_argument("--config-digest", required=True)
    p.add_argument("--auth-digest", required=True)
    a = p.parse_args()
    scope = scope_digest(a.workspace, a.principal, a.permissions_digest, a.config_digest, a.auth_digest)
    if not a.home.is_absolute() or not a.root.is_absolute():
        p.error("absolute test-home and store paths are required")
    a.home.mkdir(mode=0o700)  # Refuse an existing, potentially active home.
    a.root.mkdir(mode=0o700, exist_ok=True)
    if a.root.is_symlink() or a.root.stat().st_mode & 0o077:
        p.error("store root must be a private directory, mode 0700")
    fd, name = tempfile.mkstemp(dir=a.home, prefix=".store-config-")
    with os.fdopen(fd, "w") as stream:
        json.dump({"root": str(a.root), "scope": scope}, stream)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(name, a.home / ".remote_plugin_catalog_store.json")
    print("configured isolated test home; production integration remains disabled")


if __name__ == "__main__":
    main()
