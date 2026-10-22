-- +goose Up
-- The extended mode of a support grant (internal/panel/support): on top of its level, the
-- operations only the admin's own session may otherwise call, where money goes and what
-- lasts past the grant (a node's key, its address, an own certificate, the payment details,
-- the balances, an addon from outside mikan's catalog). The admin's own credentials, the
-- grants, the journal and the bot that tells the admin stay closed whatever the mode.
ALTER TABLE support_grants ADD COLUMN extended BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE support_grants DROP COLUMN extended;
