#!/usr/bin/env python3

from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[1]
CI_WORKFLOW = ROOT / ".github" / "workflows" / "ci.yml"
RELEASE_WORKFLOW = ROOT / ".github" / "workflows" / "release.yml"
CURRENT_GO = "1.26.5"
MINIMUM_GO = "1.25.12"
VULN_COMMAND = "go run golang.org/x/vuln/cmd/govulncheck@v1.5.0 ./..."


def version_count(workflow: str, version: str) -> int:
    pattern = rf"(?m)^\s+go-version:\s*{re.escape(version)}\s*$"
    return len(re.findall(pattern, workflow))


def main() -> int:
    ci = CI_WORKFLOW.read_text(encoding="utf-8")
    release = RELEASE_WORKFLOW.read_text(encoding="utf-8")
    failures = []

    if version_count(ci, CURRENT_GO) != 1:
        failures.append(f"ci.yml must select Go {CURRENT_GO} exactly once")
    if version_count(ci, MINIMUM_GO) != 1:
        failures.append(f"ci.yml must select Go {MINIMUM_GO} exactly once")
    if version_count(release, CURRENT_GO) != 1:
        failures.append(f"release.yml must select Go {CURRENT_GO} exactly once")
    if VULN_COMMAND not in release:
        failures.append("release.yml must run govulncheck before packaging")

    if failures:
        for failure in failures:
            print(f"workflow toolchain check: {failure}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
