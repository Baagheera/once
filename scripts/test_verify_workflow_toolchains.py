import unittest

import verify_workflow_toolchains as workflow


SETUP_GO = "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16"
CHECKOUT_STEP = """      - name: Check out tag
        run: git checkout --detach "${{ steps.tag.outputs.tag }}"
"""
SETUP_STEP = f"""      - uses: {SETUP_GO} # v6
        with:
          go-version: 1.26.5
"""
SCAN_STEP = f"""      - name: Scan Go vulnerabilities
        run: {workflow.VULN_COMMAND}
"""
BUILD_STEP = """      - name: Build artifacts
        run: python scripts/build_release_artifacts.py "${{ steps.tag.outputs.tag }}"
"""

VALID_CI = f"""name: ci

jobs:
  test:
    steps:
      - uses: {SETUP_GO} # v6
        with:
          go-version: 1.26.5

  minimum-go:
    steps:
      - uses: {SETUP_GO} # v6
        with:
          go-version: 1.25.12
"""

VALID_RELEASE = f"""name: release

jobs:
  build:
    steps:
      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
      - run: python scripts/verify_workflow_toolchains.py
      - name: Choose tag
        id: tag
        run: select-tag
{CHECKOUT_STEP}{SETUP_STEP}{SCAN_STEP}{BUILD_STEP}"""


class WorkflowToolchainTests(unittest.TestCase):
    def assert_invalid(self, ci: str, release: str, message: str) -> None:
        failures = workflow.workflow_failures(ci, release)
        self.assertIn(message, "\n".join(failures))

    def test_accepts_valid_ci_and_release_workflows(self) -> None:
        self.assertEqual(workflow.workflow_failures(VALID_CI, VALID_RELEASE), [])

    def test_rejects_swapped_ci_job_versions(self) -> None:
        ci = VALID_CI.replace("go-version: 1.26.5", "go-version: SWAPPED", 1)
        ci = ci.replace("go-version: 1.25.12", "go-version: 1.26.5", 1)
        ci = ci.replace("go-version: SWAPPED", "go-version: 1.25.12", 1)
        self.assert_invalid(ci, VALID_RELEASE, "ci.yml job test")

    def test_rejects_commented_vulnerability_command(self) -> None:
        commented = """      # - name: Scan Go vulnerabilities
      #   run: go run golang.org/x/vuln/cmd/govulncheck@v1.5.0 ./...
"""
        release = VALID_RELEASE.replace(SCAN_STEP, commented)
        self.assert_invalid(VALID_CI, release, "release.yml job build ordering")

    def test_rejects_conditional_vulnerability_scan(self) -> None:
        conditional = SCAN_STEP.replace(
            "        run:", "        if: always()\n        run:"
        )
        release = VALID_RELEASE.replace(SCAN_STEP, conditional)
        self.assert_invalid(VALID_CI, release, "release.yml job build ordering")

    def test_rejects_scan_before_selected_tag_checkout(self) -> None:
        ordered = CHECKOUT_STEP + SETUP_STEP + SCAN_STEP + BUILD_STEP
        unsafe = SCAN_STEP + CHECKOUT_STEP + SETUP_STEP + BUILD_STEP
        release = VALID_RELEASE.replace(ordered, unsafe)
        self.assert_invalid(VALID_CI, release, "release.yml job build ordering")

    def test_rejects_scan_after_artifact_build(self) -> None:
        ordered = CHECKOUT_STEP + SETUP_STEP + SCAN_STEP + BUILD_STEP
        unsafe = CHECKOUT_STEP + SETUP_STEP + BUILD_STEP + SCAN_STEP
        release = VALID_RELEASE.replace(ordered, unsafe)
        self.assert_invalid(VALID_CI, release, "release.yml job build ordering")

    def test_rejects_wrong_release_setup_go_version(self) -> None:
        release = VALID_RELEASE.replace("go-version: 1.26.5", "go-version: 1.26.4")
        self.assert_invalid(VALID_CI, release, "release.yml job build setup-go")

    def test_rejects_unpinned_release_setup_go_action(self) -> None:
        release = VALID_RELEASE.replace(SETUP_GO, "actions/setup-go@v6")
        self.assert_invalid(VALID_CI, release, "release.yml job build setup-go")

    def test_rejects_vulnerability_scan_in_another_job(self) -> None:
        release = VALID_RELEASE.replace(SCAN_STEP, "")
        release += f"""
  publish:
    steps:
      - run: {workflow.VULN_COMMAND}
"""
        self.assert_invalid(VALID_CI, release, "release.yml job build ordering")


if __name__ == "__main__":
    unittest.main()
