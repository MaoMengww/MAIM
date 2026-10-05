-- Each user owns one system conversation, including during concurrent broadcasts.
CREATE UNIQUE INDEX IF NOT EXISTS uq_conversations_system_owner
    ON messaging.conversations(owner_id) WHERE type = 3;
