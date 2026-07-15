#!/usr/bin/env python3

from hashlib import sha256
from pathlib import Path
import sys


CI_WORKFLOW = Path(__file__).resolve().parents[1] / ".github" / "workflows" / "ci.yml"
RELEASE_WORKFLOW = Path(__file__).resolve().parents[1] / ".github" / "workflows" / "release.yml"
CI_DIGEST = "7b691691daede6d105a802c12616e38ec86c765e7ab858940d3222bd75db1891"
RELEASE_DIGEST = "6be5c05fb76f7df70acc6e48cb002e7fc35854d0c97683e1158a314c70449091"


def workflow_digest(workflow: str) -> str:
    # Whole-workflow digests reject inserted prefix/suffix steps. Line endings
    # are normalized so the same checkout is accepted on every platform.
    normalized = workflow.replace("\r\n", "\n").replace("\r", "\n")
    return sha256(normalized.encode("utf-8")).hexdigest()


def workflow_failures(ci: str, release: str) -> list[str]:
    failures = []
    for filename, workflow, approved in (
        ("ci.yml", ci, CI_DIGEST),
        ("release.yml", release, RELEASE_DIGEST),
    ):
        actual = workflow_digest(workflow)
        if actual != approved:
            failures.append(
                f"{filename} workflow policy changed: expected approved SHA-256 "
                f"{approved}, got {actual}; review the workflow and update its approved digest"
            )
    return failures


def main() -> int:
    failures = workflow_failures(
        CI_WORKFLOW.read_text(encoding="utf-8"), RELEASE_WORKFLOW.read_text(encoding="utf-8")
    )
    for failure in failures:
        print(f"workflow toolchain check: {failure}", file=sys.stderr)
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
