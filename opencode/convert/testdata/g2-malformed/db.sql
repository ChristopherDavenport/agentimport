-- schema: project v1
-- Rows that do not read: data that is not JSON, null, a role that is
-- neither user nor assistant, a number where text is expected, a
-- huge text, unknown part types and tool states, a part of no message
-- of its session, rows of no session, a session row with no id, two
-- sessions each the other's parent, a call ID used twice, an assistant
-- with no parts and a step that never finished. Written by hand.
INSERT INTO project VALUES ('prj7', '/work/m', NULL, NULL, NULL, NULL, 1760000000000, 1760000000000, NULL, '[]', NULL);
INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, revert, time_created, time_updated) VALUES ('ses_bad', 'prj7', NULL, 'slug', '/work/m', 'Malformed', '1.16.0', NULL, 1760004000000, 1760004050000);
INSERT INTO message VALUES ('msg_m1', 'ses_bad', 1760004001000, 1760004001000, '{"role":"user","time":{"created":1760004001000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_m1a', 'msg_m1', 'ses_bad', 1760004001000, 1760004001000, '{"type":"text","text":5}');
INSERT INTO part VALUES ('prt_m1b', 'msg_m1', 'ses_bad', 1760004001000, 1760004001000, json_object('type','text','text',replace(hex(zeroblob(50000)),'00','ab')));
INSERT INTO part VALUES ('prt_m1c', 'msg_m1', 'ses_bad', 1760004001000, 1760004001000, '{"type":"foo","bar":1}');
INSERT INTO part VALUES ('prt_m1d', 'msg_m1', 'ses_bad', 1760004001000, 1760004001000, '{not json');
INSERT INTO part VALUES ('prt_m1e', 'msg_m1', 'ses_bad', 1760004001000, 1760004001000, 'null');
INSERT INTO message VALUES ('msg_m2', 'ses_bad', 1760004001500, 1760004001500, '{oops');
INSERT INTO part VALUES ('prt_m2a', 'msg_m2', 'ses_bad', 1760004001500, 1760004001500, '{"type":"text","text":"part of a broken message"}');
INSERT INTO message VALUES ('msg_m3', 'ses_bad', 1760004001600, 1760004001600, 'null');
INSERT INTO message VALUES ('msg_m4', 'ses_bad', 1760004001700, 1760004001700, '{"role":"system","time":{"created":1760004001700}}');
INSERT INTO part VALUES ('prt_m5a', 'msg_nope', 'ses_bad', 1760004001800, 1760004001800, '{"type":"text","text":"part of no message of the session"}');
INSERT INTO message VALUES ('msg_m6', 'ses_bad', 1760004002000, 1760004002000, '{"role":"assistant","parentID":"msg_m1","providerID":"anthropic","modelID":"claude-x","mode":"build","agent":"build","path":{"cwd":"/work/m","root":"/work/m"},"cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1760004002000,"completed":1760004003000},"finish":"stop"}');
INSERT INTO part VALUES ('prt_m6a', 'msg_m6', 'ses_bad', 1760004002000, 1760004002000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_m6b', 'msg_m6', 'ses_bad', 1760004002000, 1760004002000, '{"type":"tool","callID":"dup","tool":"read","state":{"status":"completed","input":{"filePath":"a"},"output":"a","title":"a","metadata":{},"time":{"start":1760004002001,"end":1760004002002}}}');
INSERT INTO part VALUES ('prt_m6c', 'msg_m6', 'ses_bad', 1760004002000, 1760004002000, '{"type":"tool","callID":"dup","tool":"read","state":{"status":"completed","input":{"filePath":"b"},"output":"b","title":"b","metadata":{},"time":{"start":1760004002003,"end":1760004002004}}}');
INSERT INTO part VALUES ('prt_m6d', 'msg_m6', 'ses_bad', 1760004002000, 1760004002000, '{"type":"tool","callID":"weird","tool":"read","state":{"status":"frozen","input":{}}}');
INSERT INTO part VALUES ('prt_m6e', 'msg_m6', 'ses_bad', 1760004002000, 1760004002000, '{"type":"tool","callID":"nostate","tool":"read","state":null}');
INSERT INTO part VALUES ('prt_m6f', 'msg_m6', 'ses_bad', 1760004002000, 1760004002000, '{"type":"bar"}');
INSERT INTO part VALUES ('prt_m6g', 'msg_m6', 'ses_bad', 1760004002000, 1760004002000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_m6h', 'msg_m6', 'ses_bad', 1760004002000, 1760004002000, '{"type":"text","text":"second step"}');
INSERT INTO part VALUES ('prt_m6i', 'msg_m6', 'ses_bad', 1760004002000, 1760004002000, '{"type":"step-finish","reason":"stop","cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}}}');
INSERT INTO message VALUES ('msg_m7', 'ses_bad', 1760004003000, 1760004003000, '{"role":"user","time":{"created":1760004003000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_m7a', 'msg_m7', 'ses_bad', 1760004003000, 1760004003000, '{"type":"text","text":"again"}');
INSERT INTO message VALUES ('msg_m8', 'ses_bad', 1760004003500, 1760004003500, '{"role":"assistant","parentID":"msg_m7","providerID":"anthropic","modelID":"claude-x","mode":"build","agent":"build","path":{"cwd":"/work/m","root":"/work/m"},"cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1760004003500,"completed":1760004004500},"finish":"stop"}');
INSERT INTO message VALUES ('msg_g1', 'ses_gone', 1760004000000, 1760004000000, '{"role":"user","time":{"created":1760004000000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_g1a', 'msg_g1', 'ses_gone', 1760004000000, 1760004000000, '{"type":"text","text":"of no session"}');
INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, revert, time_created, time_updated) VALUES (NULL, 'prj7', NULL, 'slug', '/work/m', 'No id', '1.16.0', NULL, 1760004000000, 1760004050000);
INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, revert, time_created, time_updated) VALUES ('ses_cyc1', 'prj7', 'ses_cyc2', 'slug', '/work/m', 'Cycle one', '1.16.0', NULL, 1760004000010, 1760004050010);
INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, revert, time_created, time_updated) VALUES ('ses_cyc2', 'prj7', 'ses_cyc1', 'slug', '/work/m', 'Cycle two', '1.16.0', NULL, 1760004000020, 1760004050020);
INSERT INTO message VALUES ('msg_cy1', 'ses_cyc1', 1760004000030, 1760004000030, '{"role":"user","time":{"created":1760004000030},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_cy1a', 'msg_cy1', 'ses_cyc1', 1760004000030, 1760004000030, '{"type":"text","text":"in a cycle"}');
-- A part whose row names another session than its message's: OpenCode
-- reads parts by message, so it is the message's.
INSERT INTO part VALUES ('prt_m7b', 'msg_m7', 'ses_cyc1', 1760004003000, 1760004003000, '{"type":"text","text":"read with its message"}');
