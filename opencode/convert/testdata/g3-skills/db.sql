-- schema: project2 v2
-- A session of OpenCode 2.0 whose user row carries two skills, each
-- with a name and a member this converter does not declare, one of
-- them without text. Written by hand; no row of a real database.
INSERT INTO project VALUES ('prj6', '/work/s', 'git', NULL, NULL, NULL, NULL, 1760000000000, 1760000000000, NULL, 0, NULL, '[]', NULL);
INSERT INTO kv VALUES ('migration.v1-v2', '{"phase":"completed"}', 1760000000000, 1760000000000);
INSERT INTO session_v2 (id, project_id, parent_id, fork_session_id, fork_boundary, slug, directory, title, version, revert, agent, model, time_created, time_updated) VALUES ('ses_skills', 'prj6', NULL, NULL, NULL, 'slug', '/work/s', 'Skills', '2.0.20', NULL, 'build', '{"id":"claude-x","providerID":"anthropic"}', 1760000800000, 1760000900000);
INSERT INTO session_message VALUES ('msg_s01', 'ses_skills', 'user', 1, 1760000801000, 1760000801000, '{"text":"tidy the docs","skills":[{"id":"sk_1","name":"docs-style","text":"Write short sentences.","location":"/work/s/.opencode/skills/docs-style/SKILL.md","x_origin":"project"},{"id":"sk_2","name":"no-body","x_origin":"global"}],"time":{"created":1760000801000}}');
INSERT INTO session_message VALUES ('msg_s02', 'ses_skills', 'assistant', 2, 1760000802000, 1760000802000, '{"agent":"build","model":{"id":"claude-x","providerID":"anthropic"},"content":[{"type":"text","text":"Done."}],"finish":"stop","tokens":{"input":20,"output":2,"reasoning":0,"cache":{"read":0,"write":0}},"cost":0.001,"time":{"created":1760000802000,"completed":1760000802100}}');
