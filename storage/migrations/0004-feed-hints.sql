-- +migrate Up

ALTER TABLE `feeds`
    ADD COLUMN `feed_interval` INT(11)      NOT NULL DEFAULT 0,
    ADD COLUMN `skip_hours`    VARCHAR(100) DEFAULT NULL,
    ADD COLUMN `skip_days`     VARCHAR(100) DEFAULT NULL;

-- +migrate Down

ALTER TABLE `feeds`
    DROP COLUMN `feed_interval`,
    DROP COLUMN `skip_hours`,
    DROP COLUMN `skip_days`;
