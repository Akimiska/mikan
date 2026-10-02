package store

import (
	"context"
	"strings"
	"testing"
)

// The tables keyed by (user_id, time) are read and pruned by time alone: the primary key
// cannot serve that. With sequential scans disabled, PostgreSQL must have a usable
// time index even for a fresh empty fixture (where a sequential scan is usually cheaper).
func TestTimeQueriesUseTheirIndex(t *testing.T) {
	ctx := context.Background()
	st, err := OpenTest(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	conn, err := st.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SET enable_seqscan=off"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ sql, table, index string }{
		{"DELETE FROM traffic_hourly WHERE hour < 1", "traffic_hourly", "traffic_hourly_hour"},
		{"DELETE FROM traffic_daily WHERE day < 1", "traffic_daily", "traffic_daily_day"},
		{"SELECT hour, sum(up), sum(down) FROM traffic_hourly WHERE hour >= 1 GROUP BY hour ORDER BY hour", "traffic_hourly", "traffic_hourly_hour"},
		{"SELECT day, sum(up), sum(down) FROM traffic_daily WHERE day >= 1 GROUP BY day ORDER BY day", "traffic_daily", "traffic_daily_day"},
		{"SELECT u.id, sum(d.up + d.down) AS b FROM traffic_daily d JOIN users u ON u.id = d.user_id WHERE d.day >= 1 GROUP BY u.id ORDER BY b DESC LIMIT 5", "traffic_daily", "traffic_daily_day"},
		{"DELETE FROM devices WHERE last_seen < 1", "devices", "devices_last_seen"},
		{"DELETE FROM audit_log WHERE ts < 1", "audit_log", "audit_log_ts"},
	} {
		rows, err := conn.QueryContext(ctx, "EXPLAIN "+c.sql)
		if err != nil {
			t.Fatalf("%s: %v", c.sql, err)
		}
		var plan []string
		for rows.Next() {
			var detail string
			if err := rows.Scan(&detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		text := strings.Join(plan, "; ")
		if !strings.Contains(text, c.index) {
			t.Errorf("%s\n  plan: %s\n  want a search by %s", c.sql, text, c.index)
		}
		if strings.Contains(text, "SCAN "+c.table) && !strings.Contains(text, c.index) {
			t.Errorf("%s scans %s: %s", c.sql, c.table, text)
		}
	}
}
