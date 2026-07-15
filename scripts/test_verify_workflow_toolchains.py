import unittest

import verify_workflow_toolchains as workflow


VALID_CI = workflow.CI_WORKFLOW.read_text(encoding="utf-8")
VALID_RELEASE = workflow.RELEASE_WORKFLOW.read_text(encoding="utf-8")


class WorkflowToolchainTests(unittest.TestCase):
    def assert_invalid(self, ci: str, release: str) -> None:
        failures = workflow.workflow_failures(ci, release)
        self.assertTrue(failures, "unsafe fixture was accepted")
        for failure in failures:
            self.assertIn("workflow policy changed", failure)
            self.assertIn("approved SHA-256", failure)
            self.assertIn("review", failure)

    def replace_once(self, text: str, old: str, new: str) -> str:
        self.assertEqual(text.count(old), 1, f"fixture marker is not unique: {old!r}")
        return text.replace(old, new, 1)

    def test_accepts_live_canonical_workflows(self) -> None:
        self.assertEqual(workflow.workflow_failures(VALID_CI, VALID_RELEASE), [])

    def test_accepts_equivalent_line_endings(self) -> None:
        for ending in ("\r\n", "\r"):
            with self.subTest(ending=repr(ending)):
                ci = VALID_CI.replace("\n", ending)
                release = VALID_RELEASE.replace("\n", ending)
                self.assertEqual(workflow.workflow_failures(ci, release), [])

    def test_rejects_changed_go_pins(self) -> None:
        mutations = (
            self.replace_once(VALID_CI, "go-version: 1.26.5", "go-version: 1.26.4"),
            self.replace_once(VALID_CI, "go-version: 1.25.12", "go-version: 1.25.11"),
        )
        for ci in mutations:
            with self.subTest(ci=ci):
                self.assert_invalid(ci, VALID_RELEASE)

    def test_rejects_each_tag_provenance_change(self) -> None:
        without_ref_type = self.replace_once(
            VALID_RELEASE,
            """          if [ "$GITHUB_REF_TYPE" != "tag" ] ||
             [ "$GITHUB_REF_NAME" != "$tag" ] ||
""",
            '          if [ "$GITHUB_REF_NAME" != "$tag" ] ||\n',
        )
        without_ref_name = self.replace_once(
            VALID_RELEASE,
            '             [ "$GITHUB_REF_NAME" != "$tag" ] ||\n',
            "",
        )
        changed_sha = self.replace_once(
            VALID_RELEASE,
            '             [ "$tag_commit" != "$GITHUB_SHA" ]; then',
            '             [ "$tag_commit" != "$GITHUB_REF" ]; then',
        )
        for name, release in (
            ("ref-type", without_ref_type),
            ("ref-name", without_ref_name),
            ("sha", changed_sha),
        ):
            with self.subTest(condition=name):
                self.assert_invalid(VALID_CI, release)

    def test_rejects_arbitrary_step_before_tag_selection(self) -> None:
        marker = "      - name: Choose tag\n"
        extra = """      - name: Run arbitrary command
        run: printf unsafe
"""
        release = self.replace_once(VALID_RELEASE, marker, extra + marker)
        self.assert_invalid(VALID_CI, release)

    def test_rejects_mutation_and_reupload_after_canonical_upload(self) -> None:
        marker = "          if-no-files-found: error\n"
        extra = """      - name: Mutate uploaded artifacts
        run: printf unsafe >> dist/once-linux-amd64.tar.gz
      - name: Re-upload mutated artifacts
        uses: actions/upload-artifact@main
        with:
          name: release-dist-mutated
          path: dist/*
"""
        release = self.replace_once(VALID_RELEASE, marker, marker + extra)
        self.assert_invalid(VALID_CI, release)

    def test_rejects_changed_build_permissions_or_checkout(self) -> None:
        changed_permissions = self.replace_once(
            VALID_RELEASE,
            """    permissions:
      contents: read
      id-token: write
      attestations: write
""",
            """    permissions:
      contents: write
      id-token: write
      attestations: write
""",
        )
        changed_checkout = self.replace_once(
            VALID_RELEASE,
            "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
            "actions/checkout@main",
        )
        for release in (changed_permissions, changed_checkout):
            with self.subTest(release=release):
                self.assert_invalid(VALID_CI, release)

    def test_rejects_changed_publish_policy_or_extra_job(self) -> None:
        changed_action = self.replace_once(
            VALID_RELEASE,
            "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c",
            "actions/download-artifact@main",
        )
        changed_permissions = self.replace_once(
            VALID_RELEASE,
            """  publish:
    name: publish-release
    runs-on: ubuntu-latest
    needs: build
    permissions:
      contents: write
""",
            """  publish:
    name: publish-release
    runs-on: ubuntu-latest
    needs: build
    permissions:
      contents: read
""",
        )
        extra_job = VALID_RELEASE + """
  unexpected:
    runs-on: ubuntu-latest
    steps:
      - run: printf unsafe
"""
        for release in (changed_action, changed_permissions, extra_job):
            with self.subTest(release=release):
                self.assert_invalid(VALID_CI, release)


if __name__ == "__main__":
    unittest.main()
