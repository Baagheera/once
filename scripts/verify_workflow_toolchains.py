#!/usr/bin/env python3

from dataclasses import dataclass
from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[1]
CI_WORKFLOW = ROOT / ".github" / "workflows" / "ci.yml"
RELEASE_WORKFLOW = ROOT / ".github" / "workflows" / "release.yml"
CURRENT_GO = "1.26.5"
MINIMUM_GO = "1.25.12"
VULN_COMMAND = "go run golang.org/x/vuln/cmd/govulncheck@v1.5.0 ./..."
CHECKOUT_COMMAND = 'git checkout --detach "${{ steps.tag.outputs.tag }}"'
BUILD_COMMAND = 'python scripts/build_release_artifacts.py "${{ steps.tag.outputs.tag }}"'
SETUP_GO = re.compile(r"^actions/setup-go@[0-9a-f]{40}$")


@dataclass(frozen=True)
class Step:
    uses: str | None = None
    run: str | None = None
    go_version: str | None = None
    conditional: bool = False
    continue_on_error: str | None = None


def literal_scalar(value: str) -> str:
    value = re.sub(r"\s+#.*$", "", value).strip()
    return value


def scalar(value: str) -> str:
    value = literal_scalar(value)
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
        return value[1:-1]
    return value


def mapping_entry(text: str) -> tuple[str, str] | None:
    key, separator, value = text.partition(":")
    if not separator or not re.fullmatch(r"[A-Za-z0-9_-]+", key):
        return None
    return key, scalar(value)


def indentation(line: str) -> int:
    return len(line) - len(line.lstrip(" "))


def ignored(line: str) -> bool:
    return not line.strip() or line.lstrip().startswith("#")


def job_steps(workflow: str, job_name: str) -> list[Step]:
    lines = workflow.splitlines()
    jobs_index = next(
        (
            index
            for index, line in enumerate(lines)
            if indentation(line) == 0 and line.strip() == "jobs:"
        ),
        None,
    )
    if jobs_index is None:
        return []

    job_index = None
    for index in range(jobs_index + 1, len(lines)):
        line = lines[index]
        if ignored(line):
            continue
        if indentation(line) == 0:
            break
        if indentation(line) == 2 and line.strip() == f"{job_name}:":
            job_index = index
            break
    if job_index is None:
        return []

    steps_index = None
    for index in range(job_index + 1, len(lines)):
        line = lines[index]
        if ignored(line):
            continue
        if indentation(line) <= 2:
            break
        if indentation(line) == 4 and line.strip() == "steps:":
            steps_index = index
            break
    if steps_index is None:
        return []

    raw_steps: list[list[str]] = []
    current: list[str] = []
    for line in lines[steps_index + 1 :]:
        if ignored(line):
            continue
        line_indent = indentation(line)
        if line_indent <= 4:
            break
        if line_indent == 6 and line.strip().startswith("-"):
            if current:
                raw_steps.append(current)
            current = [line]
        elif current:
            current.append(line)
    if current:
        raw_steps.append(current)
    return [parse_step(lines) for lines in raw_steps]


def parse_step(lines: list[str]) -> Step:
    base_indent = indentation(lines[0])
    uses = None
    run = None
    go_version = None
    conditional = False
    continue_on_error = None
    with_indent = None

    for index, line in enumerate(lines):
        line_indent = indentation(line)
        text = line.strip()
        if index == 0:
            text = text[1:].lstrip()
            entry = mapping_entry(text)
        elif line_indent == base_indent + 2:
            entry = mapping_entry(text)
            with_indent = line_indent if entry and entry[0] == "with" else None
        elif with_indent is not None and line_indent == with_indent + 2:
            entry = mapping_entry(text)
        else:
            entry = None

        if entry is None:
            continue
        key, value = entry
        if key == "uses" and line_indent <= base_indent + 2:
            uses = value
        elif key == "run" and line_indent <= base_indent + 2:
            run = value
        elif key == "if" and line_indent <= base_indent + 2:
            conditional = True
        elif key == "continue-on-error" and line_indent <= base_indent + 2:
            continue_on_error = literal_scalar(text.partition(":")[2])
        elif key == "go-version" and with_indent is not None:
            go_version = value

    return Step(
        uses=uses,
        run=run,
        go_version=go_version,
        conditional=conditional,
        continue_on_error=continue_on_error,
    )


def setup_go_steps(steps: list[Step]) -> list[Step]:
    return [step for step in steps if step.uses and step.uses.startswith("actions/setup-go@")]


def required_step(step: Step) -> bool:
    return not step.conditional and step.continue_on_error in (None, "false")


def workflow_failures(ci: str, release: str) -> list[str]:
    failures = []

    for job_name, version in (("test", CURRENT_GO), ("minimum-go", MINIMUM_GO)):
        setup = setup_go_steps(job_steps(ci, job_name))
        if (
            len(setup) != 1
            or setup[0].go_version != version
            or not required_step(setup[0])
        ):
            failures.append(
                f"ci.yml job {job_name} setup-go must be unconditional, fail closed, "
                f"and select Go {version} exactly once"
            )

    release_steps = job_steps(release, "build")
    release_setup = setup_go_steps(release_steps)
    if (
        len(release_setup) != 1
        or release_setup[0].go_version != CURRENT_GO
        or not release_setup[0].uses
        or not SETUP_GO.fullmatch(release_setup[0].uses)
        or not required_step(release_setup[0])
    ):
        failures.append(
            "release.yml job build setup-go must be unconditional, fail closed, use "
            f"a full SHA, and select Go {CURRENT_GO} exactly once"
        )

    checkout = [
        index
        for index, step in enumerate(release_steps)
        if step.run == CHECKOUT_COMMAND and required_step(step)
    ]
    setup = [
        index
        for index, step in enumerate(release_steps)
        if step in release_setup and required_step(step)
    ]
    scan = [
        index
        for index, step in enumerate(release_steps)
        if step.run == VULN_COMMAND and required_step(step)
    ]
    build = [index for index, step in enumerate(release_steps) if step.run == BUILD_COMMAND]
    if not (
        len(checkout) == len(setup) == len(scan) == len(build) == 1
        and checkout[0] < setup[0] < scan[0] < build[0]
    ):
        failures.append(
            "release.yml job build ordering must be selected-tag checkout, "
            "setup-go, unconditional govulncheck, artifact build"
        )
    return failures


def main() -> int:
    ci = CI_WORKFLOW.read_text(encoding="utf-8")
    release = RELEASE_WORKFLOW.read_text(encoding="utf-8")
    failures = workflow_failures(ci, release)

    if failures:
        for failure in failures:
            print(f"workflow toolchain check: {failure}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
