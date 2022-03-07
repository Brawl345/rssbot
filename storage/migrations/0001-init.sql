-- +migrate Up

CREATE TABLE IF NOT EXISTS `chats`
(
    `id`         bigint(20) NOT NULL,
    `created_at` datetime   NOT NULL DEFAULT current_timestamp(),
    PRIMARY KEY (`id`) USING BTREE
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4;

CREATE TABLE IF NOT EXISTS `feeds`
(
    `id`         int(11)       NOT NULL AUTO_INCREMENT,
    `url`        varchar(1024) NOT NULL,
    `last_entry` varchar(1024)          DEFAULT NULL,
    `created_at` datetime      NOT NULL DEFAULT current_timestamp(),
    `updated_at` datetime               DEFAULT NULL,
    PRIMARY KEY (`id`) USING BTREE,
    UNIQUE KEY `url` (`url`) USING HASH
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4;

CREATE TABLE IF NOT EXISTS `abonnements`
(
    `chat_id`    bigint(20) NOT NULL,
    `feed_id`    int(11)    NOT NULL,
    `created_at` datetime   NOT NULL DEFAULT current_timestamp(),
    PRIMARY KEY (`chat_id`, `feed_id`),
    KEY `FK_abonnements_feeds` (`feed_id`),
    CONSTRAINT `FK_abonnements_chats` FOREIGN KEY (`chat_id`) REFERENCES `chats` (`id`) ON UPDATE CASCADE,
    CONSTRAINT `FK_abonnements_feeds` FOREIGN KEY (`feed_id`) REFERENCES `feeds` (`id`) ON UPDATE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4;
