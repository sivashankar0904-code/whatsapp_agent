-- agent_messages: inbound WhatsApp message log. Lives in the same database
-- as whatsmeow's own whatsmeow_* tables (session and crypto state) but is
-- not one of them — whatsmeow owns and migrates those; this is a separate,
-- independent table for message content, applied at startup by
-- internal/messages.EnsureTable rather than a migration tool. This file
-- documents that schema; keep the two in sync.
--
-- processed marks whether the scheduler (internal/scheduler) has already
-- scanned this message for YouTube links — set true once scanned,
-- regardless of whether a link was found, so nothing is rescanned. This is
-- a flag on the message itself rather than a per-chat cursor, so it's not
-- affected by the message being read/seen through any other means (e.g.
-- GET /messages).
CREATE TABLE IF NOT EXISTS agent_messages (
    id          BIGSERIAL PRIMARY KEY,
    chat_jid    TEXT NOT NULL,
    sender_jid  TEXT NOT NULL,
    from_me     BOOLEAN NOT NULL,
    text        TEXT NOT NULL,
    processed   BOOLEAN NOT NULL DEFAULT false,
    "timestamp" TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backfills processed onto an agent_messages table created before this
-- column existed (a no-op on a fresh table, already created with it above).
-- Runs before the partial index below, which references the column.
ALTER TABLE agent_messages
    ADD COLUMN IF NOT EXISTS processed BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS agent_messages_chat_jid_id_idx
    ON agent_messages (chat_jid, id);

CREATE INDEX IF NOT EXISTS agent_messages_unprocessed_idx
    ON agent_messages (chat_jid, id) WHERE NOT processed;
