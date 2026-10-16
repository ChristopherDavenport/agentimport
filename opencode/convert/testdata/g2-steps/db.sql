-- schema: project v1
-- A session of OpenCode 1.4: a user message of every part type, an
-- assistant message of two steps with calls in each state, a turn that
-- failed, one aborted with output, and a call cleaned up after an
-- abort. Written by hand; no row of a real database.
INSERT INTO project VALUES ('prj1', '/work/demo', 'git', 'demo', NULL, NULL, 1760000000000, 1760000000000, NULL, '[]', NULL);
INSERT INTO session (id, project_id, slug, directory, title, version, time_created, time_updated, cost, tokens_input, tokens_output, agent, model)
  VALUES ('ses_g2steps', 'prj1', 'brave-heron', '/work/demo', 'Fix the tests', '1.4.0', 1760000100000, 1760000190000, 0.05, 100, 200, 'build', '{"id":"claude-x","providerID":"anthropic"}');

INSERT INTO message VALUES ('msg_a01', 'ses_g2steps', 1760000100000, 1760000100000,
  '{"role":"user","time":{"created":1760000100000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"},"system":"Prefer table tests."}');
INSERT INTO part VALUES ('prt_a01a', 'msg_a01', 'ses_g2steps', 1760000100000, 1760000100000, '{"type":"text","text":"fix the tests"}');
INSERT INTO part VALUES ('prt_a01b', 'msg_a01', 'ses_g2steps', 1760000100000, 1760000100000, '{"type":"text","text":"Called the Read tool with the following input: {\"filePath\":\"notes.txt\"}","synthetic":true}');
INSERT INTO part VALUES ('prt_a01c', 'msg_a01', 'ses_g2steps', 1760000100000, 1760000100000, '{"type":"text","text":"an ignored note","ignored":true}');
INSERT INTO part VALUES ('prt_a01d', 'msg_a01', 'ses_g2steps', 1760000100000, 1760000100000, '{"type":"file","mime":"image/png","filename":"shot.png","url":"data:image/png;base64,iVBORw0KGgo="}');
INSERT INTO part VALUES ('prt_a01e', 'msg_a01', 'ses_g2steps', 1760000100000, 1760000100000, '{"type":"file","mime":"text/plain","filename":"notes.txt","url":"file:///work/demo/notes.txt","source":{"type":"file","path":"notes.txt","text":{"value":"@notes.txt","start":0,"end":10}}}');
INSERT INTO part VALUES ('prt_a01f', 'msg_a01', 'ses_g2steps', 1760000100000, 1760000100000, '{"type":"agent","name":"general","source":{"value":"@general","start":14,"end":22}}');
INSERT INTO part VALUES ('prt_a01g', 'msg_a01', 'ses_g2steps', 1760000100000, 1760000100000, '{"type":"file","mime":"application/pdf","filename":"spec.pdf","url":"https://example.com/spec.pdf"}');

INSERT INTO message VALUES ('msg_a02', 'ses_g2steps', 1760000101000, 1760000110000,
  '{"role":"assistant","parentID":"msg_a01","providerID":"anthropic","modelID":"claude-x","mode":"build","agent":"build","path":{"cwd":"/work/demo","root":"/work/demo"},"cost":0.02,"tokens":{"input":20,"output":15,"reasoning":4,"cache":{"read":100,"write":10}},"time":{"created":1760000101000,"completed":1760000110000},"finish":"stop"}');
INSERT INTO part VALUES ('prt_a02a', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"step-start","snapshot":"aaa"}');
INSERT INTO part VALUES ('prt_a02b', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"reasoning","text":"","metadata":{"anthropic":{"signature":"sig-B"}},"time":{"start":1760000101100,"end":1760000101200}}');
INSERT INTO part VALUES ('prt_a02c', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"text","text":"","time":{"start":1760000101300}}');
INSERT INTO part VALUES ('prt_a02d', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"tool","callID":"toolu_r1","tool":"read","state":{"status":"completed","input":{"filePath":"a_test.go"},"output":"package a","title":"a_test.go","metadata":{},"time":{"start":1760000102000,"end":1760000102100}}}');
INSERT INTO part VALUES ('prt_a02e', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"tool","callID":"toolu_b1","tool":"bash","state":{"status":"error","input":{"command":"rm -rf build"},"error":"The user rejected permission to use this specific tool call.","time":{"start":1760000102000,"end":1760000103000}}}');
INSERT INTO part VALUES ('prt_a02f', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"tool","callID":"toolu_g1","tool":"glob","state":{"status":"pending","input":{},"raw":"{\"pattern\":\"*.go"}}');
INSERT INTO part VALUES ('prt_a02g', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"tool","callID":"toolu_w1","tool":"write","state":{"status":"running","input":{"filePath":"b.go"},"title":"b.go","metadata":{"progress":1},"time":{"start":1760000102500}}}');
INSERT INTO part VALUES ('prt_a02h', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"tool","callID":"toolu_d1","tool":"bash","state":{"status":"error","input":{"command":"go vet"},"error":"The user rejected permission to use this specific tool call with the following feedback: use make","time":{"start":1760000102000,"end":1760000103000}}}');
INSERT INTO part VALUES ('prt_a02i', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"tool","callID":"toolu_e1","tool":"edit","state":{"status":"error","input":{"filePath":"c.go"},"error":"Error: oldString not found","time":{"start":1760000102000,"end":1760000103000}}}');
INSERT INTO part VALUES ('prt_a02j', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"step-finish","reason":"tool-calls","snapshot":"bbb","cost":0.012,"tokens":{"input":10,"output":8,"reasoning":4,"cache":{"read":50,"write":10}}}');
INSERT INTO part VALUES ('prt_a02k', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"patch","hash":"bbb","files":["/work/demo/b.go"]}');
INSERT INTO part VALUES ('prt_a02l', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"step-start","snapshot":"bbb"}');
INSERT INTO part VALUES ('prt_a02m', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"retry","attempt":1,"error":{"name":"APIError","data":{"message":"overloaded","isRetryable":true}},"time":{"created":1760000104000}}');
INSERT INTO part VALUES ('prt_a02n', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"text","text":"I stopped: the user refused.","time":{"start":1760000105000,"end":1760000106000}}');
INSERT INTO part VALUES ('prt_a02o', 'msg_a02', 'ses_g2steps', 1760000101000, 1760000101000, '{"type":"step-finish","reason":"stop","cost":0.008,"tokens":{"input":20,"output":15,"reasoning":4,"cache":{"read":100,"write":0}}}');

INSERT INTO message VALUES ('msg_a03', 'ses_g2steps', 1760000120000, 1760000120000,
  '{"role":"user","time":{"created":1760000120000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_a03a', 'msg_a03', 'ses_g2steps', 1760000120000, 1760000120000, '{"type":"text","text":"try again"}');

INSERT INTO message VALUES ('msg_a04', 'ses_g2steps', 1760000121000, 1760000125000,
  '{"role":"assistant","parentID":"msg_a03","providerID":"anthropic","modelID":"claude-x","mode":"build","agent":"build","path":{"cwd":"/work/demo","root":"/work/demo"},"cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1760000121000,"completed":1760000125000},"error":{"name":"APIError","data":{"message":"rate limited","statusCode":429,"isRetryable":true}},"finish":"error"}');
INSERT INTO part VALUES ('prt_a04a', 'msg_a04', 'ses_g2steps', 1760000121000, 1760000121000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_a04b', 'msg_a04', 'ses_g2steps', 1760000121000, 1760000121000, '{"type":"text","text":"partial answer before the error"}');

INSERT INTO message VALUES ('msg_a05', 'ses_g2steps', 1760000130000, 1760000130000,
  '{"role":"user","time":{"created":1760000130000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_a05a', 'msg_a05', 'ses_g2steps', 1760000130000, 1760000130000, '{"type":"text","text":"and again"}');

INSERT INTO message VALUES ('msg_a06', 'ses_g2steps', 1760000131000, 1760000135000,
  '{"role":"assistant","parentID":"msg_a05","providerID":"anthropic","modelID":"claude-x","mode":"build","agent":"build","path":{"cwd":"/work/demo","root":"/work/demo"},"cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1760000131000,"completed":1760000135000},"error":{"name":"MessageAbortedError","data":{"message":"Aborted"}}}');
INSERT INTO part VALUES ('prt_a06a', 'msg_a06', 'ses_g2steps', 1760000131000, 1760000131000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_a06b', 'msg_a06', 'ses_g2steps', 1760000131000, 1760000131000, '{"type":"text","text":"half an answer"}');
INSERT INTO part VALUES ('prt_a06c', 'msg_a06', 'ses_g2steps', 1760000131000, 1760000131000, '{"type":"tool","callID":"toolu_x1","tool":"bash","state":{"status":"error","input":{"command":"go test ./..."},"error":"Tool execution aborted","metadata":{"interrupted":true,"output":"ok  demo/a 0.1s"},"time":{"start":1760000132000,"end":1760000133000}}}');

INSERT INTO message VALUES ('msg_a07', 'ses_g2steps', 1760000140000, 1760000140000,
  '{"role":"user","time":{"created":1760000140000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-x"}}');
INSERT INTO part VALUES ('prt_a07a', 'msg_a07', 'ses_g2steps', 1760000140000, 1760000140000, '{"type":"text","text":"","ignored":false}');
INSERT INTO part VALUES ('prt_a07b', 'msg_a07', 'ses_g2steps', 1760000140000, 1760000140000, '{"type":"text","text":"only ignored","ignored":true}');
