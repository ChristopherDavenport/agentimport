-- schema: v31
-- Columns and values a later Hermes may write: a column of sessions
-- and one of messages this converter does not know, a role it does not
-- know, a content part Open Responses does not define, and a schema
-- version past the one it was written against. Written by hand.
ALTER TABLE sessions ADD COLUMN future_flag INTEGER;
ALTER TABLE messages ADD COLUMN future_note TEXT;
UPDATE schema_version SET version = 35;
INSERT INTO sessions (id, source, model, started_at, future_flag) VALUES ('20261011_000000_ll0026', 'api', 'openai/gpt-5.5', 1760140800.0, 7);
INSERT INTO messages (session_id, role, content, timestamp, future_note, finish_reason) VALUES
  ('20261011_000000_ll0026', 'user', 'hi', 1760140801.0, 'note one', NULL),
  ('20261011_000000_ll0026', 'critic', 'a later role', 1760140801.5, NULL, NULL),
  ('20261011_000000_ll0026', 'assistant', 'hello', 1760140802.0, 'note two', 'stop');
