"""Read-only external source discovery; no archive copy and no second ledger.

Returned physical paths are intended for WeKnora ResourceCatalog.Register with
ownership=external after the caller has configured the same read-only mount.
They are NOT written to or uploaded by this adapter.
"""
from __future__ import annotations

from dataclasses import dataclass
import hashlib
from pathlib import Path


@dataclass(frozen=True)
class ExternalFile:
    source_key: str
    source_revision_id: str
    resource_path: str
    size: int
    ownership: str = "external"


def describe_external_file(
    mounted_root: str | Path, relative_path: str | Path, *,
    source_namespace: str, provider_root: str,
    chunk_size: int = 1024 * 1024,
) -> ExternalFile:
    """Describe a stable file without copying bytes or creating any local state."""
    if not source_namespace or not provider_root.endswith("/"):
        raise ValueError("source namespace and provider_root ending in / are required")
    if chunk_size <= 0:
        raise ValueError("chunk_size must be positive")
    root = Path(mounted_root).resolve(strict=True)
    relative = Path(relative_path)
    if relative.is_absolute() or not relative.parts or ".." in relative.parts:
        raise ValueError("relative source path must remain beneath mounted_root")
    path = (root / relative).resolve(strict=True)
    if not path.is_relative_to(root) or not path.is_file():
        raise ValueError("external source escapes the authorized mounted root or is not a file")
    digest = hashlib.sha256()
    size = 0
    with path.open("rb") as stream:
        while block := stream.read(chunk_size):
            digest.update(block)
            size += len(block)
    identity = relative.as_posix()
    return ExternalFile(
        source_key=f"{source_namespace}:{identity}",
        source_revision_id="sha256:" + digest.hexdigest(),
        resource_path=provider_root + relative.as_posix(),
        size=size,
    )
