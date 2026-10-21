-- +goose Up
-- The nodes' core (Citrus) reads a REALITY inbound's max-time-difference in milliseconds,
-- like Xray; mihomo read microseconds. A template that set its own value was written for
-- mihomo and would now let a client's clock be a thousand times further off, so a
-- recorded ClientHello replays for days: the value becomes milliseconds (at least 1), and 0,
-- no check at all, stays 0. Templates are stored as proto.Marshal writes them, the key
-- indented under reality-config.
UPDATE inbounds SET config = regexp_replace(config,
    '\n(\s+)max-time-difference: [0-9]+\n',
    chr(10) || '\1max-time-difference: ' ||
        (SELECT CASE WHEN v = 0 THEN 0 ELSE GREATEST(1, round(v / 1000)) END
         FROM (SELECT (substring(config FROM '\n\s+max-time-difference: ([0-9]+)\n'))::numeric AS v) AS t)::bigint::text || chr(10))
WHERE config ~ '\n\s+max-time-difference: [0-9]+\n';

-- +goose Down
UPDATE inbounds SET config = regexp_replace(config,
    '\n(\s+)max-time-difference: [0-9]+\n',
    chr(10) || '\1max-time-difference: ' ||
        ((substring(config FROM '\n\s+max-time-difference: ([0-9]+)\n'))::numeric * 1000)::bigint::text || chr(10))
WHERE config ~ '\n\s+max-time-difference: [0-9]+\n';
