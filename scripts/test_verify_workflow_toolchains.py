import unittest

import verify_workflow_toolchains as workflow


SETUP_GO = "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6"


def setup_step(version: str) -> str:
    return f"""      - uses: {SETUP_GO}
        with:
          go-version: {version}
          cache: true
"""


CI_CURRENT_SETUP = setup_step("1.26.5")
CI_MINIMUM_SETUP = setup_step("1.25.12")
CHECKOUT_STEP = """      - name: Check out tag
        run: git checkout --detach "${{ steps.tag.outputs.tag }}"
"""
RELEASE_SETUP = setup_step("1.26.5")
SCAN_STEP = """      - name: Scan Go vulnerabilities
        run: go run golang.org/x/vuln/cmd/govulncheck@v1.5.0 ./...
"""
BUILD_STEP = """      - name: Build artifacts
        run: python scripts/build_release_artifacts.py "${{ steps.tag.outputs.tag }}"
"""
VERIFY_STEP = """      - name: Verify artifacts
        run: python scripts/verify_release_artifacts.py "${{ steps.tag.outputs.tag }}"
"""
RELEASE_SEQUENCE = CHECKOUT_STEP + RELEASE_SETUP + SCAN_STEP + BUILD_STEP + VERIFY_STEP
PROVENANCE_GATE = """          if [ "$tag_commit" != "$GITHUB_SHA" ]; then
            echo "dispatch this workflow from the requested tag" >&2
            exit 1
          fi
"""

VALID_CI = f"""name: ci

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
{CI_CURRENT_SETUP}
  minimum-go:
    runs-on: ubuntu-latest
    steps:
{CI_MINIMUM_SETUP}"""

VALID_RELEASE = f"""name: release

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: Choose tag
        id: tag
        run: |
          if ! tag_commit="$(git rev-parse -q --verify "refs/tags/$tag^{{commit}}")"; then
            exit 1
          fi
{PROVENANCE_GATE}{RELEASE_SEQUENCE}"""


def with_step_control(step: str, control: str) -> str:
    return step.replace("        run:", f"        {control}\n        run:", 1)


def with_job_control(text: str, job: str, control: str) -> str:
    return text.replace(f"  {job}:\n", f"  {job}:\n{control}", 1)


class WorkflowToolchainTests(unittest.TestCase):
    def assert_invalid(self, ci: str, release: str) -> None:
        failures = workflow.workflow_failures(ci, release)
        self.assertTrue(failures, "unsafe fixture was accepted")

    def test_accepts_canonical_workflows(self) -> None:
        self.assertEqual(workflow.workflow_failures(VALID_CI, VALID_RELEASE), [])

    def test_rejects_extra_scan_step_controls(self) -> None:
        for control in (
            "shell: bash",
            "env:\n          FLAG: unsafe",
            "working-directory: scripts",
        ):
            with self.subTest(control=control):
                scan = with_step_control(SCAN_STEP, control)
                self.assert_invalid(VALID_CI, VALID_RELEASE.replace(SCAN_STEP, scan))

    def test_rejects_conditional_or_non_blocking_build(self) -> None:
        for control in ("if: always()", "continue-on-error: true"):
            with self.subTest(control=control):
                build = with_step_control(BUILD_STEP, control)
                self.assert_invalid(VALID_CI, VALID_RELEASE.replace(BUILD_STEP, build))

    def test_rejects_workflow_or_critical_job_env_and_defaults(self) -> None:
        for control in (
            "env:\n  FLAG: unsafe\n\n",
            "defaults:\n  run:\n    shell: bash\n\n",
        ):
            self.assert_invalid(control + VALID_CI, VALID_RELEASE)
            self.assert_invalid(VALID_CI, control + VALID_RELEASE)

        for control in (
            "    env:\n      FLAG: unsafe\n",
            "    defaults:\n      run:\n        shell: bash\n",
        ):
            for job in ("test", "minimum-go"):
                ci = with_job_control(VALID_CI, job, control)
                self.assert_invalid(ci, VALID_RELEASE)
            release = with_job_control(VALID_RELEASE, "build", control)
            self.assert_invalid(VALID_CI, release)

    def test_rejects_missing_mismatched_or_duplicate_provenance_gate(self) -> None:
        releases = (
            VALID_RELEASE.replace(PROVENANCE_GATE, ""),
            VALID_RELEASE.replace("$GITHUB_SHA", "$GITHUB_REF"),
            VALID_RELEASE.replace(PROVENANCE_GATE, PROVENANCE_GATE * 2),
        )
        for release in releases:
            with self.subTest(release=release):
                self.assert_invalid(VALID_CI, release)

    def test_rejects_reordered_release_sequence(self) -> None:
        reordered = CHECKOUT_STEP + RELEASE_SETUP + BUILD_STEP + SCAN_STEP + VERIFY_STEP
        self.assert_invalid(VALID_CI, VALID_RELEASE.replace(RELEASE_SEQUENCE, reordered))

    def test_rejects_swapped_ci_versions(self) -> None:
        swapped = VALID_CI.replace("go-version: 1.26.5", "go-version: SWAP", 1)
        swapped = swapped.replace("go-version: 1.25.12", "go-version: 1.26.5", 1)
        swapped = swapped.replace("go-version: SWAP", "go-version: 1.25.12", 1)
        self.assert_invalid(swapped, VALID_RELEASE)

    def test_rejects_additional_setup_go_steps(self) -> None:
        extra_ci = VALID_CI.replace(CI_CURRENT_SETUP, CI_CURRENT_SETUP * 2, 1)
        extra_release = VALID_RELEASE.replace(RELEASE_SEQUENCE, RELEASE_SETUP + RELEASE_SEQUENCE)
        self.assert_invalid(extra_ci, VALID_RELEASE)
        self.assert_invalid(VALID_CI, extra_release)


if __name__ == "__main__":
    unittest.main()
