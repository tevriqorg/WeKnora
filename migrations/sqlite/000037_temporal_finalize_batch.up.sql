ALTER TABLE knowledges ADD COLUMN finalizing_batch_id TEXT NOT NULL DEFAULT '';
CREATE TABLE temporal_finalize_receipts (
 batch_id TEXT NOT NULL, slot_kind TEXT NOT NULL, tenant_id INTEGER NOT NULL,
 knowledge_id TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY (batch_id, slot_kind)
);
CREATE INDEX idx_temporal_finalize_receipt_knowledge ON temporal_finalize_receipts(knowledge_id);
