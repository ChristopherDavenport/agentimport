-- schema: project v1
-- A session of OpenCode 1.3.10, whose output tokens count reasoning:
-- reasoning of each provider's metadata, Anthropic's redacted thinking,
-- OpenAI's encrypted reasoning split over two parts, Gemini's thought
-- signatures on text and on a call, a hosted tool, media in a tool
-- result, Anthropic reasoning with no signature, which is not sent back,
-- and the model switched between them. Written by hand.
INSERT INTO project VALUES ('prj2', '/work/r', NULL, NULL, NULL, NULL, 1760000000000, 1760000000000, NULL, '[]', NULL);
INSERT INTO session (id, project_id, slug, directory, title, version, time_created, time_updated)
  VALUES ('ses_g2reason', 'prj2', 'calm-lynx', '/work/r', 'Explain the cache', '1.3.10', 1760000200000, 1760000290000);

INSERT INTO message VALUES ('msg_b01', 'ses_g2reason', 1760000200000, 1760000200000,
  '{"role":"user","time":{"created":1760000200000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_b01a', 'msg_b01', 'ses_g2reason', 1760000200000, 1760000200000, '{"type":"text","text":"explain the cache"}');

INSERT INTO message VALUES ('msg_b02', 'ses_g2reason', 1760000201000, 1760000209000,
  '{"role":"assistant","parentID":"msg_b01","providerID":"anthropic","modelID":"claude-x","mode":"build","agent":"build","path":{"cwd":"/work/r","root":"/work/r"},"cost":0.01,"tokens":{"input":5,"output":50,"reasoning":20,"cache":{"read":0,"write":0}},"time":{"created":1760000201000,"completed":1760000209000},"finish":"stop"}');
INSERT INTO part VALUES ('prt_b02a', 'msg_b02', 'ses_g2reason', 1760000201000, 1760000201000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_b02b', 'msg_b02', 'ses_g2reason', 1760000201000, 1760000201000, '{"type":"reasoning","text":"","metadata":{"anthropic":{"redactedData":"cmVkYWN0ZWQ="}},"time":{"start":1760000201100,"end":1760000201200}}');
INSERT INTO part VALUES ('prt_b02c', 'msg_b02', 'ses_g2reason', 1760000201000, 1760000201000, '{"type":"reasoning","text":"The cache is keyed by path.","metadata":{"anthropic":{"signature":"sig-C"}},"time":{"start":1760000201300,"end":1760000201400}}');
INSERT INTO part VALUES ('prt_b02d', 'msg_b02', 'ses_g2reason', 1760000201000, 1760000201000, '{"type":"text","text":"It is keyed by path.","time":{"start":1760000202000,"end":1760000203000}}');
INSERT INTO part VALUES ('prt_b02e', 'msg_b02', 'ses_g2reason', 1760000201000, 1760000201000, '{"type":"step-finish","reason":"stop","cost":0.01,"tokens":{"input":5,"output":50,"reasoning":20,"cache":{"read":0,"write":0}}}');

INSERT INTO message VALUES ('msg_b03', 'ses_g2reason', 1760000210000, 1760000210000,
  '{"role":"user","time":{"created":1760000210000},"agent":"build","model":{"providerID":"openai","modelID":"gpt-x"}}');
INSERT INTO part VALUES ('prt_b03a', 'msg_b03', 'ses_g2reason', 1760000210000, 1760000210000, '{"type":"text","text":"ask the other model"}');

INSERT INTO message VALUES ('msg_b04', 'ses_g2reason', 1760000211000, 1760000219000,
  '{"role":"assistant","parentID":"msg_b03","providerID":"openai","modelID":"gpt-x","mode":"build","agent":"build","path":{"cwd":"/work/r","root":"/work/r"},"cost":0.02,"tokens":{"input":30,"output":60,"reasoning":40,"cache":{"read":10,"write":0}},"time":{"created":1760000211000,"completed":1760000219000},"finish":"stop","variant":"high"}');
INSERT INTO part VALUES ('prt_b04a', 'msg_b04', 'ses_g2reason', 1760000211000, 1760000211000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_b04b', 'msg_b04', 'ses_g2reason', 1760000211000, 1760000211000, '{"type":"reasoning","text":"First thought.","metadata":{"openai":{"itemId":"rs_1","reasoningEncryptedContent":null}},"time":{"start":1760000211100,"end":1760000211200}}');
INSERT INTO part VALUES ('prt_b04c', 'msg_b04', 'ses_g2reason', 1760000211000, 1760000211000, '{"type":"reasoning","text":"Second thought.","metadata":{"openai":{"itemId":"rs_1","reasoningEncryptedContent":"enc-OPENAI"}},"time":{"start":1760000211300,"end":1760000211400}}');
INSERT INTO part VALUES ('prt_b04d', 'msg_b04', 'ses_g2reason', 1760000211000, 1760000211000, '{"type":"text","text":"The other answer.","metadata":{"openai":{"itemId":"msg_1","phase":"final_answer"}},"time":{"start":1760000212000,"end":1760000213000}}');
INSERT INTO part VALUES ('prt_b04e', 'msg_b04', 'ses_g2reason', 1760000211000, 1760000211000, '{"type":"step-finish","reason":"stop","cost":0.02,"tokens":{"input":30,"output":60,"reasoning":40,"cache":{"read":10,"write":0}}}');

INSERT INTO message VALUES ('msg_b05', 'ses_g2reason', 1760000220000, 1760000220000,
  '{"role":"user","time":{"created":1760000220000},"agent":"plan","model":{"providerID":"google","modelID":"gemini-x"}}');
INSERT INTO part VALUES ('prt_b05a', 'msg_b05', 'ses_g2reason', 1760000220000, 1760000220000, '{"type":"text","text":"plan a screenshot"}');

INSERT INTO message VALUES ('msg_b06', 'ses_g2reason', 1760000221000, 1760000229000,
  '{"role":"assistant","parentID":"msg_b05","providerID":"google","modelID":"gemini-x","mode":"plan","agent":"plan","path":{"cwd":"/work/r","root":"/work/r"},"cost":0.001,"tokens":{"input":7,"output":9,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1760000221000,"completed":1760000229000},"finish":"stop"}');
INSERT INTO part VALUES ('prt_b06a', 'msg_b06', 'ses_g2reason', 1760000221000, 1760000221000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_b06b', 'msg_b06', 'ses_g2reason', 1760000221000, 1760000221000, '{"type":"text","text":"Taking one.","metadata":{"google":{"thoughtSignature":"ts-TEXT"}}}');
INSERT INTO part VALUES ('prt_b06c', 'msg_b06', 'ses_g2reason', 1760000221000, 1760000221000, '{"type":"tool","callID":"call_s1","tool":"screenshot","metadata":{"google":{"thoughtSignature":"ts-CALL"}},"state":{"status":"completed","input":{},"output":"","title":"screen","metadata":{},"time":{"start":1760000222000,"end":1760000223000},"attachments":[{"id":"prt_att","sessionID":"ses_g2reason","messageID":"msg_b06","type":"file","mime":"image/png","filename":"screen.png","url":"data:image/png;base64,iVBORw0KGgo="}]}}');
INSERT INTO part VALUES ('prt_b06d', 'msg_b06', 'ses_g2reason', 1760000221000, 1760000221000, '{"type":"step-finish","reason":"tool-calls","cost":0.001,"tokens":{"input":7,"output":9,"reasoning":0,"cache":{"read":0,"write":0}}}');

INSERT INTO message VALUES ('msg_b07', 'ses_g2reason', 1760000230000, 1760000230000,
  '{"role":"user","time":{"created":1760000230000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_b07a', 'msg_b07', 'ses_g2reason', 1760000230000, 1760000230000, '{"type":"text","text":"search the web"}');

INSERT INTO message VALUES ('msg_b08', 'ses_g2reason', 1760000231000, 1760000239000,
  '{"role":"assistant","parentID":"msg_b07","providerID":"anthropic","modelID":"claude-x","mode":"build","agent":"build","path":{"cwd":"/work/r","root":"/work/r"},"cost":0.01,"tokens":{"input":5,"output":5,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1760000231000,"completed":1760000239000},"finish":"stop"}');
INSERT INTO part VALUES ('prt_b08a', 'msg_b08', 'ses_g2reason', 1760000231000, 1760000231000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_b08aa', 'msg_b08', 'ses_g2reason', 1760000231000, 1760000231000, '{"type":"reasoning","text":"a thought cut off before its signature","time":{"start":1760000231100}}');
INSERT INTO part VALUES ('prt_b08b', 'msg_b08', 'ses_g2reason', 1760000231000, 1760000231000, '{"type":"tool","callID":"srvtoolu_1","tool":"web_search","metadata":{"providerExecuted":true},"state":{"status":"completed","input":{"query":"cache"},"output":"[results]","title":"web","metadata":{},"time":{"start":1760000232000,"end":1760000233000}}}');
INSERT INTO part VALUES ('prt_b08c', 'msg_b08', 'ses_g2reason', 1760000231000, 1760000231000, '{"type":"text","text":"Found it."}');
INSERT INTO part VALUES ('prt_b08d', 'msg_b08', 'ses_g2reason', 1760000231000, 1760000231000, '{"type":"step-finish","reason":"stop","cost":0.01,"tokens":{"input":5,"output":5,"reasoning":0,"cache":{"read":0,"write":0}}}');
