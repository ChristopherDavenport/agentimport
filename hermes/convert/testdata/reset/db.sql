-- schema: v31
-- open
-- Resets on one gateway chat: a reset child marked _reset_from, whose
-- parent ended "session_reset", and a legacy one from before the
-- marker, whose parent ended "idle" on the same routing key. Each is a
-- new conversation. The writer stays open, so the rows are read from
-- the write-ahead log. Written by hand.
INSERT INTO sessions (id, source, user_id, session_key, chat_id, model, started_at, ended_at, end_reason) VALUES ('20261008_030000_hh0019', 'discord', 'u1', 'agent:main:discord:dm:42', '42', 'openai/gpt-5.5', 1759892400.0, 1759892500.0, 'idle');
INSERT INTO sessions (id, source, user_id, session_key, chat_id, model, parent_session_id, started_at, ended_at, end_reason) VALUES ('20261008_030200_hh0020', 'discord', 'u1', 'agent:main:discord:dm:42', '42', 'openai/gpt-5.5', '20261008_030000_hh0019', 1759892520.0, 1759892600.0, 'session_reset');
INSERT INTO sessions (id, source, user_id, session_key, chat_id, model, model_config, parent_session_id, started_at) VALUES ('20261008_030400_hh0021', 'discord', 'u1', 'agent:main:discord:dm:42', '42', 'openai/gpt-5.5', '{"_reset_from":"20261008_030200_hh0020"}', '20261008_030200_hh0020', 1759892640.0);
INSERT INTO messages (session_id, role, content, timestamp, finish_reason) VALUES
  ('20261008_030000_hh0019', 'user', 'hello', 1759892401.0, NULL),
  ('20261008_030000_hh0019', 'assistant', 'hi', 1759892402.0, 'stop'),
  ('20261008_030200_hh0020', 'user', 'new topic', 1759892521.0, NULL),
  ('20261008_030200_hh0020', 'assistant', 'sure', 1759892522.0, 'stop'),
  ('20261008_030400_hh0021', 'user', 'fresh start', 1759892641.0, NULL),
  ('20261008_030400_hh0021', 'assistant', 'ready', 1759892642.0, 'stop');
