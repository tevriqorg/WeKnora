ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS finalizing_batch_id VARCHAR(36) NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS temporal_finalize_receipts (
 batch_id VARCHAR(36) NOT NULL, slot_kind VARCHAR(32) NOT NULL, tenant_id BIGINT NOT NULL,
 knowledge_id VARCHAR(36) NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY (batch_id, slot_kind)
);
CREATE INDEX IF NOT EXISTS idx_temporal_finalize_receipt_knowledge ON temporal_finalize_receipts(knowledge_id);
