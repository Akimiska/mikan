-- +goose Up
-- The deprecated bot.* events carried a whole payment and a whole subscription, its link
-- included; nothing writes or reads them any more, and what was written goes with them.
DELETE FROM addon_events WHERE type LIKE 'bot.%';

-- +goose Down
SELECT 1;
