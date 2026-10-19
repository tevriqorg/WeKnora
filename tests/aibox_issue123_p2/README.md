P2 Issue #123 public Actions test snapshot

This directory contains the Issue #123 P2 At-rest and Transit source snapshot copied from `tevriqorg/P2-AIBOX` branch `feature/issue-123-raw-once`, current source/test head `46e7525c40286aed278108ff1a6e73a486f058c7`, for execution on public GitHub-hosted Actions. Only the scoped P2 ingress modules, tests, and integration harness are included; no credentials, real data, or deployment configuration are included.

The public workflow runs the exact copied P2 `at_rest.py`, `store.py`, and `tests/g1/test_ingress.py` against temporary simulated files, then exercises actual WeKnora ResourceCatalog, Derived Knowledge, SourceRevision, and DataSourceService code. The integration test writes its receipt only after those WeKnora assertions succeed; the P2 finalizer verifies the receipt before completing and cleaning Transit staging.

The virtual mailbox test uses a test-only `StreamingConnector` with WeKnora's real stream handler, persisted cursor/checkpoint, and ingest path. It does not connect to a mailbox.

By ChatGPT


## Recovery and durability gates

The workflow verifies bounded reads, source-alias protection, path safety, durable staging, retry target pinning, and concurrent commit target claims. It also deliberately tampers with a receipt's Resource path, requires rejection, restores the actual receipt, finalizes the handoff, and replays finalization after cleanup.

P2 Go integration template blob: `c80a1f4d076fc57736f0bf52f50d67d52adb6e43`.

By ChatGPT
