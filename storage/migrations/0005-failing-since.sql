-- +migrate Up

ALTER TABLE `feeds`
    ADD COLUMN `failing_since` DATETIME DEFAULT NULL;

-- +migrate Down

ALTER TABLE `feeds`
    DROP COLUMN `failing_since`;
