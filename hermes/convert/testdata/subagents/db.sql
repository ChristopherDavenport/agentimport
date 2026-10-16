-- schema: v31
-- Foreground delegation: one delegate_task call with two tasks ran two
-- children, and a row with two calls ran one child each, told apart by
-- the goal each child was sent first; a sub-agent of the first child;
-- and a sub-agent no call was running for. The children are tied to
-- their calls by when they started. Written by hand.
INSERT INTO sessions (id, source, model, started_at, cwd, estimated_cost_usd) VALUES ('20261005_060000_ee0007', 'cli', 'openai/gpt-5.5', 1759644000.0, '/work/d', 0.5);
INSERT INTO sessions (id, source, model, model_config, parent_session_id, started_at, ended_at, cwd) VALUES
  ('20261005_060010_ee0008', 'subagent', 'openai/gpt-5.5-mini', '{"_delegate_from":"20261005_060000_ee0007"}', '20261005_060000_ee0007', 1759644010.5, 1759644020.0, '/work/d'),
  ('20261005_060010_ee0009', 'subagent', 'openai/gpt-5.5-mini', '{"_delegate_from":"20261005_060000_ee0007"}', '20261005_060000_ee0007', 1759644010.6, 1759644021.0, '/work/d'),
  ('20261005_060012_ee0010', 'subagent', 'openai/gpt-5.5-mini', '{"_delegate_from":"20261005_060010_ee0008"}', '20261005_060010_ee0008', 1759644012.0, 1759644015.0, '/work/d'),
  ('20261005_060040_ee0011', 'subagent', 'openai/gpt-5.5-mini', '{"_delegate_from":"20261005_060000_ee0007"}', '20261005_060000_ee0007', 1759644040.5, 1759644045.0, '/work/d'),
  ('20261005_060040_ee0012', 'subagent', 'openai/gpt-5.5-mini', '{"_delegate_from":"20261005_060000_ee0007"}', '20261005_060000_ee0007', 1759644040.6, 1759644046.0, '/work/d'),
  ('20261005_060100_ee0013', 'subagent', 'openai/gpt-5.5-mini', '{"_delegate_from":"20261005_060000_ee0007"}', '20261005_060000_ee0007', 1759644100.0, 1759644101.0, '/work/d');
INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, tool_name, timestamp, finish_reason) VALUES
  ('20261005_060000_ee0007', 'user', 'Survey the repo.', NULL, NULL, NULL, 1759644001.0, NULL),
  ('20261005_060000_ee0007', 'assistant', '', '[{"id":"call_batch","type":"function","function":{"name":"delegate_task","arguments":"{\"tasks\":[{\"goal\":\"List the Go files.\"},{\"goal\":\"List the tests.\"}]}"}}]', NULL, NULL, 1759644010.0, 'tool_calls'),
  ('20261005_060000_ee0007', 'tool', '{"results":[{"task_index":0,"status":"completed","summary":"12 files"},{"task_index":1,"status":"completed","summary":"3 tests"}]}', NULL, 'call_batch', 'delegate_task', 1759644022.0, NULL),
  ('20261005_060000_ee0007', 'assistant', '', '[{"id":"call_x","type":"function","function":{"name":"delegate_task","arguments":"{\"goal\":\"Read main.go.\"}"}},{"id":"call_y","type":"function","function":{"name":"delegate_task","arguments":"{\"goal\":\"Read go.mod.\"}"}}]', NULL, NULL, 1759644040.0, 'tool_calls'),
  ('20261005_060000_ee0007', 'tool', '{"results":[{"task_index":0,"status":"completed","summary":"main reads flags"}]}', NULL, 'call_x', 'delegate_task', 1759644047.0, NULL),
  ('20261005_060000_ee0007', 'tool', '{"results":[{"task_index":0,"status":"completed","summary":"go 1.25"}]}', NULL, 'call_y', 'delegate_task', 1759644047.5, NULL),
  ('20261005_060000_ee0007', 'assistant', 'Twelve files, three tests, flags, Go 1.25.', NULL, NULL, NULL, 1759644050.0, 'stop');
INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, timestamp, finish_reason) VALUES
  ('20261005_060010_ee0008', 'user', 'List the Go files.', NULL, NULL, 1759644010.7, NULL),
  ('20261005_060010_ee0008', 'assistant', '', '[{"id":"call_n","type":"function","function":{"name":"delegate_task","arguments":"{\"goal\":\"Count lines.\"}"}}]', NULL, 1759644011.0, 'tool_calls'),
  ('20261005_060010_ee0008', 'tool', '{"results":[{"task_index":0,"status":"completed","summary":"900 lines"}]}', NULL, 'call_n', 1759644016.0, NULL),
  ('20261005_060010_ee0008', 'assistant', '12 files', NULL, NULL, 1759644019.0, 'stop'),
  ('20261005_060010_ee0009', 'user', 'List the tests.', NULL, NULL, 1759644010.8, NULL),
  ('20261005_060010_ee0009', 'assistant', '3 tests', NULL, NULL, 1759644020.5, 'stop'),
  ('20261005_060012_ee0010', 'user', 'Count lines.', NULL, NULL, 1759644012.1, NULL),
  ('20261005_060012_ee0010', 'assistant', '900 lines', NULL, NULL, 1759644014.0, 'stop'),
  ('20261005_060040_ee0011', 'user', 'Read go.mod.', NULL, NULL, 1759644040.7, NULL),
  ('20261005_060040_ee0011', 'assistant', 'go 1.25', NULL, NULL, 1759644044.0, 'stop'),
  ('20261005_060040_ee0012', 'user', 'Read main.go.', NULL, NULL, 1759644040.8, NULL),
  ('20261005_060040_ee0012', 'assistant', 'main reads flags', NULL, NULL, 1759644045.5, 'stop'),
  ('20261005_060100_ee0013', 'user', 'Stray task.', NULL, NULL, 1759644100.1, NULL),
  ('20261005_060100_ee0013', 'assistant', 'done', NULL, NULL, 1759644100.5, 'stop');
