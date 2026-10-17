-- An older Hermes state.db, at schema version 14, its columns a subset
-- of SCHEMA_SQL in hermes_state_common.py (NousResearch/hermes-agent
-- 848fc0ad8b, MIT License, Copyright (c) 2025 Nous Research; see
-- THIRD_PARTY_NOTICES.md): before the
-- delegate marker (v16), per-model usage (v20), system_prompts (v25),
-- async_delegations and in-place compaction's flags. Written by hand
-- from the migrations in hermes_state_schema.py, which add columns by
-- reconciling the live tables against SCHEMA_SQL; the columns of this
-- version were not read from a release of that age.
CREATE TABLE schema_version (version INTEGER NOT NULL);
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    user_id TEXT,
    model TEXT,
    model_config TEXT,
    system_prompt TEXT,
    parent_session_id TEXT,
    started_at REAL NOT NULL,
    ended_at REAL,
    end_reason TEXT,
    message_count INTEGER DEFAULT 0,
    tool_call_count INTEGER DEFAULT 0,
    input_tokens INTEGER DEFAULT 0,
    output_tokens INTEGER DEFAULT 0,
    title TEXT
);
CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    role TEXT NOT NULL,
    content TEXT,
    tool_call_id TEXT,
    tool_calls TEXT,
    tool_name TEXT,
    timestamp REAL NOT NULL,
    token_count INTEGER,
    finish_reason TEXT,
    reasoning TEXT
);
INSERT INTO schema_version VALUES (14);
