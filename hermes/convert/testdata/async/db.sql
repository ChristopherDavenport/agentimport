-- schema: v31
-- A background delegation: the call's output is Hermes' handle of the
-- run, the child works on after it, and its result arrives later as a
-- hidden user message naming the delegation; async_delegations holds
-- the run. Written by hand.
INSERT INTO sessions (id, source, model, started_at, cwd) VALUES ('20261006_050000_ff0014', 'tui', 'openai/gpt-5.5', 1759726800.0, '/work/a');
INSERT INTO sessions (id, source, model, model_config, parent_session_id, started_at, ended_at, cwd) VALUES ('20261006_050010_ff0015', 'subagent', 'openai/gpt-5.5-mini', '{"_delegate_from":"20261006_050000_ff0014"}', '20261006_050000_ff0014', 1759726810.5, 1759726860.0, '/work/a');
INSERT INTO async_delegations (delegation_id, origin_session, parent_session_id, state, dispatched_at, completed_at, updated_at, delivery_state, origin_session_id) VALUES ('deleg_01', 'agent:main:tui', '20261006_050000_ff0014', 'completed', 1759726810.6, 1759726860.0, 1759726861.0, 'delivered', '20261006_050000_ff0014');
INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, tool_name, display_kind, display_metadata, timestamp, finish_reason) VALUES
  ('20261006_050000_ff0014', 'user', 'Audit the deps in the background.', NULL, NULL, NULL, NULL, NULL, 1759726801.0, NULL),
  ('20261006_050000_ff0014', 'assistant', 'Dispatching an auditor.', '[{"id":"call_bg","type":"function","function":{"name":"delegate_task","arguments":"{\"goal\":\"Audit the deps.\"}"}}]', NULL, NULL, NULL, NULL, 1759726810.0, 'tool_calls'),
  ('20261006_050000_ff0014', 'tool', '{"status":"dispatched","mode":"background","count":1,"delegation_id":"deleg_01","goals":["Audit the deps."],"note":"Running in the background."}', NULL, 'call_bg', 'delegate_task', NULL, NULL, 1759726811.0, NULL),
  ('20261006_050000_ff0014', 'assistant', 'The auditor is running.', NULL, NULL, NULL, NULL, NULL, 1759726812.0, 'stop'),
  ('20261006_050000_ff0014', 'user', '[Async delegation deleg_01 finished] No vulnerable deps.', NULL, NULL, NULL, 'hidden', '{"delegation_id":"deleg_01","status":"completed"}', 1759726861.0, NULL),
  ('20261006_050000_ff0014', 'assistant', 'The audit found nothing.', NULL, NULL, NULL, NULL, NULL, 1759726862.0, 'stop');
INSERT INTO messages (session_id, role, content, timestamp, finish_reason) VALUES
  ('20261006_050010_ff0015', 'user', 'Audit the deps.', 1759726810.7, NULL),
  ('20261006_050010_ff0015', 'assistant', 'No vulnerable deps.', 1759726859.0, 'stop');
