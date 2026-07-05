#!/usr/bin/env python3
import hashlib
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
DIST = ROOT / "dist"
TARGETS = (
    ("linux", "amd64"),
    ("linux", "arm64"),
    ("darwin", "amd64"),
    ("darwin", "arm64"),
    ("windows", "amd64"),
    ("windows", "arm64"),
)


def main() -> int:
    version = sys.argv[1] if len(sys.argv) > 1 else git_version()
    if not version:
        print("empty version", file=sys.stderr)
        return 2

    if DIST.exists():
        shutil.rmtree(DIST)
    DIST.mkdir(parents=True)

    for goos, goarch in TARGETS:
        build_one(version, goos, goarch)

    write_checksums()
    return 0


def git_version() -> str:
    return subprocess.check_output(
        ["git", "describe", "--tags", "--always", "--dirty"],
        cwd=ROOT,
        text=True,
    ).strip()


def build_one(version: str, goos: str, goarch: str) -> None:
    ext = ".exe" if goos == "windows" else ""
    name = f"once_{version}_{goos}_{goarch}"
    print(f"building {goos}/{goarch}", flush=True)

    with tempfile.TemporaryDirectory(prefix="once-release-") as tmp:
        work = Path(tmp)
        binary = work / f"once{ext}"
        env = os.environ.copy()
        env.update({"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"})

        subprocess.run(
            [
                "go",
                "build",
                "-trimpath",
                "-ldflags",
                f"-s -w -X github.com/Baagheera/once/internal/cli.version={version}",
                "-o",
                str(binary),
                "./cmd/once",
            ],
            cwd=ROOT,
            env=env,
            check=True,
        )

        shutil.copy2(ROOT / "README.md", work / "README.md")
        shutil.copy2(ROOT / "LICENSE", work / "LICENSE")

        if goos == "windows":
            archive = DIST / f"{name}.zip"
            with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as zf:
                zf.write(binary, binary.name)
                zf.write(work / "README.md", "README.md")
                zf.write(work / "LICENSE", "LICENSE")
        else:
            archive = DIST / f"{name}.tar.gz"
            with tarfile.open(archive, "w:gz") as tf:
                add_tar_file(tf, binary, binary.name, 0o755)
                add_tar_file(tf, work / "README.md", "README.md", 0o644)
                add_tar_file(tf, work / "LICENSE", "LICENSE", 0o644)


def add_tar_file(tf: tarfile.TarFile, path: Path, arcname: str, mode: int) -> None:
    info = tf.gettarinfo(path, arcname)
    info.mode = mode
    with path.open("rb") as f:
        tf.addfile(info, f)


def write_checksums() -> None:
    lines = []
    for path in sorted(DIST.iterdir()):
        if not path.is_file() or path.name == "SHA256SUMS":
            continue
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        lines.append(f"{digest}  {path.name}\n")
    (DIST / "SHA256SUMS").write_text("".join(lines), encoding="utf-8")


if __name__ == "__main__":
    raise SystemExit(main())
