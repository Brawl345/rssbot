-- +migrate Up

ALTER TABLE `feeds`
    ADD COLUMN `etag` VARCHAR(255) DEFAULT NULL AFTER `last_entry`,
    ADD COLUMN `last_modified` VARCHAR(255) DEFAULT NULL AFTER `etag`;
