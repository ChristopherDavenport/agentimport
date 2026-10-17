-- schema: v31
-- A Telegram session: the gateway's session_meta row, a group message
-- observed, a hidden reminder, user text sent as api_content beside a
-- memory block the loader strips, a background-review harness prompt
-- and the curator's reply, a stale tool marker, an approval refused,
-- consecutive user turns, a tool output of no call and a call left
-- unanswered at the end. Written by hand.
INSERT INTO sessions (id, source, user_id, session_key, chat_id, chat_type, thread_id, display_name, model, started_at, cwd) VALUES ('20261009_020000_ii0022', 'telegram', 't7', 'agent:main:telegram:group:-100:9', '-100', 'group', '9', 'Team', 'openai/gpt-5.5', 1759975200.0, '/srv');
INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, tool_name, timestamp, finish_reason, platform_message_id, observed, display_kind, display_metadata, api_content) VALUES
  ('20261009_020000_ii0022', 'session_meta', NULL, NULL, NULL, NULL, 1759975200.5, NULL, NULL, 0, NULL, NULL, NULL),
  ('20261009_020000_ii0022', 'user', 'Alice: the build is red', NULL, NULL, NULL, 1759975201.0, NULL, '501', 1, NULL, NULL, NULL),
  ('20261009_020000_ii0022', 'user', 'Bob: <memory-context>Bob likes Go.</memory-context>why is it red?', NULL, NULL, NULL, 1759975202.0, NULL, '502', 0, NULL, NULL, NULL),
  ('20261009_020000_ii0022', 'user', 'Reminder: be brief.', NULL, NULL, NULL, 1759975202.5, NULL, NULL, 0, 'hidden', NULL, NULL),
  ('20261009_020000_ii0022', 'user', 'Carol: fix it', NULL, NULL, NULL, 1759975203.0, NULL, '503', 0, NULL, NULL, '[2026-10-09 02:00] Carol: fix it'),
  ('20261009_020000_ii0022', 'assistant', '[terminal]', '[{"id":"call_t","type":"function","function":{"name":"terminal","arguments":"{\"command\":\"rm -rf build\"}"}}]', NULL, NULL, 1759975204.0, 'tool_calls', NULL, 0, NULL, NULL, NULL),
  ('20261009_020000_ii0022', 'tool', '{"error":"BLOCKED: User denied this command through the selected approval transport. The user has NOT consented to this action. Do NOT retry or attempt the same outcome through another route."}', NULL, 'call_t', 'terminal', 1759975210.0, NULL, NULL, 0, NULL, NULL, NULL),
  ('20261009_020000_ii0022', 'assistant', 'Understood, I will not delete it.', NULL, NULL, NULL, 1759975211.0, 'stop', NULL, 0, NULL, NULL, NULL),
  ('20261009_020000_ii0022', 'user', 'Review the conversation above and consider saving to memory anything useful.', NULL, NULL, NULL, 1759975212.0, NULL, NULL, 0, NULL, NULL, NULL),
  ('20261009_020000_ii0022', 'assistant', 'Saved one memory.', NULL, NULL, NULL, 1759975213.0, 'stop', NULL, 0, NULL, NULL, NULL),
  ('20261009_020000_ii0022', 'tool', 'late output', NULL, 'call_gone', 'terminal', 1759975214.0, NULL, NULL, 0, NULL, NULL, NULL),
  ('20261009_020000_ii0022', 'user', 'Dave: still red', NULL, NULL, NULL, 1759975215.0, NULL, '504', 0, NULL, NULL, NULL),
  ('20261009_020000_ii0022', 'user', 'Dave: hello?', NULL, NULL, NULL, 1759975216.0, NULL, '505', 0, NULL, NULL, NULL),
  ('20261009_020000_ii0022', 'assistant', 'Checking the log.', '[{"id":"call_l","type":"function","function":{"name":"terminal","arguments":"{\"command\":\"cat build.log\"}"}}]', NULL, NULL, 1759975217.0, 'tool_calls', NULL, 0, NULL, NULL, NULL);
