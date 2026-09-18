-- registrations: chat_jids opted into the scheduled job. A chat_jid is not
-- required to already exist in chats — registration only records intent to
-- include it in the scheduler run, addressed independently of message/chat
-- history. Which messages have already been scanned is tracked per-message
-- via agent_messages.processed, not here — see schemas/01_agent_messages.sql.
-- Applied at startup by internal/registrations.EnsureTable rather than a
-- migration tool. This file documents that schema; keep both in sync.
CREATE TABLE IF NOT EXISTS registrations (
    chat_jid   TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
