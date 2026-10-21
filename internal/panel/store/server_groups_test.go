package store

import (
	"context"
	"testing"
)

// Migration 0044 turns the pools' limits and exclusions into the groups' modes with nothing
// changed for anyone: a limit is own, an exclusion closed, and a pool a tariff or a user had
// no row for (open without a limit) is written down as unlimited, since an unlisted group
// counts to the main traffic from now on. Down again, the exclusions come back.
func TestServerGroupsMigration(t *testing.T) {
	ctx := context.Background()
	s, err := OpenTest(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := postgresProvider(ctx, s.DB, postgresFS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 43); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO tariffs(id,name,duration_days,created_at) VALUES (91,'a',30,1), (92,'b',30,1)`)
	exec(`INSERT INTO traffic_pools(id,name,created_at) VALUES (81,'WL',1), (82,'Premium',1)`)
	exec(`INSERT INTO tariff_pools(tariff_id,pool_id,traffic_limit,excluded) VALUES (91,81,100,false), (91,82,0,true)`)
	exec(`INSERT INTO users(id,name,period_days,period_start,sub_token,created_at,updated_at) VALUES (71,'u',30,1,'tok71',1,1)`)
	exec(`INSERT INTO user_pools(user_id,pool_id,traffic_limit,used_up,excluded) VALUES (71,81,NULL,5,false), (71,82,NULL,0,true)`)
	if _, err := p.UpTo(ctx, 44); err != nil {
		t.Fatal(err)
	}
	modes := func(q string) map[[2]int64]string {
		t.Helper()
		rows, err := s.DB.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[[2]int64]string{}
		for rows.Next() {
			var a, b int64
			var mode string
			var limit *int64
			if err := rows.Scan(&a, &b, &mode, &limit); err != nil {
				t.Fatal(err)
			}
			if limit != nil {
				mode += ":" + itoa(*limit)
			}
			out[[2]int64{a, b}] = mode
		}
		return out
	}
	tp := modes(`SELECT tariff_id, pool_id, mode, traffic_limit FROM tariff_pools WHERE tariff_id IN (91,92)`)
	want := map[[2]int64]string{{91, 81}: "own:100", {91, 82}: "closed", {92, 81}: "unlimited", {92, 82}: "unlimited"}
	for k, v := range want {
		if tp[k] != v {
			t.Fatalf("tariff %d, pool %d: %q, want %q (all %v)", k[0], k[1], tp[k], v, tp)
		}
	}
	up := modes(`SELECT user_id, pool_id, mode, traffic_limit FROM user_pools WHERE user_id = 71`)
	if up[[2]int64{71, 81}] != "unlimited" || up[[2]int64{71, 82}] != "closed" {
		t.Fatalf("user pools: %v", up)
	}
	var used int64
	if err := s.DB.QueryRowContext(ctx, `SELECT used_up FROM user_pools WHERE user_id = 71 AND pool_id = 81`).Scan(&used); err != nil || used != 5 {
		t.Fatalf("what was used stays: %d %v", used, err)
	}

	if _, err := p.DownTo(ctx, 43); err != nil {
		t.Fatal(err)
	}
	var excluded bool
	if err := s.DB.QueryRowContext(ctx, `SELECT excluded FROM tariff_pools WHERE tariff_id = 91 AND pool_id = 82`).Scan(&excluded); err != nil || !excluded {
		t.Fatalf("down: the closed pool is excluded again: %v %v", excluded, err)
	}
	if _, err := p.UpTo(ctx, 44); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
