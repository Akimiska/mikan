-- +goose Up
-- 0043, as the beta 0.5.1.1-rc.11 had it, turned a REALITY template's max-time-difference of
-- 0 (no check of the client's clock) into 1 ms, a check almost every client fails. A value of
-- 1 ms comes from that alone (mihomo's microseconds below 1500): it goes back to 0.
UPDATE inbounds SET config = regexp_replace(config, '\n(\s+)max-time-difference: 1\n', chr(10) || '\1max-time-difference: 0' || chr(10))
WHERE config ~ '\n\s+max-time-difference: 1\n';

-- +goose Down
-- Nothing to undo: 0 is what the template had before 0043.
SELECT 1;
