-- The project table, as OpenCode 2.0 creates it on a fresh install
-- (schema.gen.ts at 2.0.30).
CREATE TABLE `project` (
	`id` text PRIMARY KEY,
	`worktree` text NOT NULL,
	`vcs` text,
	`name` text,
	`icon_url` text,
	`icon_url_override` text,
	`icon_color` text,
	`time_created` integer NOT NULL,
	`time_updated` integer NOT NULL,
	`time_initialized` integer,
	`time_active` integer DEFAULT 0 NOT NULL,
	`time_archived` integer,
	`sandboxes` text NOT NULL,
	`commands` text
);
