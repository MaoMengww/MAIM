-- Bot runtime replicas share one atomic ID source for Neo4j memory records.
CREATE SEQUENCE IF NOT EXISTS bot.memory_id_seq START WITH 1;
