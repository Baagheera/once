#!/usr/bin/env python3

from pathlib import Path
import re
import sys


CI_WORKFLOW = Path(__file__).resolve().parents[1] / ".github" / "workflows" / "ci.yml"
RELEASE_WORKFLOW = Path(__file__).resolve().parents[1] / ".github" / "workflows" / "release.yml"
SETUP_GO = "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6"


def setup_step(version: str) -> str:
    return f"""      - uses: {SETUP_GO}
        with:
          go-version: {version}
          cache: true
"""


RELEASE_SEQUENCE = f"""      - name: Check out tag
        run: git checkout --detach "${{{{ steps.tag.outputs.tag }}}}"
{setup_step("1.26.5")}      - name: Scan Go vulnerabilities
        run: go run golang.org/x/vuln/cmd/govulncheck@v1.5.0 ./...
      - name: Build artifacts
        run: python scripts/build_release_artifacts.py "${{{{ steps.tag.outputs.tag }}}}"
      - name: Verify artifacts
        run: python scripts/verify_release_artifacts.py "${{{{ steps.tag.outputs.tag }}}}"
"""
PROVENANCE_GATE = """          if [ "$tag_commit" != "$GITHUB_SHA" ]; then
            echo "dispatch this workflow from the requested tag" >&2
            exit 1
          fi
"""


def job_section(workflow: str, name: str) -> str | None:
    jobs_marker, heading = "\njobs:\n", f"  {name}:\n"
    if workflow.count(jobs_marker) != 1:
        return None
    jobs = workflow.split(jobs_marker, 1)[1]
    if jobs.count(heading) != 1:
        return None
    section = jobs.split(heading, 1)[1]
    next_job = re.search(r"(?m)^  [A-Za-z0-9_-]+:\n", section)
    return section[: next_job.start()] if next_job else section


def exact_step_or_sequence(section: str, expected: str) -> bool:
    if section.count(expected) != 1:
        return False
    remainder = section.split(expected, 1)[1]
    return not remainder or remainder == "\n" or remainder.startswith("      - ")


def has_forbidden_scope_controls(text: str, indent: int) -> bool:
    forbidden = {f"{' ' * indent}env:", f"{' ' * indent}defaults:"}
    return any(line in forbidden for line in text.splitlines())


def workflow_failures(ci: str, release: str) -> list[str]:
    failures = []
    for filename, text in (("ci.yml", ci), ("release.yml", release)):
        if has_forbidden_scope_controls(text, 0):
            failures.append(f"{filename} must not define workflow-level env or defaults")

    for job_name, version in (("test", "1.26.5"), ("minimum-go", "1.25.12")):
        section = job_section(ci, job_name)
        if (
            section is None
            or has_forbidden_scope_controls(section, 4)
            or section.count("actions/setup-go@") != 1
            or not exact_step_or_sequence(section, setup_step(version))
        ):
            failures.append(f"ci.yml job {job_name} must use canonical Go {version} setup")

    build = job_section(release, "build")
    if (
        build is None
        or has_forbidden_scope_controls(build, 4)
        or build.count("actions/setup-go@") != 1
        or not exact_step_or_sequence(build, RELEASE_SEQUENCE)
    ):
        failures.append("release.yml job build must use the canonical release sequence")
    if build is None or build.count(PROVENANCE_GATE) != 1:
        failures.append("release.yml job build must enforce tag commit provenance exactly once")
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
