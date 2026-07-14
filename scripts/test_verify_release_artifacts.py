import unittest

import verify_release_artifacts as artifacts


class GoMetadataTests(unittest.TestCase):
    def metadata_verifier(self):
        verifier = getattr(artifacts, "verify_go_metadata", None)
        self.assertIsNotNone(
            verifier,
            "verify_go_metadata must validate embedded Go build metadata",
        )
        return verifier

    def test_accepts_go_1_26_5_metadata(self) -> None:
        metadata = "/tmp/once: go1.26.5\n\tpath\tgithub.com/Baagheera/once/cmd/once\n"
        self.metadata_verifier()(metadata, "once_v0.6.1_linux_amd64.tar.gz")

    def test_rejects_older_go_metadata(self) -> None:
        metadata = "/tmp/once: go1.26.4\n\tpath\tgithub.com/Baagheera/once/cmd/once\n"
        with self.assertRaisesRegex(RuntimeError, "embedded Go version"):
            self.metadata_verifier()(metadata, "once_v0.6.1_linux_amd64.tar.gz")

    def test_rejects_malformed_go_metadata(self) -> None:
        metadata = "/tmp/once: definitely-not-go\n"
        with self.assertRaisesRegex(RuntimeError, "malformed Go build metadata"):
            self.metadata_verifier()(metadata, "once_v0.6.1_linux_amd64.tar.gz")


if __name__ == "__main__":
    unittest.main()
