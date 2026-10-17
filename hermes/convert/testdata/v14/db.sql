-- schema: v14
-- A database at schema version 14: the system prompt in the session
-- row, no in-place compaction flags, a sub-agent told by its source
-- alone, before the delegate marker, and a compression continuation
-- whose summary is told by its text, as no row carries
-- _compressed_summary. Written by hand.
INSERT INTO sessions (id, source, model, system_prompt, started_at, ended_at, end_reason, input_tokens, output_tokens) VALUES ('20250601_100000_jj0023', 'cli', 'anthropic/claude-3.7-sonnet', 'You are Hermes.', 1748772000.0, 1748772099.0, 'compression', 400, 40);
INSERT INTO sessions (id, source, model, system_prompt, parent_session_id, started_at) VALUES ('20250601_100140_jj0025', 'cli', 'anthropic/claude-3.7-sonnet', 'You are Hermes.', '20250601_100000_jj0023', 1748772099.5);
INSERT INTO sessions (id, source, model, parent_session_id, started_at) VALUES ('20250601_100010_jj0024', 'subagent', 'anthropic/claude-3.7-sonnet', '20250601_100000_jj0023', 1748772010.5);
INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, tool_name, timestamp, finish_reason, reasoning) VALUES
  ('20250601_100000_jj0023', 'user', 'Summarise the project.', NULL, NULL, NULL, 1748772001.0, NULL, NULL),
  ('20250601_100000_jj0023', 'assistant', '', '[{"id":"toolu_d","type":"function","function":{"name":"delegate_task","arguments":"{\"goal\":\"Read the docs.\"}"}}]', NULL, NULL, 1748772010.0, 'tool_calls', 'I should delegate.'),
  ('20250601_100000_jj0023', 'tool', '{"results":[{"task_index":0,"status":"completed","summary":"docs read"}]}', NULL, 'toolu_d', 'delegate_task', 1748772020.0, NULL, NULL),
  ('20250601_100000_jj0023', 'assistant', 'It is a parser.', NULL, NULL, NULL, 1748772021.0, 'stop', NULL),
  ('20250601_100140_jj0025', 'user', '[CONTEXT SUMMARY]: the project is a parser.', NULL, NULL, NULL, 1748772100.0, NULL, NULL),
  ('20250601_100140_jj0025', 'user', 'Thanks.', NULL, NULL, NULL, 1748772101.0, NULL, NULL),
  ('20250601_100140_jj0025', 'assistant', 'You are welcome.', NULL, NULL, NULL, 1748772102.0, 'length', NULL),
  ('20250601_100010_jj0024', 'user', 'Read the docs.', NULL, NULL, NULL, 1748772010.6, NULL, NULL),
  ('20250601_100010_jj0024', 'assistant', 'docs read', NULL, NULL, NULL, 1748772019.0, 'stop', NULL);
