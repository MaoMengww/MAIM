-- Fix balance precision: decimal(12,2) rounds small per-request deductions (e.g. ¥0.004255) to zero.
-- Why: The balance column was originally decimal(12,2), which only has 2 decimal places.
-- When deducting small amounts from LLM usage (typically ¥0.001–¥0.01 per call),
-- PostgreSQL rounds the result to 2 decimal places, making tiny deductions invisible.
-- Changing to decimal(12,6) gives enough precision for micro-deductions.

ALTER TABLE "user".users ALTER COLUMN balance TYPE DECIMAL(12,6);
