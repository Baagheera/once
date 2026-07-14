#!/usr/bin/env python3
import hashlib
import os
import platform
import re
import stat
import subprocess
import sys
import tarfile
import tempfile
import zipfile
from pathlib import Path

from verify_workflow_toolchains import CURRENT_GO


ROOT = Path(__file__).resolve().parents[1]
DIST = ROOT / "dist"
EXPECTED_GO_VERSION = f"go{CURRENT_GO}"
TARGETS = (
    ("linux", "amd64", ".tar.gz", "once"),
    ("linux", "arm64", ".tar.gz", "once"),
    ("darwin", "amd64", ".tar.gz", "once"),
    ("darwin", "arm64", ".tar.gz", "once"),
    ("windows", "amd64", ".zip", "once.exe"),
    ("windows", "arm64", ".zip", "once.exe"),
)


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: verify_release_artifacts.py VERSION", file=sys.stderr)
        return 2

    version = sys.argv[1]
    try:
        verify_dist(version)
    except Exception as err:
        print(f"verify release artifacts: {err}", file=sys.stderr)
        return 1
    return 0


def verify_dist(version: str) -> None:
    if not DIST.is_dir():
        raise RuntimeError("dist directory does not exist")

    expected = {archive_name(version, goos, goarch, suffix) for goos, goarch, suffix, _ in TARGETS}
    expected.add("SHA256SUMS")
    actual = {path.name for path in DIST.iterdir() if path.is_file()}
    if actual != expected:
        missing = sorted(expected - actual)
        extra = sorted(actual - expected)
        raise RuntimeError(f"unexpected dist files; missing={missing} extra={extra}")

    checksums = read_checksums(DIST / "SHA256SUMS")
    if set(checksums) != expected - {"SHA256SUMS"}:
        missing = sorted((expected - {"SHA256SUMS"}) - set(checksums))
        extra = sorted(set(checksums) - (expected - {"SHA256SUMS"}))
        raise RuntimeError(f"unexpected checksums; missing={missing} extra={extra}")

    for name, digest in checksums.items():
        actual_digest = hashlib.sha256((DIST / name).read_bytes()).hexdigest()
        if actual_digest != digest:
            raise RuntimeError(f"checksum mismatch for {name}")

    for goos, goarch, suffix, binary in TARGETS:
        name = archive_name(version, goos, goarch, suffix)
        if suffix == ".tar.gz":
            verify_tar(DIST / name, binary)
        else:
            verify_zip(DIST / name, binary)

    smoke_linux_amd64(version)


def archive_name(version: str, goos: str, goarch: str, suffix: str) -> str:
    return f"once_{version}_{goos}_{goarch}{suffix}"


def read_checksums(path: Path) -> dict[str, str]:
    checksums: dict[str, str] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        digest, filename = line.split(maxsplit=1)
        if len(digest) != 64:
            raise RuntimeError(f"invalid digest for {filename}")
        checksums[filename] = digest
    return checksums


def verify_tar(path: Path, binary: str) -> None:
    with tarfile.open(path, "r:gz") as tf:
        members = {member.name: member for member in tf.getmembers()}
        if set(members) != {binary, "README.md", "LICENSE"}:
            raise RuntimeError(f"unexpected tar contents in {path.name}: {sorted(members)}")
        for member in members.values():
            reject_unsafe_archive_name(path, member.name)
            if not member.isfile():
                raise RuntimeError(f"non-regular tar entry in {path.name}: {member.name}")
        if members[binary].mode & 0o111 == 0:
            raise RuntimeError(f"{binary} is not executable in {path.name}")
        binary_file = tf.extractfile(members[binary])
        if binary_file is None:
            raise RuntimeError(f"cannot read {binary} from {path.name}")
        verify_archive_binary(binary_file.read(), binary, path.name)


def verify_zip(path: Path, binary: str) -> None:
    with zipfile.ZipFile(path) as zf:
        names = set(zf.namelist())
        if names != {binary, "README.md", "LICENSE"}:
            raise RuntimeError(f"unexpected zip contents in {path.name}: {sorted(names)}")
        for info in zf.infolist():
            reject_unsafe_archive_name(path, info.filename)
            if info.is_dir():
                raise RuntimeError(f"directory zip entry in {path.name}: {info.filename}")
            mode = info.external_attr >> 16
            file_type = stat.S_IFMT(mode)
            if file_type not in (0, stat.S_IFREG):
                raise RuntimeError(f"non-regular zip entry in {path.name}: {info.filename}")
        verify_archive_binary(zf.read(binary), binary, path.name)


def verify_archive_binary(contents: bytes, binary: str, archive: str) -> None:
    with tempfile.TemporaryDirectory(prefix="once-release-verify-") as tmp:
        binary_path = Path(tmp) / binary
        binary_path.write_bytes(contents)
        result = subprocess.run(
            ["go", "version", "-m", str(binary_path)],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
    if result.returncode != 0:
        detail = (
            result.stderr.strip()
            or result.stdout.strip()
            or f"exit code {result.returncode}"
        )
        raise RuntimeError(f"cannot read Go build metadata from {archive}: {detail}")
    verify_go_metadata(result.stdout, archive)


def verify_go_metadata(metadata: str, archive: str) -> None:
    lines = metadata.splitlines()
    first_line = lines[0] if lines else ""
    _, separator, version = first_line.rpartition(": ")
    version_pattern = r"go\d+\.\d+(?:\.\d+)?(?:beta\d+|rc\d+)?"
    if not separator or not re.fullmatch(version_pattern, version):
        raise RuntimeError(f"malformed Go build metadata in {archive}: {first_line!r}")
    if version != EXPECTED_GO_VERSION:
        raise RuntimeError(
            f"{archive} embedded Go version = {version!r}, want {EXPECTED_GO_VERSION!r}"
        )


def reject_unsafe_archive_name(path: Path, name: str) -> None:
    if name.startswith("/") or name.startswith("\\") or Path(name).is_absolute():
        raise RuntimeError(f"unsafe absolute archive path in {path.name}: {name}")
    if ".." in Path(name).parts:
        raise RuntimeError(f"unsafe parent archive path in {path.name}: {name}")


def smoke_linux_amd64(version: str) -> None:
    if platform.system() != "Linux" or platform.machine() not in {"x86_64", "AMD64"}:
        print("skipping linux/amd64 binary smoke test on this platform")
        return

    archive = DIST / archive_name(version, "linux", "amd64", ".tar.gz")
    with tempfile.TemporaryDirectory(prefix="once-release-smoke-") as tmp:
        tmp_path = Path(tmp)
        with tarfile.open(archive, "r:gz") as tf:
            safe_extract(tf, tmp_path)
        binary = tmp_path / "once"
        os.chmod(binary, 0o755)
        result = subprocess.run(
            [str(binary), "version"],
            cwd=tmp_path,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=True,
        )
    expected = f"once {version}\n"
    if result.stdout != expected:
        raise RuntimeError(f"once version stdout = {result.stdout!r}, want {expected!r}")
    if result.stderr:
        raise RuntimeError(f"once version stderr = {result.stderr!r}")


def safe_extract(tf: tarfile.TarFile, destination: Path) -> None:
    for member in tf.getmembers():
        reject_unsafe_archive_name(Path(tf.name or "archive"), member.name)
    tf.extractall(destination)


if __name__ == "__main__":
    raise SystemExit(main())
