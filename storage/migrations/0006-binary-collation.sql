-- +migrate Up

-- Newer MariaDB defaults to utf8mb4_uca1400_ai_ci, which treats e.g. "…" and
-- "..." or differently cased URL paths as equal. Compare byte-exact instead.
ALTER TABLE `replacements`
    MODIFY `value` VARCHAR(255) NOT NULL COLLATE utf8mb4_bin;

ALTER TABLE `feeds`
    MODIFY `url` VARCHAR(512) NOT NULL COLLATE utf8mb4_bin;

-- +migrate Down

ALTER TABLE `feeds`
    MODIFY `url` VARCHAR(512) NOT NULL COLLATE utf8mb4_general_ci;

ALTER TABLE `replacements`
    MODIFY `value` VARCHAR(255) NOT NULL COLLATE utf8mb4_general_ci;
