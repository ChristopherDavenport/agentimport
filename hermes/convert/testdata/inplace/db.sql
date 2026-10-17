-- schema: v31
-- Two in-place compactions of one session. The first protected the
-- first two rows, copied before its summary (an assistant summary row),
-- and
-- carried the last turn, whose originals it flagged out of context and
-- whose copies keep their times; the second, after the protection
-- decayed, has a user summary and no head; the user turn after it
-- follows another, which a resume merges. A rewind undid one
-- turn between them. Rows 1-5, 8-14 and 17-18 are compacted (active 0,
-- compacted 1), the carried turn's originals, 6-7, and the rewound
-- turn inactive (active 0, compacted 0), the rest live. Written by
-- hand.
INSERT INTO sessions (id, source, model, started_at, end_reason, cwd) VALUES ('20261003_080000_cc0003', 'cli', 'openai/gpt-5.5', 1759478400.0, NULL, '/work/c');
-- Generation 0.
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted) VALUES (1, '20261003_080000_cc0003', 'user', 'Build the parser.', 1759478401.0, 0, 1);
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted, finish_reason) VALUES (2, '20261003_080000_cc0003', 'assistant', 'Starting on the parser.', 1759478402.0, 0, 1, 'stop');
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted) VALUES (3, '20261003_080000_cc0003', 'user', 'Add tests.', 1759478403.0, 0, 1);
INSERT INTO messages (id, session_id, role, content, tool_calls, timestamp, active, compacted, finish_reason) VALUES (4, '20261003_080000_cc0003', 'assistant', '', '[{"id":"call_a","type":"function","function":{"name":"write_file","arguments":"{}"}}]', 1759478404.0, 0, 1, 'tool_calls');
INSERT INTO messages (id, session_id, role, content, tool_call_id, timestamp, active, compacted) VALUES (5, '20261003_080000_cc0003', 'tool', 'written', 'call_a', 1759478405.0, 0, 1);
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted) VALUES (6, '20261003_080000_cc0003', 'user', 'Run them.', 1759478406.0, 0, 0);
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted, finish_reason) VALUES (7, '20261003_080000_cc0003', 'assistant', 'They pass.', 1759478407.0, 0, 0, 'stop');
-- Generation 1: the head copied, the summary, the tail copied.
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted) VALUES (8, '20261003_080000_cc0003', 'user', 'Build the parser.', 1759478401.0, 0, 1);
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted, finish_reason) VALUES (9, '20261003_080000_cc0003', 'assistant', 'Starting on the parser.', 1759478402.0, 0, 1, 'stop');
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted, _compressed_summary) VALUES (10, '20261003_080000_cc0003', 'assistant', '[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted into the summary below. One: tests were added.', 1759478500.0, 0, 1, 1);
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted) VALUES (11, '20261003_080000_cc0003', 'user', 'Run them.', 1759478406.0, 0, 1);
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted, finish_reason) VALUES (12, '20261003_080000_cc0003', 'assistant', 'They pass.', 1759478407.0, 0, 1, 'stop');
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted) VALUES (13, '20261003_080000_cc0003', 'user', 'Now the lexer.', 1759478510.0, 0, 1);
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted, finish_reason) VALUES (14, '20261003_080000_cc0003', 'assistant', 'Lexer done.', 1759478520.0, 0, 1, 'stop');
-- A turn a rewind undid.
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted) VALUES (15, '20261003_080000_cc0003', 'user', 'Delete everything.', 1759478530.0, 0, 0);
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted, finish_reason) VALUES (16, '20261003_080000_cc0003', 'assistant', 'Deleting.', 1759478531.0, 0, 0, 'stop');
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted) VALUES (17, '20261003_080000_cc0003', 'user', 'Add docs.', 1759478540.0, 0, 1);
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted, finish_reason) VALUES (18, '20261003_080000_cc0003', 'assistant', 'Docs added.', 1759478541.0, 0, 1, 'stop');
-- Generation 2: a user summary, no head, no tail.
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted, _compressed_summary) VALUES (19, '20261003_080000_cc0003', 'user', '[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted into the summary below. Two: parser, lexer, docs.', 1759478600.0, 1, 0, 1);
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted) VALUES (20, '20261003_080000_cc0003', 'user', 'Ship it.', 1759478610.0, 1, 0);
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted, finish_reason) VALUES (21, '20261003_080000_cc0003', 'assistant', 'Shipped.', 1759478611.0, 1, 0, 'stop');
