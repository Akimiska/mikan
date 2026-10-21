-- +goose Up
-- The servers a tariff includes: every one (chosen_servers 0, as until now), or only the
-- chosen ones: nodes whole, with the protocols added to them later, and single protocols.
-- A client whose card lists protocols of its own keeps them over the tariff's.
ALTER TABLE tariffs ADD COLUMN chosen_servers BIGINT NOT NULL DEFAULT 0 CHECK (chosen_servers IN (0, 1));

CREATE TABLE tariff_nodes (
  tariff_id BIGINT NOT NULL REFERENCES tariffs(id) ON DELETE CASCADE,
  node_id   BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  PRIMARY KEY (tariff_id, node_id)
);
CREATE INDEX tariff_nodes_node ON tariff_nodes (node_id);

CREATE TABLE tariff_inbounds (
  tariff_id  BIGINT NOT NULL REFERENCES tariffs(id) ON DELETE CASCADE,
  inbound_id BIGINT NOT NULL REFERENCES inbounds(id) ON DELETE CASCADE,
  PRIMARY KEY (tariff_id, inbound_id)
);
CREATE INDEX tariff_inbounds_inbound ON tariff_inbounds (inbound_id);

-- +goose Down
DROP TABLE tariff_inbounds;
DROP TABLE tariff_nodes;
ALTER TABLE tariffs DROP COLUMN chosen_servers;
