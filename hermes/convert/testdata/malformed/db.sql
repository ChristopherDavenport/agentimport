-- schema: v31
-- Rows that do not read as Hermes writes them: tool_calls that is not
-- JSON and one holding a number among its calls, a reasoning carrier
-- that is not JSON, display metadata under a string layer and display
-- metadata that is garbage, structured content behind a broken prefix,
-- a message with no role, a call ID used twice, a call ID with
-- characters a provider refuses, a start time outside Hermes' window,
-- a huge text, messages of no session, a session with no id, two
-- sessions each the other's parent, and a delegate child whose parent
-- is not in the database. Written by hand.
INSERT INTO sessions (id, source, model, started_at, cwd) VALUES ('20261010_010000_kk0025', 'cli', 'openai/gpt-5.5', 99999999999.0, '/work/m');
INSERT INTO sessions (id, source, model, started_at) VALUES (NULL, 'cli', 'openai/gpt-5.5', 1760058000.0);
INSERT INTO sessions (id, source, model, parent_session_id, started_at) VALUES ('cyc_one', 'cli', 'm', 'cyc_two', 1760058001.0), ('cyc_two', 'cli', 'm', 'cyc_one', 1760058002.0);
INSERT INTO sessions (id, source, model, model_config, parent_session_id, started_at) VALUES ('kid_of_gone', 'subagent', 'm', '{"_delegate_from":"gone_parent"}', 'gone_parent', 1760058003.0);
INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, timestamp, finish_reason, reasoning_details, display_metadata) VALUES
  ('20261010_010000_kk0025', 'user', 'go', NULL, NULL, 1760058010.0, NULL, NULL, '"{\"reactions\":[\"+1\"]}"'),
  ('20261010_010000_kk0025', 'assistant', 'a', '{not json', NULL, 1760058011.0, 'stop', '[{bad', NULL),
  ('20261010_010000_kk0025', 'assistant', 'b', '[7,{"id":"dup","type":"function","function":{"name":"terminal","arguments":{"command":"ls"}}}]', NULL, 1760058012.0, 'tool_calls', NULL, 'garbage'),
  ('20261010_010000_kk0025', 'tool', 'one', NULL, 'dup', 1760058013.0, NULL, NULL, NULL),
  ('20261010_010000_kk0025', 'assistant', '', '[{"id":"dup","type":"function","function":{"name":"terminal","arguments":"{}"}},{"id":"bad id/with:chars","type":"function","function":{"name":"terminal","arguments":"{}"}}]', NULL, 1760058014.0, 'tool_calls', '[7]', NULL),
  ('20261010_010000_kk0025', 'tool', 'two', NULL, 'dup', 1760058015.0, NULL, NULL, NULL),
  ('20261010_010000_kk0025', 'tool', 'three', NULL, 'bad id/with:chars', 1760058016.0, NULL, NULL, NULL),
  ('20261010_010000_kk0025', 'user', char(0) || 'json:[{"type":"text","text":"broken', NULL, NULL, 1760058017.0, NULL, NULL, NULL),
  ('20261010_010000_kk0025', 'user', char(0) || 'json:[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}},{"type":"input_audio","input_audio":{"data":"AAA=","format":"wav"}},7]', NULL, NULL, 1760058018.0, NULL, NULL, NULL),
  ('20261010_010000_kk0025', 'assistant', replace(hex(zeroblob(40000)), '00', 'ab'), NULL, NULL, 1760058019.0, 'stop', NULL, NULL),
  ('20261010_010000_kk0025', '', 'no role', NULL, NULL, 1760058020.0, NULL, NULL, NULL),
  ('20261010_010000_kk0025', 'user', 'end', NULL, NULL, 'not a time', NULL, NULL, NULL),
  ('nobody', 'user', 'of no session', NULL, NULL, 1760058030.0, NULL, NULL, NULL),
  ('cyc_one', 'user', 'in a cycle', NULL, NULL, 1760058040.0, NULL, NULL, NULL),
  ('kid_of_gone', 'user', 'orphaned child', NULL, NULL, 1760058050.0, NULL, NULL, NULL);
