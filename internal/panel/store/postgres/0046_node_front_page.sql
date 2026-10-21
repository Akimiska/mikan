-- +goose Up
-- The built-in page (frontpage.Templates) a stranger gets over https on the node's own
-- domain, or on its address with no name, through the node's shared port 443: "" none.
ALTER TABLE nodes ADD COLUMN front_page TEXT NOT NULL DEFAULT '' CHECK (front_page IN ('', 'maintenance', 'cats'));

-- +goose Down
ALTER TABLE nodes DROP COLUMN front_page;
