-- Ingestion replicas allocate chunk IDs independently of process-local clocks.
CREATE SEQUENCE IF NOT EXISTS knowledge.ingest_chunk_ids START WITH 1;
