-- schema: project v1
-- open
-- A session whose rows sit in the write-ahead log, not yet checkpointed
-- into the database file, as while OpenCode runs: the writer is kept
-- open while the store is read. Written by hand.
PRAGMA wal_checkpoint(TRUNCATE);
INSERT INTO project VALUES ('prj7', '/work/m', NULL, NULL, NULL, NULL, 1760000000000, 1760000000000, NULL, '[]', NULL);
INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, revert, time_created, time_updated) VALUES ('ses_wal', 'prj7', NULL, 'slug', '/work/m', 'In the log', '1.16.0', NULL, 1760002000000, 1760002050000);
INSERT INTO message VALUES ('msg_w1', 'ses_wal', 1760002001000, 1760002001000, '{"role":"user","time":{"created":1760002001000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_w1a', 'msg_w1', 'ses_wal', 1760002001000, 1760002001000, '{"type":"text","text":"only in the log"}');
INSERT INTO message VALUES ('msg_w2', 'ses_wal', 1760002002000, 1760002002000, '{"role":"assistant","parentID":"msg_w1","providerID":"anthropic","modelID":"claude-x","mode":"build","agent":"build","path":{"cwd":"/work/m","root":"/work/m"},"cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1760002002000,"completed":1760002003000},"finish":"stop"}');
INSERT INTO part VALUES ('prt_w2a', 'msg_w2', 'ses_wal', 1760002002000, 1760002002000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_w2b', 'msg_w2', 'ses_wal', 1760002002000, 1760002002000, '{"type":"text","text":"read from the log"}');
INSERT INTO part VALUES ('prt_w2c', 'msg_w2', 'ses_wal', 1760002002000, 1760002002000, '{"type":"step-finish","reason":"stop","cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}}}');
