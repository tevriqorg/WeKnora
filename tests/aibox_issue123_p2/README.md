P2 Issue #123 public Action test snapshot

This directory contains the Issue #123 P2 At-rest and Transit implementation snapshot copied from `tevriqorg/P2-AIBOX` commit `e906b0a2c76f743788f53a64a31fe2cf912b0f1f` for execution in the public WeKnora GitHub Actions workflow. The P2 project owner explicitly authorized placing the relevant Issue #123 code here for testing. Only the isolated At-rest/Transit modules and fixture harness are included; no credentials, real data, deployment configuration, or unrelated P2 code is included.

The public test runs the exact copied `at_rest.py` and `store.py` modules against temporary simulated files, then exercises actual WeKnora ResourceCatalog, Knowledge, SourceRevision, and DataSourceService code. The Go integration test writes a handoff receipt only after verified WeKnora writes succeed; finalization validates that receipt before completing/cleaning Transit staging.

The virtual mailbox is a test-only feed in the Go test and uses the actual WeKnora incremental/cursor and ingest code paths. It does not connect to any mailbox.

By ChatGPT


## Additional P2 G1 acceptance gate

The public Actions job also runs the P2-sourced bounded-stream read-size enforcement, source-alias protection, path-safety, durable-staging, and commit-recovery test cases from the isolated Python fixture. It intentionally excludes state-machine unit cases that manually set handoff success; actual WeKnora handoff remains receipt-gated by the Go integration test above.

P2 test snapshot: branch `feature/issue-123-raw-once`, commit `3afa817d6e6d9769283fc6665325a98905bf43e1`.. The virtual mailbox connector test source matches this UAT branch's Go template blob `ca2712a3aa02940d59682704d1503b7666c7737d`.
