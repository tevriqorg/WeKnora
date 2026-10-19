import tempfile
import unittest
from pathlib import Path

from implementation.ingress import IngressError, IngressStore


class IngressContractTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.store = IngressStore(self.root)

    def tearDown(self):
        self.tmp.cleanup()

    def new_session(self):
        return self.store.create(
            "fixture", "source-1", "fixture://device/source-1",
            mime="video/mp4", target_policy="archive-test",
        )

    def staged(self, payload=b"raw-payload"):
        session = self.new_session()
        return self.store.stage_bytes(session["ingest_id"], payload)

    def analyzed(self, payload=b"raw-payload"):
        session = self.staged(payload)
        self.store.begin_analysis(session["ingest_id"])
        self.store.mark_analyzed(session["ingest_id"], "manifest://analysis/1")
        return self.store.get(session["ingest_id"])

    def test_file_streaming_transit_keeps_source_and_one_final_raw(self):
        # Actual IngressStore, mock QNAP final storage; source files are read-only.
        source = self.root / "tfcard" / "DCIM"
        source.mkdir(parents=True)
        for number in (3, 4, 5, 6):
            original = source / f"{number}.mp4"
            original.write_bytes(f"POCKET-{number}".encode() * (256 * 1024))
            digest = __import__("hashlib").sha256(original.read_bytes()).hexdigest()
            session = self.store.create(
                "pocket3", f"clip-{number}", f"tfcard://DCIM/{number}.mp4", mime="video/mp4",
            )
            staged = self.store.stage_file(session["ingest_id"], original, chunk_size=65536)
            self.assertEqual(digest, staged["content_hash"])
            self.store.begin_analysis(session["ingest_id"])
            self.store.mark_analyzed(session["ingest_id"], f"manifest://clip-{number}")
            self.store.commit(session["ingest_id"], f"pocket3/{number}.mp4")
            self.store.mark_handoff_pending(session["ingest_id"])
            self.store.complete_handoff(session["ingest_id"])
            self.store.cleanup_staging(session["ingest_id"])
            self.assertEqual(digest, __import__("hashlib").sha256(original.read_bytes()).hexdigest())
            self.assertEqual(digest, __import__("hashlib").sha256(self.store.final_path(session["ingest_id"]).read_bytes()).hexdigest())
            self.assertEqual(session["ingest_id"],
                             self.store.create("pocket3", f"clip-{number}", f"tfcard://DCIM/{number}.mp4")["ingest_id"])
        self.assertEqual(4, len(list(self.store.final_root.rglob("*.mp4"))))
        self.assertEqual([], list(self.store.staging_root.iterdir()))

    def test_recover_commit_after_conflicting_final_is_cleared(self):
        session = self.analyzed()
        final = self.store.final_root / "camera" / "clip.mp4"
        final.parent.mkdir(parents=True)
        final.write_bytes(b"collision")
        with self.assertRaises(IngressError):
            self.store.commit(session["ingest_id"], "camera/clip.mp4")
        self.assertEqual("committing", self.store.get(session["ingest_id"])["state"])
        # No overwritten bytes; after owner resolves the conflict, retry can
        # finish the same session rather than creating a second final file.
        self.assertEqual(b"collision", final.read_bytes())
        final.unlink()
        self.store.commit(session["ingest_id"], "camera/clip.mp4")
        self.assertEqual(b"raw-payload", final.read_bytes())
        self.store.mark_handoff_pending(session["ingest_id"])
        self.store.complete_handoff(session["ingest_id"])
        self.store.cleanup_staging(session["ingest_id"])
        self.assertEqual([], list(self.store.staging_root.iterdir()))

    def test_file_streaming_does_not_call_path_read_bytes(self):
        from unittest.mock import patch
        source = self.root / "source.mp4"
        source.write_bytes(b"x" * 1024 * 1024)
        session = self.new_session()
        with patch.object(Path, "read_bytes", side_effect=AssertionError("whole-file read forbidden")):
            self.store.stage_file(session["ingest_id"], source, chunk_size=32768)
            self.store.begin_analysis(session["ingest_id"])
            self.store.mark_analyzed(session["ingest_id"], "manifest://data")
            committed = self.store.commit(session["ingest_id"], "safe/file.mp4")
            self.assertEqual("committed", committed["state"])
            self.store.cleanup_staging(session["ingest_id"])

    def test_archive_target_rejects_escape_and_does_not_write_outside(self):
        session = self.analyzed()
        for unsafe in ("../oops.mp4", "/tmp/oops.mp4", "dir/../../oops.mp4", ""):
            with self.subTest(name=unsafe):
                with self.assertRaises(IngressError):
                    self.store.commit(session["ingest_id"], unsafe)
        self.assertEqual("analyzed", self.store.get(session["ingest_id"])["state"])

    def test_durable_staging_survives_restart(self):
        session = self.staged()
        reopened = IngressStore(self.root)
        recovered = reopened.get(session["ingest_id"])
        self.assertEqual("staged", recovered["state"])
        self.assertTrue(Path(recovered["staging_ref"]).is_file())
        self.assertEqual(b"raw-payload", Path(recovered["staging_ref"]).read_bytes())

    def test_happy_path_commit_handoff_and_cleanup(self):
        session = self.analyzed()
        committed = self.store.commit(session["ingest_id"], "camera/a.mp4")
        self.assertEqual("committed", committed["state"])
        self.assertEqual(b"raw-payload", self.store.final_path(session["ingest_id"]).read_bytes())
        self.store.mark_handoff_pending(session["ingest_id"])
        self.store.complete_handoff(session["ingest_id"])
        self.store.cleanup_staging(session["ingest_id"])
        finished = self.store.get(session["ingest_id"])
        self.assertEqual("complete", finished["state"])
        self.assertEqual("complete", finished["handoff_status"])
        self.assertEqual("", finished["staging_ref"])

    def test_cleanup_refuses_before_verified_final(self):
        session = self.analyzed()
        with self.assertRaises(IngressError):
            self.store.cleanup_staging(session["ingest_id"])
        self.assertTrue(Path(session["staging_ref"]).exists())

    def test_commit_ack_uncertainty_recognizes_matching_final(self):
        session = self.analyzed()
        final = self.store.final_root / "camera/a.mp4"
        final.parent.mkdir(parents=True, exist_ok=True)
        final.write_bytes(b"raw-payload")
        committed = self.store.commit(session["ingest_id"], "camera/a.mp4")
        self.assertEqual("committed", committed["state"])
        self.assertEqual(final.as_uri(), committed["final_resource_ref"])
        self.assertEqual(b"raw-payload", final.read_bytes())

    def test_wrong_existing_final_fails_closed(self):
        session = self.analyzed()
        final = self.store.final_root / "camera/a.mp4"
        final.parent.mkdir(parents=True, exist_ok=True)
        final.write_bytes(b"different")
        with self.assertRaisesRegex(IngressError, "does not match"):
            self.store.commit(session["ingest_id"], "camera/a.mp4")
        self.assertEqual(b"different", final.read_bytes())
        self.assertTrue(Path(session["staging_ref"]).exists())

    def test_storage_commit_rejects_corrupt_staging(self):
        session = self.analyzed()
        Path(session["staging_ref"]).write_bytes(b"tampered")
        with self.assertRaisesRegex(IngressError, "size/hash"):
            self.store.commit(session["ingest_id"], "camera/a.mp4")
        self.assertFalse((self.store.final_root / "camera/a.mp4").exists())

    def test_preservation_bypass_is_explicit_and_recoverable(self):
        session = self.staged()
        committed = self.store.commit(session["ingest_id"], "nvr/clip.mp4", preservation_bypass=True)
        self.assertEqual("committed_analysis_pending", committed["state"])
        self.assertEqual("pending", committed["analysis_status"])
        self.assertTrue(self.store.final_path(session["ingest_id"]).is_file())
        self.store.cleanup_staging(session["ingest_id"])
        self.store.begin_analysis(session["ingest_id"])
        self.assertEqual("analyzing", self.store.get(session["ingest_id"])["state"])

    def test_handoff_outage_retries_from_final_after_staging_cleanup(self):
        session = self.analyzed()
        self.store.commit(session["ingest_id"], "camera/a.mp4")
        self.store.mark_handoff_pending(session["ingest_id"], "weknora unavailable")
        final = self.store.final_path(session["ingest_id"])
        self.store.cleanup_staging(session["ingest_id"])
        recovered = IngressStore(self.root)
        self.assertEqual(b"raw-payload", recovered.final_path(session["ingest_id"]).read_bytes())
        self.assertEqual("handoff_pending", recovered.get(session["ingest_id"])["state"])
        recovered.complete_handoff(session["ingest_id"])
        self.assertTrue(final.exists())
        self.assertEqual("complete", recovered.get(session["ingest_id"])["state"])


    def test_recover_commit_after_conflicting_final_is_cleared_without_fake_handoff(self):
        session = self.analyzed()
        final = self.store.final_root / "camera" / "clip.mp4"
        final.parent.mkdir(parents=True)
        final.write_bytes(b"collision")
        with self.assertRaises(IngressError):
            self.store.commit(session["ingest_id"], "camera/clip.mp4")
        self.assertEqual("committing", self.store.get(session["ingest_id"])["state"])
        self.assertEqual(b"collision", final.read_bytes())
        final.unlink()
        committed = self.store.commit(session["ingest_id"], "camera/clip.mp4")
        self.assertEqual("committed", committed["state"])
        self.assertEqual(b"raw-payload", final.read_bytes())
        self.assertTrue(Path(committed["staging_ref"]).is_file())

if __name__ == "__main__":
    unittest.main()
