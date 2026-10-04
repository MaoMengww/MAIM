-- P2: audit domain removed. Databases that already applied 000_schema.sql keep
-- the audit schema and its tables, so drop them explicitly; fresh databases
-- never create them.
DROP SCHEMA IF EXISTS audit CASCADE;
