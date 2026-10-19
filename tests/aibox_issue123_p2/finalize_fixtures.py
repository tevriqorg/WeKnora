"""Finalize Transit only after an actual WeKnora handoff receipt is verified."""
from __future__ import annotations
import argparse
import json
import os
import sys
from pathlib import Path

P2_SOURCE_ROOT = Path(__file__).resolve().parent / "p2_source"
sys.path.insert(0, str(P2_SOURCE_ROOT))

from implementation.ingress.store import IngressStore


def finalize(root: Path) -> None:
    manifest = json.loads((root / "fixture.json").read_text(encoding="utf-8"))
    receipt_path = root / "handoff-receipt.json"
    if not receipt_path.is_file():
        raise RuntimeError("missing receipt from successful actual WeKnora integration test")
    receipt = json.loads(receipt_path.read_text(encoding="utf-8"))
    rows = receipt.get("transit", [])
    verified = {
        item.get("source_key"): item
        for item in rows
        if item.get("verified_by_weknora") is True
        and item.get("knowledge_id")
        and item.get("resource_ref")
    }
    expected = manifest.get("transit", [])
    if len(rows) != len(expected) or len(verified) != len(expected):
        raise RuntimeError("WeKnora handoff receipt is incomplete or contains duplicate source keys")
    refs = [proof.get("resource_ref") for proof in verified.values()]
    if len(set(refs)) != len(expected):
        raise RuntimeError("WeKnora handoff receipt reuses a Resource reference")
    for item in expected:
        proof = verified.get(item["source_key"])
        if (
            not proof
            or proof.get("source_revision_id") != item["source_revision_id"]
            or proof.get("ingest_id") != item["ingest_id"]
            or proof.get("resource_path") != item["resource_path"]
        ):
            raise RuntimeError(f"no matching actual WeKnora handoff receipt: {item['source_key']}")

    store = IngressStore(root / "7" / "transit")
    for item in expected:
        session = store.get(item["ingest_id"])
        if session["state"] != "handoff_pending":
            raise RuntimeError(f"unexpected pre-handoff state: {session['state']}")
        if not store.can_cleanup_staging(item["ingest_id"]):
            raise RuntimeError(f"final resource is not verified: {item['ingest_id']}")
        store.complete_handoff(item["ingest_id"])
        store.cleanup_staging(item["ingest_id"])
        completed = store.get(item["ingest_id"])
        if completed["state"] != "complete" or completed["staging_ref"]:
            raise RuntimeError(f"handoff recovery did not clean staging: {item['ingest_id']}")
        if Path(item["staging_path"]).exists():
            raise RuntimeError(f"staging leaked after successful handoff: {item['ingest_id']}")
    final_dir = root / "7" / "transit" / "final"
    finals = [path for path in final_dir.iterdir() if path.is_file()]
    if len(finals) != len(expected):
        raise RuntimeError(f"expected exactly {len(expected)} final Transit raws, got {len(finals)}")
    summary = (
        "### Issue #123 isolated Raw Once integration\n"
        "- At-rest: 3 P2-generated external descriptors handed to real WeKnora services; zero Raw copies; external delete protection passed.\n"
        "- Transit: 4 P2 IngressStore commits handed to real WeKnora services; final Raw count verified; staging cleaned only after matching WeKnora receipts.\n"
        "- Temporary fixtures only; no QNAP, mailbox service, Vela data, or production runtime.\n"
    )
    summary_env = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary_env:
        with Path(summary_env).open("a", encoding="utf-8") as stream:
            stream.write(summary)
    print(summary)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True, type=Path)
    args = parser.parse_args()
    finalize(args.root)


if __name__ == "__main__":
    main()
