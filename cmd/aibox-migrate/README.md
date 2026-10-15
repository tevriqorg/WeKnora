# AIBOX G7 migration command

One-shot migration/preflight command. It reuses WeKnora PostgreSQL repositories and the existing Temporal Asynq queue; it does not create a migration database or scheduler.

`prepare` bootstraps missing SourceRevision #1, ensures initial active TemporalGraphGeneration per non-temporary KB, and enqueues only current enabled Knowledge lacking ready temporal support.

`preflight` is read-only and exits non-zero while any eligible Knowledge lacks a current revision, matching active generation, or ready temporal support.

The exact Graphiti commit, extraction schema version, and model/config fingerprint are mandatory inputs.
