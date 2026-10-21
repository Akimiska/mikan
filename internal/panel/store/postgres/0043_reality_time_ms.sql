-- +goose Up
-- The nodes' core (Citrus) reads a REALITY inbound's max-time-difference in milliseconds,
-- like Xray; mihomo read microseconds. A template that set its own value was written for
-- mihomo and would now let a client's clock be a thousand times further off, so a
-- recorded ClientHello replays for days: the value becomes milliseconds (at least 1).
-- Templates are stored as proto.Marshal writes them, the key indented under
-- reality-config.
UPDATE inbounds SET config = regexp_replace(config,
    '\n(\s+)max-time-difference: [0-9]+\n',
    chr(10) || '\1max-time-difference: ' ||
        GREATEST(1, round((substring(config FROM '\n\s+max-time-difference: ([0-9]+)\n'))::numeric / 1000))::bigint::text || chr(10))
WHERE config ~ '\n\s+max-time-difference: [0-9]+\n';

-- +goose Down
UPDATE inbounds SET config = regexp_replace(config,
    '\n(\s+)max-time-difference: [0-9]+\n',
    chr(10) || '\1max-time-difference: ' ||
        ((substring(config FROM '\n\s+max-time-difference: ([0-9]+)\n'))::numeric * 1000)::bigint::text || chr(10))
WHERE config ~ '\n\s+max-time-difference: [0-9]+\n';
