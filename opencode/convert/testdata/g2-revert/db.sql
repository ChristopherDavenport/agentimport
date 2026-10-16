-- schema: project v1
-- Staged reverts: one from a message, one from a part of a message.
-- The rows from the revert point on are still on disk until OpenCode's
-- next prompt deletes them. Written by hand.
INSERT INTO project VALUES ('prj7', '/work/m', NULL, NULL, NULL, NULL, 1760000000000, 1760000000000, NULL, '[]', NULL);
INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, revert, time_created, time_updated) VALUES ('ses_revmsg', 'prj7', NULL, 'slug', '/work/m', 'Revert msg', '1.16.0', '{"messageID":"msg_rm3","snapshot":"abc"}', 1760003000000, 1760003050000);
INSERT INTO message VALUES ('msg_rm1', 'ses_revmsg', 1760003001000, 1760003001000, '{"role":"user","time":{"created":1760003001000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_rm1a', 'msg_rm1', 'ses_revmsg', 1760003001000, 1760003001000, '{"type":"text","text":"first"}');
INSERT INTO message VALUES ('msg_rm2', 'ses_revmsg', 1760003002000, 1760003002000, '{"role":"assistant","parentID":"msg_rm1","providerID":"anthropic","modelID":"claude-x","mode":"build","agent":"build","path":{"cwd":"/work/m","root":"/work/m"},"cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1760003002000,"completed":1760003003000},"finish":"stop"}');
INSERT INTO part VALUES ('prt_rm2a', 'msg_rm2', 'ses_revmsg', 1760003002000, 1760003002000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_rm2b', 'msg_rm2', 'ses_revmsg', 1760003002000, 1760003002000, '{"type":"text","text":"answer one"}');
INSERT INTO part VALUES ('prt_rm2c', 'msg_rm2', 'ses_revmsg', 1760003002000, 1760003002000, '{"type":"step-finish","reason":"tool-calls","cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}}}');
INSERT INTO part VALUES ('prt_rm2d', 'msg_rm2', 'ses_revmsg', 1760003002000, 1760003002000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_rm2e', 'msg_rm2', 'ses_revmsg', 1760003002000, 1760003002000, '{"type":"text","text":"answer two"}');
INSERT INTO part VALUES ('prt_rm2f', 'msg_rm2', 'ses_revmsg', 1760003002000, 1760003002000, '{"type":"step-finish","reason":"stop","cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}}}');
INSERT INTO message VALUES ('msg_rm3', 'ses_revmsg', 1760003003000, 1760003003000, '{"role":"user","time":{"created":1760003003000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_rm3a', 'msg_rm3', 'ses_revmsg', 1760003003000, 1760003003000, '{"type":"text","text":"undone ask"}');
INSERT INTO message VALUES ('msg_rm4', 'ses_revmsg', 1760003004000, 1760003004000, '{"role":"assistant","parentID":"msg_rm3","providerID":"anthropic","modelID":"claude-x","mode":"build","agent":"build","path":{"cwd":"/work/m","root":"/work/m"},"cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1760003004000,"completed":1760003005000},"finish":"stop"}');
INSERT INTO part VALUES ('prt_rm4a', 'msg_rm4', 'ses_revmsg', 1760003004000, 1760003004000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_rm4b', 'msg_rm4', 'ses_revmsg', 1760003004000, 1760003004000, '{"type":"text","text":"undone answer"}');
INSERT INTO part VALUES ('prt_rm4c', 'msg_rm4', 'ses_revmsg', 1760003004000, 1760003004000, '{"type":"step-finish","reason":"stop","cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}}}');
INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, revert, time_created, time_updated) VALUES ('ses_revpart', 'prj7', NULL, 'slug', '/work/m', 'Revert art', '1.16.0', '{"messageID":"msg_rp2","partID":"prt_rp2c"}', 1760003100000, 1760003150000);
INSERT INTO message VALUES ('msg_rp1', 'ses_revpart', 1760003101000, 1760003101000, '{"role":"user","time":{"created":1760003101000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_rp1a', 'msg_rp1', 'ses_revpart', 1760003101000, 1760003101000, '{"type":"text","text":"first"}');
INSERT INTO message VALUES ('msg_rp2', 'ses_revpart', 1760003102000, 1760003102000, '{"role":"assistant","parentID":"msg_rp1","providerID":"anthropic","modelID":"claude-x","mode":"build","agent":"build","path":{"cwd":"/work/m","root":"/work/m"},"cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1760003102000,"completed":1760003103000},"finish":"stop"}');
INSERT INTO part VALUES ('prt_rp2a', 'msg_rp2', 'ses_revpart', 1760003102000, 1760003102000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_rp2b', 'msg_rp2', 'ses_revpart', 1760003102000, 1760003102000, '{"type":"text","text":"answer one"}');
INSERT INTO part VALUES ('prt_rp2c', 'msg_rp2', 'ses_revpart', 1760003102000, 1760003102000, '{"type":"step-finish","reason":"tool-calls","cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}}}');
INSERT INTO part VALUES ('prt_rp2d', 'msg_rp2', 'ses_revpart', 1760003102000, 1760003102000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_rp2e', 'msg_rp2', 'ses_revpart', 1760003102000, 1760003102000, '{"type":"text","text":"answer two"}');
INSERT INTO part VALUES ('prt_rp2f', 'msg_rp2', 'ses_revpart', 1760003102000, 1760003102000, '{"type":"step-finish","reason":"stop","cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}}}');
INSERT INTO message VALUES ('msg_rp3', 'ses_revpart', 1760003103000, 1760003103000, '{"role":"user","time":{"created":1760003103000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_rp3a', 'msg_rp3', 'ses_revpart', 1760003103000, 1760003103000, '{"type":"text","text":"undone ask"}');
INSERT INTO message VALUES ('msg_rp4', 'ses_revpart', 1760003104000, 1760003104000, '{"role":"assistant","parentID":"msg_rp3","providerID":"anthropic","modelID":"claude-x","mode":"build","agent":"build","path":{"cwd":"/work/m","root":"/work/m"},"cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1760003104000,"completed":1760003105000},"finish":"stop"}');
INSERT INTO part VALUES ('prt_rp4a', 'msg_rp4', 'ses_revpart', 1760003104000, 1760003104000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_rp4b', 'msg_rp4', 'ses_revpart', 1760003104000, 1760003104000, '{"type":"text","text":"undone answer"}');
INSERT INTO part VALUES ('prt_rp4c', 'msg_rp4', 'ses_revpart', 1760003104000, 1760003104000, '{"type":"step-finish","reason":"stop","cost":0.01,"tokens":{"input":10,"output":10,"reasoning":0,"cache":{"read":0,"write":0}}}');
