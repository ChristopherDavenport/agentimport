-- The V2 tables, as OpenCode 2.0 creates them (schema.gen.ts at
-- 2.0.30): what a fresh install has beside the project table, and what
-- migration 20260804233008_loose_psylocke adds to an upgraded V1
-- database, which keeps its V1 tables.
CREATE TABLE `kv` (
	`key` text PRIMARY KEY,
	`value` text NOT NULL,
	`time_created` integer NOT NULL,
	`time_updated` integer NOT NULL
);
CREATE TABLE `event_sequence` (
	`aggregate_id` text PRIMARY KEY,
	`seq` integer NOT NULL,
	`owner_id` text
);
CREATE TABLE `event` (
	`id` text PRIMARY KEY,
	`aggregate_id` text NOT NULL,
	`seq` integer NOT NULL,
	`created` integer DEFAULT 0 NOT NULL,
	`type` text NOT NULL,
	`data` text NOT NULL,
	CONSTRAINT `fk_event_aggregate_id_event_sequence_aggregate_id_fk` FOREIGN KEY (`aggregate_id`) REFERENCES `event_sequence`(`aggregate_id`) ON DELETE CASCADE
);
CREATE TABLE `session_v2` (
	`id` text PRIMARY KEY,
	`project_id` text NOT NULL,
	`workspace_id` text,
	`parent_id` text,
	`fork_session_id` text,
	`fork_boundary` text,
	`slug` text NOT NULL,
	`directory` text NOT NULL,
	`path` text,
	`title` text,
	`version` text NOT NULL,
	`share_url` text,
	`summary_additions` integer,
	`summary_deletions` integer,
	`summary_files` integer,
	`summary_diffs` text,
	`metadata` text,
	`cost` real DEFAULT 0 NOT NULL,
	`tokens_input` integer DEFAULT 0 NOT NULL,
	`tokens_output` integer DEFAULT 0 NOT NULL,
	`tokens_reasoning` integer DEFAULT 0 NOT NULL,
	`tokens_cache_read` integer DEFAULT 0 NOT NULL,
	`tokens_cache_write` integer DEFAULT 0 NOT NULL,
	`revert` text,
	`permission` text,
	`agent` text,
	`model` text,
	`time_created` integer NOT NULL,
	`time_updated` integer NOT NULL,
	`time_idle` integer,
	`time_viewed` integer,
	`idle_outcome` text,
	`time_compacting` integer,
	`time_archived` integer,
	`time_suspended` integer,
	`resume_attempts` integer DEFAULT 0 NOT NULL,
	CONSTRAINT `fk_session_v2_project_id_project_id_fk` FOREIGN KEY (`project_id`) REFERENCES `project`(`id`) ON DELETE CASCADE
);
CREATE TABLE `session_message` (
	`id` text PRIMARY KEY,
	`session_id` text NOT NULL,
	`type` text NOT NULL,
	`seq` integer NOT NULL,
	`time_created` integer NOT NULL,
	`time_updated` integer NOT NULL,
	`data` text NOT NULL,
	CONSTRAINT `fk_session_message_session_id_session_v2_id_fk` FOREIGN KEY (`session_id`) REFERENCES `session_v2`(`id`) ON DELETE CASCADE
);
CREATE TABLE `session_pending` (
	`id` text PRIMARY KEY,
	`session_id` text NOT NULL,
	`type` text NOT NULL,
	`data` text NOT NULL,
	`delivery` text,
	`admitted_seq` integer NOT NULL,
	`time_created` integer NOT NULL,
	CONSTRAINT `fk_session_pending_session_id_session_v2_id_fk` FOREIGN KEY (`session_id`) REFERENCES `session_v2`(`id`) ON DELETE CASCADE
);
CREATE TABLE `session_inbox` (
	`id` text PRIMARY KEY,
	`session_id` text NOT NULL,
	`type` text NOT NULL,
	`payload` text NOT NULL,
	`delivery` text NOT NULL,
	`enqueued_seq` integer NOT NULL,
	`time_created` integer NOT NULL,
	CONSTRAINT `fk_session_inbox_session_id_session_v2_id_fk` FOREIGN KEY (`session_id`) REFERENCES `session_v2`(`id`) ON DELETE CASCADE
);
CREATE UNIQUE INDEX `session_message_session_seq_idx` ON `session_message` (`session_id`,`seq`);
CREATE INDEX `session_message_session_type_seq_idx` ON `session_message` (`session_id`,`type`,`seq`);
CREATE INDEX `session_v2_parent_idx` ON `session_v2` (`parent_id`);
