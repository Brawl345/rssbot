-- +migrate Up

ALTER TABLE `feeds`
    ADD COLUMN `etag`            VARCHAR(512) DEFAULT NULL,
    ADD COLUMN `last_modified`   VARCHAR(128) DEFAULT NULL,
    ADD COLUMN `next_poll_at`    DATETIME     DEFAULT NULL,
    ADD COLUMN `last_poll_at`    DATETIME     DEFAULT NULL,
    ADD COLUMN `error_count`     INT(11)      NOT NULL DEFAULT 0,
    ADD COLUMN `unchanged_count` INT(11)      NOT NULL DEFAULT 0,
    ADD COLUMN `disabled`        TINYINT(1)   NOT NULL DEFAULT 0,
    ADD COLUMN `disabled_reason` VARCHAR(512) DEFAULT NULL,
    ADD INDEX `idx_feeds_due` (`disabled`, `next_poll_at`) USING BTREE;

-- +migrate Down

ALTER TABLE `feeds`
    DROP INDEX `idx_feeds_due`,
    DROP COLUMN `etag`,
    DROP COLUMN `last_modified`,
    DROP COLUMN `next_poll_at`,
    DROP COLUMN `last_poll_at`,
    DROP COLUMN `error_count`,
    DROP COLUMN `unchanged_count`,
    DROP COLUMN `disabled`,
    DROP COLUMN `disabled_reason`;
