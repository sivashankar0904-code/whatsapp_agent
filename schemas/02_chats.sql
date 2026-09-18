-- chats: known chats (1:1 and groups), addressed by chat_jid the same way
-- agent_messages is. display_name is nullable — it is not carried on
-- inbound message events and is only known once something (a group-info
-- fetch, an explicit upsert) sets it; is_group is known as soon as any
-- message arrives, from the event's IsGroup flag. Applied at startup by
-- internal/chats.EnsureTable rather than a migration tool. This file
-- documents that schema; keep the two in sync.
CREATE TABLE IF NOT EXISTS chats (
    chat_jid     TEXT PRIMARY KEY,
    display_name TEXT,
    is_group     BOOLEAN NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS chats_display_name_idx
    ON chats (display_name);
