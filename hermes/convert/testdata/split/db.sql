-- schema: v31
-- A compression that split the session: the parent ended
-- "compression", and its child holds the head it protected, copied,
-- the summary, the tail, copied with its times, and the turns after;
-- the child was compressed again into a grandchild. The child is the
-- one a resume reads. Written by hand.
INSERT INTO sessions (id, source, model, model_config, started_at, ended_at, end_reason, cwd, input_tokens, output_tokens) VALUES ('20261004_070000_dd0004', 'cli', 'anthropic/claude-sonnet-4', '{"reasoning_effort":"high"}', 1759561200.0, 1759561300.0, 'compression', '/work/s', 5000, 300);
INSERT INTO sessions (id, source, model, model_config, parent_session_id, started_at, ended_at, end_reason, cwd, input_tokens, output_tokens) VALUES ('20261004_070140_dd0005', 'cli', 'anthropic/claude-sonnet-4', '{"reasoning_effort":"high"}', '20261004_070000_dd0004', 1759561300.5, 1759561400.0, 'compression', '/work/s', 800, 60);
INSERT INTO sessions (id, source, model, parent_session_id, started_at, cwd) VALUES ('20261004_070320_dd0006', 'cli', 'anthropic/claude-sonnet-4', '20261004_070140_dd0005', 1759561400.5, '/work/s');
INSERT INTO messages (session_id, role, content, timestamp, finish_reason) VALUES
  ('20261004_070000_dd0004', 'user', 'Port the module.', 1759561201.0, NULL),
  ('20261004_070000_dd0004', 'assistant', 'Porting.', 1759561202.0, 'stop'),
  ('20261004_070000_dd0004', 'user', 'Keep going.', 1759561203.0, NULL),
  ('20261004_070000_dd0004', 'assistant', 'Half done.', 1759561204.0, 'stop'),
  ('20261004_070000_dd0004', 'user', 'Finish it.', 1759561205.0, NULL),
  ('20261004_070000_dd0004', 'assistant', 'Finished the port.', 1759561206.0, 'stop');
INSERT INTO messages (session_id, role, content, timestamp, finish_reason, _compressed_summary) VALUES
  ('20261004_070140_dd0005', 'user', 'Port the module.', 1759561201.0, NULL, 0),
  ('20261004_070140_dd0005', 'assistant', 'Porting.', 1759561202.0, 'stop', 0),
  ('20261004_070140_dd0005', 'assistant', '[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted into the summary below. The port is half done.', 1759561300.2, NULL, 1),
  ('20261004_070140_dd0005', 'user', 'Finish it.', 1759561205.0, NULL, 0),
  ('20261004_070140_dd0005', 'assistant', 'Finished the port.', 1759561206.0, 'stop', 0),
  ('20261004_070140_dd0005', 'user', 'Now test it.', 1759561310.0, NULL, 0),
  ('20261004_070140_dd0005', 'assistant', 'Tests pass.', 1759561320.0, 'stop', 0);
INSERT INTO messages (session_id, role, content, timestamp, finish_reason, _compressed_summary) VALUES
  ('20261004_070320_dd0006', 'assistant', '[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted into the summary below. Ported and tested.', 1759561400.2, NULL, 1),
  ('20261004_070320_dd0006', 'user', 'Release it.', 1759561410.0, NULL, 0),
  ('20261004_070320_dd0006', 'assistant', 'Released.', 1759561411.0, 'stop', 0);
