-- The project table, as OpenCode 1.19 has it: the table of
-- 20260127222353_familiar_lady_ursula with the columns later
-- migrations added. Written by hand from OpenCode's migrations; no row
-- of a real database.
CREATE TABLE `project` (
	`id` text PRIMARY KEY,
	`worktree` text NOT NULL,
	`vcs` text,
	`name` text,
	`icon_url` text,
	`icon_color` text,
	`time_created` integer NOT NULL,
	`time_updated` integer NOT NULL,
	`time_initialized` integer,
	`sandboxes` text NOT NULL,
	`commands` text
);
