-- schema: v31
-- One assistant turn per reasoning carrier: reasoning_content
-- (DeepSeek) beside a whitespace pad in reasoning, reasoning alone,
-- Anthropic thinking and redacted thinking blocks in reasoning_details,
-- OpenRouter's reasoning.text, reasoning.summary and
-- reasoning.encrypted, a provider's native carrier no item holds, the
-- Codex route's Responses items with a phase, and a Gemini thought
-- signature on a tool call, and an unsigned reasoning.text. Written by
-- hand.
INSERT INTO sessions (id, source, model, started_at, cwd) VALUES ('20261002_090000_bb0002', 'tui', 'deepseek/deepseek-v4', 1759395600.0, '/work/r');
INSERT INTO messages (session_id, role, content, timestamp) VALUES ('20261002_090000_bb0002', 'user', 'one', 1759395601.0);
INSERT INTO messages (session_id, role, content, reasoning, reasoning_content, timestamp, finish_reason) VALUES ('20261002_090000_bb0002', 'assistant', 'Answer one.', ' ', 'I think about one.', 1759395602.0, 'stop');
INSERT INTO messages (session_id, role, content, timestamp) VALUES ('20261002_090000_bb0002', 'user', 'two', 1759395603.0);
INSERT INTO messages (session_id, role, content, reasoning, reasoning_content, timestamp, finish_reason) VALUES ('20261002_090000_bb0002', 'assistant', 'Answer two.', 'Plain reasoning text.', ' ', 1759395604.0, 'stop');
INSERT INTO messages (session_id, role, content, timestamp) VALUES ('20261002_090000_bb0002', 'user', 'three', 1759395605.0);
INSERT INTO messages (session_id, role, content, reasoning, reasoning_details, timestamp, finish_reason) VALUES ('20261002_090000_bb0002', 'assistant', 'Answer three.', 'Thinking about three.',
  '[{"type":"thinking","thinking":"Thinking about three.","signature":"c2lnLXRocmVl"},{"type":"redacted_thinking","data":"cmVkYWN0ZWQ="}]', 1759395606.0, 'stop');
INSERT INTO messages (session_id, role, content, timestamp) VALUES ('20261002_090000_bb0002', 'user', 'four', 1759395607.0);
INSERT INTO messages (session_id, role, content, reasoning, reasoning_details, timestamp, finish_reason) VALUES ('20261002_090000_bb0002', 'assistant', 'Answer four.', 'Text four.',
  '[{"type":"reasoning.text","text":"Text four.","signature":"c2lnLWZvdXI=","format":"anthropic-claude-v1","index":0},{"type":"reasoning.summary","summary":"Summary four.","format":"openai-responses-v1"},{"type":"reasoning.encrypted","data":"ZW5jLWZvdXI=","format":"openai-responses-v1","id":"rs_4"}]', 1759395608.0, 'stop');
INSERT INTO messages (session_id, role, content, timestamp) VALUES ('20261002_090000_bb0002', 'user', 'five', 1759395609.0);
INSERT INTO messages (session_id, role, content, reasoning, reasoning_details, timestamp, finish_reason) VALUES ('20261002_090000_bb0002', 'assistant', 'Answer five.', 'Native five.',
  '[{"type":"hermes.native_assistant","provider":"bedrock","blocks":[{"reasoningContent":{"reasoningText":{"text":"Native five."}}}]}]', 1759395610.0, 'stop');
INSERT INTO messages (session_id, role, content, timestamp) VALUES ('20261002_090000_bb0002', 'user', 'six', 1759395611.0);
INSERT INTO messages (session_id, role, content, reasoning, codex_reasoning_items, codex_message_items, timestamp, finish_reason) VALUES ('20261002_090000_bb0002', 'assistant', 'Answer six.', 'Summary six.',
  '[{"type":"reasoning","id":"rs_6","summary":[{"type":"summary_text","text":"Summary six."}],"encrypted_content":"ZW5jLXNpeA==","_issuer_kind":"openai-codex","_issuer_model":"gpt-5.5-codex"}]',
  '[{"type":"message","role":"assistant","id":"msg_6","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"Answer six."}]}]', 1759395612.0, 'stop');
INSERT INTO messages (session_id, role, content, timestamp) VALUES ('20261002_090000_bb0002', 'user', 'seven', 1759395613.0);
INSERT INTO messages (session_id, role, content, tool_calls, timestamp, finish_reason) VALUES ('20261002_090000_bb0002', 'assistant', '',
  '[{"id":"call_7","type":"function","function":{"name":"terminal","arguments":"{\"command\":\"ls\"}"},"extra_content":{"google":{"thought_signature":"dGhvdWdodC03"}}}]', 1759395614.0, 'tool_calls');
INSERT INTO messages (session_id, role, content, tool_call_id, tool_name, timestamp) VALUES ('20261002_090000_bb0002', 'tool', 'a.txt', 'call_7', 'terminal', 1759395615.0);
INSERT INTO messages (session_id, role, content, timestamp, finish_reason) VALUES ('20261002_090000_bb0002', 'assistant', 'There is a.txt.', 1759395616.0, 'stop');
-- OpenRouter's reasoning.text for a model that signs nothing, beside
-- the same text in reasoning and reasoning_content.
INSERT INTO messages (session_id, role, content, timestamp) VALUES ('20261002_090000_bb0002', 'user', 'eight', 1759395617.0);
INSERT INTO messages (session_id, role, content, reasoning, reasoning_content, reasoning_details, timestamp, finish_reason) VALUES ('20261002_090000_bb0002', 'assistant', 'Answer eight.', 'Unsigned eight.', 'Unsigned eight.',
  '[{"type":"reasoning.text","text":"Unsigned eight.","format":"unknown","index":0}]', 1759395618.0, 'stop');
