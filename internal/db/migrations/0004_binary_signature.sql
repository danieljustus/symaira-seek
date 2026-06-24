-- Add binary_signature column for two-stage Hamming pre-filter.
-- The column is nullable so existing rows remain valid; new inserts
-- backfill the signature via SignBinarySignature.
ALTER TABLE chunks ADD COLUMN binary_signature BLOB;
