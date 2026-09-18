-- agent_messages: inbound WhatsApp message log. Lives in the same database
-- as whatsmeow's own whatsmeow_* tables (session and crypto state) but is
-- not one of them — whatsmeow owns and migrates those; this is a separate,
-- independent table for message content, applied at startup by
-- internal/messages.EnsureTable rather than a migration tool. This file
-- documents that schema; keep the two in sync.
CREATE TABLE IF NOT EXISTS agent_messages (
    id          BIGSERIAL PRIMARY KEY,
    chat_jid    TEXT NOT NULL,
    sender_jid  TEXT NOT NULL,
    from_me     BOOLEAN NOT NULL,
    text        TEXT NOT NULL,
    "timestamp" TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS agent_messages_chat_jid_id_idx
    ON agent_messages (chat_jid, id);
