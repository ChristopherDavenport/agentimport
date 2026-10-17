-- schema: v31
-- A /branch copy: the child names the parent in _branched_from and
-- starts with the parent's live rows, cloned with their times, then
-- goes its own way; a legacy branch from before the marker follows a
-- parent ended "branched". Written by hand.
INSERT INTO sessions (id, source, model, started_at, ended_at, end_reason, cwd, title) VALUES ('20261007_040000_gg0016', 'tui', 'openai/gpt-5.5', 1759809600.0, 1759809700.0, 'branched', '/work/b', 'Plan');
INSERT INTO sessions (id, source, model, model_config, parent_session_id, started_at, cwd, title) VALUES ('20261007_040140_gg0017', 'tui', 'openai/gpt-5.5', '{"_branched_from":"20261007_040000_gg0016"}', '20261007_040000_gg0016', 1759809700.5, '/work/b', 'Plan #2');
INSERT INTO sessions (id, source, model, parent_session_id, started_at, cwd) VALUES ('20261007_040200_gg0018', 'tui', 'openai/gpt-5.5', '20261007_040000_gg0016', 1759809720.0, '/work/b');
INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, timestamp, finish_reason) VALUES
  ('20261007_040000_gg0016', 'user', 'Plan the release.', NULL, NULL, 1759809601.0, NULL),
  ('20261007_040000_gg0016', 'assistant', '', '[{"id":"call_p","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"CHANGELOG.md\"}"}}]', NULL, 1759809602.0, 'tool_calls'),
  ('20261007_040000_gg0016', 'tool', 'v1.0', NULL, 'call_p', 1759809603.0, NULL),
  ('20261007_040000_gg0016', 'assistant', 'Release v1.1 next.', NULL, NULL, 1759809604.0, 'stop'),
  ('20261007_040000_gg0016', 'user', 'Make it v2.', NULL, NULL, 1759809605.0, NULL),
  ('20261007_040000_gg0016', 'assistant', 'Planning v2.', NULL, NULL, 1759809606.0, 'stop');
INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, timestamp, finish_reason) VALUES
  ('20261007_040140_gg0017', 'user', 'Plan the release.', NULL, NULL, 1759809601.0, NULL),
  ('20261007_040140_gg0017', 'assistant', '', '[{"id":"call_p","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"CHANGELOG.md\"}"}}]', NULL, 1759809602.0, 'tool_calls'),
  ('20261007_040140_gg0017', 'tool', 'v1.0', NULL, 'call_p', 1759809603.0, NULL),
  ('20261007_040140_gg0017', 'assistant', 'Release v1.1 next.', NULL, NULL, 1759809604.0, 'stop'),
  ('20261007_040140_gg0017', 'user', 'Make it v1.2 instead.', NULL, NULL, 1759809710.0, NULL),
  ('20261007_040140_gg0017', 'assistant', 'Planning v1.2.', NULL, NULL, 1759809711.0, 'stop'),
  ('20261007_040200_gg0018', 'user', 'Plan the release.', NULL, NULL, 1759809601.0, NULL),
  ('20261007_040200_gg0018', 'assistant', 'Legacy branch reply.', NULL, NULL, 1759809721.0, 'stop');
