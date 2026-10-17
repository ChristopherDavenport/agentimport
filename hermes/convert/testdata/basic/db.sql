-- schema: v31
-- A CLI session with one tool call, its output and a reply, the
-- system prompt in system_prompts, usage per session and per model,
-- a git checkout and a title. Written by hand.
INSERT INTO system_prompts VALUES ('h1', 'You are Hermes, a helpful agent.');
INSERT INTO sessions (id, source, model, model_config, system_prompt_hash, started_at, ended_at, end_reason, message_count, tool_call_count, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, cwd, git_branch, git_repo_root, billing_provider, estimated_cost_usd, title, api_call_count, tool_names)
  VALUES ('20261001_100000_aa0001', 'cli', 'anthropic/claude-sonnet-4', '{"temperature":0.2}', 'h1', 1759312800.0, 1759312900.5, 'cli_close', 4, 1, 1200, 80, 900, 100, 0, '/work/app', 'main', '/work/app', 'openrouter', 0.0123, 'Read the readme', 2, '["read_file","terminal"]');
INSERT INTO session_model_usage (session_id, model, billing_provider, api_call_count, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, estimated_cost_usd, first_seen, last_seen)
  VALUES ('20261001_100000_aa0001', 'anthropic/claude-sonnet-4', 'openrouter', 2, 1200, 80, 900, 100, 0.0123, 1759312801.0, 1759312805.0);
INSERT INTO messages (session_id, role, content, timestamp, message_uid, display_order, display_identity) VALUES ('20261001_100000_aa0001', 'user', '  What does the README say?  ', 1759312801.25, 'u-1', 1, x'00ff');
INSERT INTO messages (session_id, role, content, tool_calls, timestamp, finish_reason, message_uid, tool_call_uids)
  VALUES ('20261001_100000_aa0001', 'assistant', 'Let me read it.', '[{"id":"toolu_01","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]', 1759312802.5, 'tool_calls', 'u-2', '{"toolu_01":"tc-1"}');
INSERT INTO messages (session_id, role, content, tool_call_id, tool_name, effect_disposition, timestamp, tool_call_uid)
  VALUES ('20261001_100000_aa0001', 'tool', '# App' || char(10) || 'It prints hello.' || char(10), 'toolu_01', 'read_file', 'none', 1759312803.0, 'tc-1');
INSERT INTO messages (session_id, role, content, timestamp, finish_reason, token_count) VALUES ('20261001_100000_aa0001', 'assistant', 'The README says the app prints hello.', 1759312805.0, 'stop', 12);
