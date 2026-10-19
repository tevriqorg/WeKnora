"""Create fully isolated P2 At-rest and Transit fixtures for Issue #123 CI."""
from __future__ import annotations
import argparse
import sys
import hashlib
import json
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / "p2_source"))

from implementation.ingress.at_rest import describe_external_file
from implementation.ingress.store import IngressStore


def digest(path: Path) -> str:
    h = hashlib.sha256()
    size = 0
    with path.open("rb") as stream:
        while block := stream.read(64 * 1024):
            h.update(block)
            size += len(block)
    return f"sha256:{h.hexdigest()}", size


def prepare(root: Path) -> None:
    root.mkdir(parents=True, exist_ok=True)
    qnap_root = root / "7"
    archive = qnap_root / "qnap" / "archive"
    archive.mkdir(parents=True)
    at_rest = []
    for n in (1, 2, 3):
        path = archive / f"{n}.md"
        path.write_text(f"mock existing archived file {n}\n", encoding="utf-8")
        described = describe_external_file(
            qnap_root,
            f"qnap/archive/{n}.md",
            source_namespace="mock-qnap:fixture",
            provider_root="local://7/",
        )
        at_rest.append({
            "source_key": described.source_key,
            "source_revision_id": described.source_revision_id,
            "resource_path": described.resource_path,
            "relative_path": path.relative_to(root).as_posix(),
            "original_name": path.name,
            "size": described.size,
        })

    transit_root = qnap_root / "transit"
    store = IngressStore(transit_root)
    transit = []
    acquire = root / "acquire"
    acquire.mkdir()
    for n in (3, 4, 5, 6):
        name = f"{n}.mp4"
        source = acquire / name
        source.write_bytes((f"mock transit media {n}\n".encode("utf-8")) * 4096)
        session = store.create(
            "mock-camera", f"asset-{n}", f"mock-camera://asset-{n}",
            mime="video/mp4", target_policy="mock-qnap", analysis_profile="fixture",
            processor_version="issue123-fixture-v1",
        )
        staged = store.stage_file(session["ingest_id"], source, chunk_size=32768)
        store.begin_analysis(session["ingest_id"])
        store.mark_analyzed(session["ingest_id"], f"fixture-analysis://asset-{n}")
        committed = store.commit(session["ingest_id"], name)
        source.unlink()
        store.mark_handoff_pending(session["ingest_id"])
        final_path = Path(committed["final_resource_ref"].removeprefix("file://"))
        resource_path = f"local://7/transit/final/{name}"
        transit.append({
            "source_key": f"mock-camera:asset-{n}",
            "source_revision_id": f"sha256:{staged['content_hash']}",
            "resource_path": resource_path,
            "relative_path": final_path.relative_to(root).as_posix(),
            "original_name": name,
            "size": staged["size"],
            "ingest_id": session["ingest_id"],
            "staging_path": staged["staging_ref"],
        })

    manifest = {"at_rest": at_rest, "transit": transit}
    (root / "fixture.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    final_files = list((transit_root / "final").glob("*"))
    if len(final_files) != 4:
        raise RuntimeError(f"expected 4 unique final raws, got {len(final_files)}")
    for item in transit:
        staging = Path(item["staging_path"])
        if not staging.is_file():
            raise RuntimeError(f"missing staging file before handoff: {staging}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True, type=Path)
    args = parser.parse_args()
    prepare(args.root)


if __name__ == "__main__":
    main()
