DROP INDEX IF EXISTS idx_source_revisions_one_current;
DROP INDEX IF EXISTS idx_source_revisions_knowledge;
DROP TABLE IF EXISTS source_revisions;
ALTER TABLE resources DROP CONSTRAINT IF EXISTS resources_ownership_check;
ALTER TABLE resources DROP COLUMN IF EXISTS ownership;
