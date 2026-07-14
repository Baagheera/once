import unittest

import verify_release_artifacts as artifacts


class GoMetadataTests(unittest.TestCase):
    def test_expected_go_version_is_release_toolchain(self) -> None:
        self.assertEqual(artifacts.EXPECTED_GO_VERSION, "go1.26.5")

    def test_accepts_go_1_26_5_metadata(self) -> None:
        metadata = "/tmp/once: go1.26.5\n\tpath\tgithub.com/Baagheera/once/cmd/once\n"
        artifacts.verify_go_metadata(metadata, "once_v0.6.1_linux_amd64.tar.gz")

    def test_rejects_older_go_metadata(self) -> None:
        metadata = "/tmp/once: go1.26.4\n\tpath\tgithub.com/Baagheera/once/cmd/once\n"
        with self.assertRaisesRegex(RuntimeError, "embedded Go version"):
            artifacts.verify_go_metadata(metadata, "once_v0.6.1_linux_amd64.tar.gz")

    def test_rejects_malformed_go_metadata(self) -> None:
        metadata = "/tmp/once: definitely-not-go\n"
        with self.assertRaisesRegex(RuntimeError, "malformed Go build metadata"):
            artifacts.verify_go_metadata(metadata, "once_v0.6.1_linux_amd64.tar.gz")

    def test_rejects_duplicate_archive_member_names(self) -> None:
        with self.assertRaisesRegex(RuntimeError, "duplicate archive member"):
            artifacts.unique_member_names(
                ["once", "README.md", "once", "LICENSE"], "release.tar.gz"
            )


if __name__ == "__main__":
    unittest.main()
