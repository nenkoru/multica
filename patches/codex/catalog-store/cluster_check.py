#!/usr/bin/env python3
"""Synthetic-only kjob entrypoint; no model, auth, or live catalog access."""
import json
import os
import pathlib
import shutil
import resource
import signal
import tempfile
import subprocess
import urllib.request

root = pathlib.Path.cwd()
experiment = pathlib.Path(tempfile.mkdtemp(prefix="probe-", dir=root))
os.environ["CARGO_HOME"] = str(root / ".cargo")
os.environ["RUSTUP_HOME"] = str(root / ".rustup")
os.environ["PATH"] = str(root / ".cargo/bin") + ":" + os.environ["PATH"]
if not shutil.which("cargo"):
    path = root / "rustup-init"
    urllib.request.urlretrieve(
        "https://static.rust-lang.org/rustup/dist/x86_64-unknown-linux-gnu/rustup-init", path
    )
    path.chmod(0o700)
    subprocess.run([str(path), "-y", "--profile", "minimal", "--default-toolchain", "1.90.0", "--no-modify-path"], check=True)
subprocess.run(["python3", "-m", "unittest", "-v", "test_scope.py"], check=True)
subprocess.run(["cargo", "test", "--locked", "--manifest-path", "harness/Cargo.toml"], check=True)
for mode, expected, filename in [("baseline-check", 101, "baseline.json"), ("patched", 0, "patched.json")]:
    with open(filename, "w") as stream:
        result = subprocess.run(["cargo", "run", "--manifest-path", "harness/Cargo.toml", "--", mode,
                                 str(experiment / mode)], stdout=stream)
    if result.returncode != expected:
        raise RuntimeError(f"{mode}: expected exit {expected}, got {result.returncode}")
    print(json.loads(pathlib.Path(filename).read_text()))

def limit_file_size():
    signal.signal(signal.SIGXFSZ, signal.SIG_IGN)
    resource.setrlimit(resource.RLIMIT_FSIZE, (32 * 1024, 32 * 1024))

result = subprocess.run(["harness/target/debug/catalog-store-harness", "failed-write",
                         str(experiment / "failed-write")], preexec_fn=limit_file_size,
                        restore_signals=False,
                        text=True, capture_output=True, check=True)
print(result.stdout)
pathlib.Path("failed-write.json").write_text(result.stdout)
