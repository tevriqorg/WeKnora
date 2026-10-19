from __future__ import annotations

import hashlib
import json
import os
import shutil
import sqlite3
import tempfile
import time
import uuid
from pathlib import Path
from typing import Any


class IngressError(RuntimeError):
    pass


STATES = {
    "discovered", "acquiring", "staged", "analyzing", "analyzed",
    "committing", "committed", "handoff_pending", "complete",
    "committed_analysis_pending",
}

TRANSITIONS = {
    "discovered": {"acquiring"},
    "acquiring": {"staged"},
    "staged": {"analyzing", "committing"},
    "analyzing": {"analyzed", "committing"},
    "analyzed": {"committing"},
    "committing": {"committed", "committed_analysis_pending"},
    "committed": {"handoff_pending"},
    "committed_analysis_pending": {"analyzing", "handoff_pending"},
    "handoff_pending": {"complete", "analyzing"},
    "complete": set(),
}


class IngressStore:
    """Deterministic Transit reference; not a second Knowledge/workflow ledger."""

    def __init__(self, root: str | Path):
        self.root = Path(root)
        self.root.mkdir(parents=True, exist_ok=True)
        self.staging_root = self.root / "staging"
        self.final_root = self.root / "final"
        self.staging_root.mkdir(exist_ok=True)
        self.final_root.mkdir(exist_ok=True)
        self.db_path = self.root / "ingress.sqlite3"
        self._init_db()

    def _connect(self) -> sqlite3.Connection:
        con = sqlite3.connect(self.db_path)
        con.row_factory = sqlite3.Row
        con.execute("PRAGMA journal_mode=WAL")
        con.execute("PRAGMA synchronous=FULL")
        return con

    def _init_db(self) -> None:
        with self._connect() as con:
            con.executescript("""
            CREATE TABLE IF NOT EXISTS ingest_sessions (
              ingest_id TEXT PRIMARY KEY, mode TEXT NOT NULL CHECK(mode='transit'),
              source_adapter TEXT NOT NULL, source_external_id TEXT NOT NULL,
              source_locator TEXT NOT NULL, observed_at REAL NOT NULL,
              content_hash TEXT NOT NULL DEFAULT '', size INTEGER NOT NULL DEFAULT 0,
              mime TEXT NOT NULL DEFAULT '', staging_ref TEXT NOT NULL DEFAULT '',
              analysis_profile TEXT NOT NULL DEFAULT '', processor_version TEXT NOT NULL DEFAULT '',
              analysis_status TEXT NOT NULL DEFAULT 'pending',
              analysis_manifest_ref TEXT NOT NULL DEFAULT '', target_policy TEXT NOT NULL DEFAULT '',
              final_resource_ref TEXT NOT NULL DEFAULT '', handoff_status TEXT NOT NULL DEFAULT 'pending',
              state TEXT NOT NULL, last_error TEXT NOT NULL DEFAULT '',
              retry_metadata TEXT NOT NULL DEFAULT '{}', updated_at REAL NOT NULL
            );
            CREATE UNIQUE INDEX IF NOT EXISTS ingest_source_identity
              ON ingest_sessions(source_adapter, source_external_id);
            """)

    def get(self, ingest_id: str) -> dict[str, Any]:
        with self._connect() as con:
            row = con.execute("SELECT * FROM ingest_sessions WHERE ingest_id=?", (ingest_id,)).fetchone()
        if not row:
            raise IngressError(f"unknown ingest_id: {ingest_id}")
        result = dict(row)
        result["retry_metadata"] = json.loads(result["retry_metadata"] or "{}")
        return result

    def create(self, source_adapter: str, source_external_id: str, source_locator: str,
               mime: str = "application/octet-stream", target_policy: str = "archive",
               analysis_profile: str = "default", processor_version: str = "test-v1") -> dict[str, Any]:
        now = time.time()
        with self._connect() as con:
            existing = con.execute(
                "SELECT ingest_id FROM ingest_sessions WHERE source_adapter=? AND source_external_id=?",
                (source_adapter, source_external_id),
            ).fetchone()
            if existing:
                return self.get(existing["ingest_id"])
            ingest_id = str(uuid.uuid4())
            con.execute("""INSERT INTO ingest_sessions(
              ingest_id,mode,source_adapter,source_external_id,source_locator,observed_at,mime,
              analysis_profile,processor_version,target_policy,state,updated_at,retry_metadata)
              VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)""",
              (ingest_id, "transit", source_adapter, source_external_id, source_locator, now, mime,
               analysis_profile, processor_version, target_policy, "discovered", now, "{}"))
        return self.get(ingest_id)

    def _transition(self, ingest_id: str, target: str, **values: Any) -> None:
        current = self.get(ingest_id)
        if target not in STATES or (target != current["state"] and target not in TRANSITIONS[current["state"]]):
            raise IngressError(f"invalid transition {current['state']} -> {target}")
        values.update(state=target, updated_at=time.time())
        assignments = ",".join(f"{key}=?" for key in values)
        with self._connect() as con:
            con.execute(f"UPDATE ingest_sessions SET {assignments} WHERE ingest_id=?", (*values.values(), ingest_id))

    def stage_bytes(self, ingest_id: str, payload: bytes) -> dict[str, Any]:
        session = self.get(ingest_id)
        if session["state"] == "discovered":
            self._transition(ingest_id, "acquiring")
        elif session["state"] != "acquiring":
            raise IngressError("stage_bytes requires acquiring state")
        digest = hashlib.sha256(payload).hexdigest()
        staging = self.staging_root / f"{ingest_id}.part"
        with open(staging, "wb") as fh:
            fh.write(payload)
            fh.flush()
            os.fsync(fh.fileno())
        self._transition(ingest_id, "staged", staging_ref=str(staging),
                         content_hash=digest, size=len(payload), last_error="")
        return self.get(ingest_id)

    def stage_file(self, ingest_id: str, source: str | Path,
                   chunk_size: int = 1024 * 1024) -> dict[str, Any]:
        """Stage a source file with bounded memory; never mutate the source.

        This is the file-backed Transit path; stage_bytes is for small fixtures.
        The .part remains the only durable staging copy until verified commit.
        """
        if chunk_size <= 0:
            raise ValueError("chunk_size must be positive")
        source = Path(source)
        if not source.is_file():
            raise IngressError("source file is unavailable")
        session = self.get(ingest_id)
        if session["state"] == "discovered":
            self._transition(ingest_id, "acquiring")
        elif session["state"] != "acquiring":
            raise IngressError("stage_file requires acquiring state")
        staging = self.staging_root / f"{ingest_id}.part"
        digest = hashlib.sha256()
        size = 0
        with open(source, "rb") as src, open(staging, "wb") as dst:
            while chunk := src.read(chunk_size):
                dst.write(chunk)
                digest.update(chunk)
                size += len(chunk)
            dst.flush()
            os.fsync(dst.fileno())
        self._transition(ingest_id, "staged", staging_ref=str(staging),
                         content_hash=digest.hexdigest(), size=size, last_error="")
        return self.get(ingest_id)

    def begin_analysis(self, ingest_id: str) -> None:
        self._transition(ingest_id, "analyzing", analysis_status="running")

    def mark_analyzed(self, ingest_id: str, manifest_ref: str) -> None:
        if not manifest_ref:
            raise IngressError("durable analysis manifest is required")
        self._transition(ingest_id, "analyzed", analysis_status="complete",
                         analysis_manifest_ref=manifest_ref, last_error="")

    @staticmethod
    def _verify(path: Path, expected_hash: str, expected_size: int) -> bool:
        if not path.is_file() or path.stat().st_size != expected_size:
            return False
        digest = hashlib.sha256()
        with path.open("rb") as stream:
            while chunk := stream.read(1024 * 1024):
                digest.update(chunk)
        return digest.hexdigest() == expected_hash

    def commit(self, ingest_id: str, final_name: str, preservation_bypass: bool = False) -> dict[str, Any]:
        session = self.get(ingest_id)
        # "committing" is a durable retry point: a crash after writing the
        # verified final but before ack must not strand the ingest session.
        allowed = session["state"] in {"analyzed", "committing"} or (
            preservation_bypass and session["state"] in {"staged", "analyzing"}
        )
        if session["state"] == "committing" and session["analysis_status"] != "complete" and not preservation_bypass:
            raise IngressError("resuming a preservation-bypass commit requires explicit bypass")
        if not allowed:
            raise IngressError("commit requires analyzed state or explicit preservation bypass")
        staging = Path(session["staging_ref"])
        if not self._verify(staging, session["content_hash"], session["size"]):
            raise IngressError("staging object failed size/hash verification")
        candidate = Path(final_name)
        if not final_name or candidate.is_absolute() or ".." in candidate.parts:
            raise IngressError("unsafe archive target")
        final = self.final_root / candidate
        if not final.resolve().is_relative_to(self.final_root.resolve()):
            raise IngressError("final target escapes archive root")
        final.parent.mkdir(parents=True, exist_ok=True)
        self._transition(ingest_id, "committing")
        if final.exists():
            if not self._verify(final, session["content_hash"], session["size"]):
                raise IngressError("existing final object does not match staged source")
        else:
            fd, tmp_name = tempfile.mkstemp(prefix=".commit-", dir=final.parent)
            os.close(fd)
            tmp = Path(tmp_name)
            try:
                shutil.copyfile(staging, tmp)
                if not self._verify(tmp, session["content_hash"], session["size"]):
                    raise IngressError("final object failed size/hash verification")
                os.replace(tmp, final)
            finally:
                tmp.unlink(missing_ok=True)
        state = "committed_analysis_pending" if preservation_bypass else "committed"
        status = "pending" if preservation_bypass else session["analysis_status"]
        self._transition(ingest_id, state, final_resource_ref=final.as_uri(),
                         analysis_status=status, last_error="")
        return self.get(ingest_id)

    def mark_handoff_pending(self, ingest_id: str, error: str = "") -> None:
        session = self.get(ingest_id)
        if not session["final_resource_ref"]:
            raise IngressError("handoff requires final resource")
        self._transition(ingest_id, "handoff_pending", handoff_status="pending", last_error=error)

    def complete_handoff(self, ingest_id: str) -> None:
        self._transition(ingest_id, "complete", handoff_status="complete", last_error="")

    def can_cleanup_staging(self, ingest_id: str) -> bool:
        session = self.get(ingest_id)
        ref = session["final_resource_ref"]
        if not ref.startswith("file://"):
            return False
        final = Path(ref.removeprefix("file://"))
        if not self._verify(final, session["content_hash"], session["size"]):
            return False
        if session["analysis_manifest_ref"]:
            return True
        return session["state"] in {"committed_analysis_pending", "handoff_pending", "complete"} \
            and session["analysis_status"] == "pending"

    def cleanup_staging(self, ingest_id: str) -> None:
        if not self.can_cleanup_staging(ingest_id):
            raise IngressError("unsafe staging cleanup")
        session = self.get(ingest_id)
        if session["staging_ref"]:
            Path(session["staging_ref"]).unlink(missing_ok=True)
        with self._connect() as con:
            con.execute("UPDATE ingest_sessions SET staging_ref='',updated_at=? WHERE ingest_id=?",
                        (time.time(), ingest_id))

    def final_path(self, ingest_id: str) -> Path:
        ref = self.get(ingest_id)["final_resource_ref"]
        if not ref.startswith("file://"):
            raise IngressError("final resource is not established")
        return Path(ref.removeprefix("file://"))
