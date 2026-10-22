-- +goose Up
-- Load balancing: nodes of one balance group share their clients out. A node has room for
-- so many clients (capacity, NULL: no limit); each client of the group is kept on one node
-- of it, which is all its subscription shows of the group.
ALTER TABLE nodes ADD COLUMN capacity BIGINT;
ALTER TABLE nodes ADD COLUMN balance_group TEXT NOT NULL DEFAULT '';
CREATE TABLE node_assignments (
  user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  balance_group TEXT NOT NULL,
  node_id       BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  assigned_at   BIGINT NOT NULL,
  PRIMARY KEY (user_id, balance_group)
);
CREATE INDEX node_assignments_node ON node_assignments (node_id);

-- +goose Down
DROP TABLE node_assignments;
ALTER TABLE nodes DROP COLUMN balance_group;
ALTER TABLE nodes DROP COLUMN capacity;
