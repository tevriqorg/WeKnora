P2 Issue #123 public Action test snapshot

This directory contains the Issue #123 P2 At-rest and Transit implementation snapshot copied from `tevriqorg/P2-AIBOX` branch `feature/issue-123-raw-once`, source head `e77beba3035ca11922ca2db9b9aa74ecd82482e6`, for execution in the public WeKnora GitHub Actions workflow. The P2 project owner explicitly authorized placing the relevant Issue #123 code here for testing. Only the isolated At-rest/Transit modules and fixture harness are included; no credentials, real data, deployment configuration, or unrelated P2 code is included.

The public test runs the exact copied `at_rest.py` and `store.py` modules against temporary simulated files, then exercises actual WeKnora ResourceCatalog, Knowledge, SourceRevision, and DataSourceService code. The Go integration test writes a handoff receipt only after verified WeKnora writes succeed; finalization validates that receipt before completing/cleaning Transit staging.

The virtual mailbox is a test-only feed in the Go test and uses the actual WeKnora incremental/cursor and ingest code paths. It does not connect to any mailbox.

By ChatGPT


## Additional P2 G1 acceptance gates

The public Actions job also runs the P2-sourced bounded-stream read-size enforcement, source-alias protection, path-safety, durable-staging, and commit-recovery test cases from the isolated Python fixture. The commit-recovery regression now verifies a retry cannot switch the originally persisted final archive target.

The workflow intentionally excludes state-machine unit cases that manually set handoff success; actual WeKnora handoff remains receipt-gated by the Go integration test above. The finalizer is replayed after successful cleanup to verify it resumes safely when sessions are already complete.

P2 Go integration test template blob: `c80a1f4d076fc57736f0bf52f50d67d52adb6e43`.

By ChatGPT
