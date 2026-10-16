-- sessions.db: the sessions and spawn queue tables of Cline's schema (shared/src/db/sqlite-db.ts)
CREATE TABLE IF NOT EXISTS sessions (
	session_id TEXT PRIMARY KEY,
	source TEXT NOT NULL,
	pid INTEGER NOT NULL,
	started_at TEXT NOT NULL,
	ended_at TEXT,
	exit_code INTEGER,
	status TEXT NOT NULL,
	status_lock INTEGER NOT NULL DEFAULT 0,
	interactive INTEGER NOT NULL,
	provider TEXT NOT NULL,
	model TEXT NOT NULL,
	cwd TEXT NOT NULL,
	workspace_root TEXT NOT NULL,
	team_name TEXT,
	enable_tools INTEGER NOT NULL,
	enable_spawn INTEGER NOT NULL,
	enable_teams INTEGER NOT NULL,
	parent_session_id TEXT,
	parent_agent_id TEXT,
	agent_id TEXT,
	conversation_id TEXT,
	is_subagent INTEGER NOT NULL DEFAULT 0,
	prompt TEXT,
	metadata_json TEXT,
	transcript_path TEXT NOT NULL DEFAULT '',
	hook_path TEXT NOT NULL,
	messages_path TEXT,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS subagent_spawn_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	root_session_id TEXT NOT NULL,
	parent_agent_id TEXT NOT NULL,
	task TEXT,
	system_prompt TEXT,
	created_at TEXT NOT NULL,
	consumed_at TEXT
);
INSERT INTO sessions (session_id, source, pid, started_at, status, interactive, provider, model, cwd, workspace_root, enable_tools, enable_spawn, enable_teams, parent_session_id, parent_agent_id, agent_id, conversation_id, is_subagent, prompt, metadata_json, hook_path, messages_path, updated_at) VALUES ('1759327200000_sub01', 'cli', 4242, '2025-10-01T14:00:00.000Z', 'completed', 1, 'anthropic', 'claude-x', '/work/proj', '/work/proj', 1, 1, 1, NULL, NULL, NULL, 'conv_root', 0, 'Plan the refactor with helpers', NULL, '', NULL, '2025-10-01T14:00:00.000Z');
INSERT INTO sessions (session_id, source, pid, started_at, status, interactive, provider, model, cwd, workspace_root, enable_tools, enable_spawn, enable_teams, parent_session_id, parent_agent_id, agent_id, conversation_id, is_subagent, prompt, metadata_json, hook_path, messages_path, updated_at) VALUES ('1759327200000_sub01__agent_7', 'cli', 4242, '2025-10-01T14:00:00.000Z', 'completed', 1, 'anthropic', 'claude-x', '/work/proj', '/work/proj', 1, 1, 1, '1759327200000_sub01', 'agent_lead01', 'agent_7', 'conv_agent_7', 1, 'Survey the lexer', NULL, '', NULL, '2025-10-01T14:00:00.000Z');
INSERT INTO sessions (session_id, source, pid, started_at, status, interactive, provider, model, cwd, workspace_root, enable_tools, enable_spawn, enable_teams, parent_session_id, parent_agent_id, agent_id, conversation_id, is_subagent, prompt, metadata_json, hook_path, messages_path, updated_at) VALUES ('1759327200000_sub01__agent_8', 'cli', 4242, '2025-10-01T14:00:00.000Z', 'completed', 1, 'anthropic', 'claude-x', '/work/proj', '/work/proj', 1, 1, 1, '1759327200000_sub01', 'agent_lead01', 'agent_8', 'conv_agent_8', 1, 'Survey the parser', NULL, '', NULL, '2025-10-01T14:00:00.000Z');
INSERT INTO sessions (session_id, source, pid, started_at, status, interactive, provider, model, cwd, workspace_root, enable_tools, enable_spawn, enable_teams, parent_session_id, parent_agent_id, agent_id, conversation_id, is_subagent, prompt, metadata_json, hook_path, messages_path, updated_at) VALUES ('1759327200000_sub01__agent_9', 'cli', 4242, '2025-10-01T14:00:00.000Z', 'completed', 1, 'anthropic', 'claude-x', '/work/proj', '/work/proj', 1, 1, 1, '1759327200000_sub01', 'agent_8', 'agent_9', 'conv_agent_9', 1, 'Survey the grammar', NULL, '', NULL, '2025-10-01T14:00:00.000Z');
INSERT INTO sessions (session_id, source, pid, started_at, status, interactive, provider, model, cwd, workspace_root, enable_tools, enable_spawn, enable_teams, parent_session_id, parent_agent_id, agent_id, conversation_id, is_subagent, prompt, metadata_json, hook_path, messages_path, updated_at) VALUES ('1759327200000_sub01__teamtask__reviewer__Ab3dE9', 'cli', 4242, '2025-10-01T14:00:00.000Z', 'completed', 1, 'anthropic', 'claude-x', '/work/proj', '/work/proj', 1, 1, 1, '1759327200000_sub01', 'lead', 'reviewer', 'conv_reviewer', 1, 'Review the plan', NULL, '', NULL, '2025-10-01T14:00:00.000Z');
INSERT INTO subagent_spawn_queue (root_session_id, parent_agent_id, task, system_prompt, created_at, consumed_at) VALUES ('1759327200000_sub01', 'agent_lead01', 'Survey the lexer', 'You survey code and report.', '2025-10-01T14:00:01.000Z', '2025-10-01T14:00:02.000Z');
INSERT INTO subagent_spawn_queue (root_session_id, parent_agent_id, task, system_prompt, created_at, consumed_at) VALUES ('1759327200000_sub01', 'agent_lead01', 'Survey the parser', 'You survey code and report.', '2025-10-01T14:00:01.000Z', '2025-10-01T14:00:02.500Z');
