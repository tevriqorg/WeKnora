P2 Issue #123 public Action test snapshot

This directory contains the Issue #123 P2 At-rest and Transit implementation snapshot copied from `tevriqorg/P2-AIBOX` commit `e906b0a2c76f743788f53a64a31fe2cf912b0f1f` for execution in the public WeKnora GitHub Actions workflow. The P2 project owner explicitly authorized placing the relevant Issue #123 code here for testing. Only the isolated At-rest/Transit modules and fixture harness are included; no credentials, real data, deployment configuration, or unrelated P2 code is included.

The public test runs the exact copied `at_rest.py` and `store.py` modules against temporary simulated files, then exercises actual WeKnora ResourceCatalog, Knowledge, SourceRevision, and DataSourceService code. The Go integration test writes a handoff receipt only after verified WeKnora writes succeed; finalization validates that receipt before completing/cleaning Transit staging.

The virtual mailbox is a test-only feed in the Go test and uses the actual WeKnora incremental/cursor and ingest code paths. It does not connect to any mailbox.

By ChatGPT
