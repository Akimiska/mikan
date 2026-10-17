package store

import (
	"context"
	"testing"
)

// The users there already are get the source the records tell: a payment that made the
// user is a purchase in the bot, an old panel's link an import, the trial's own record a
// trial (which wins). Whoever left no trace is the admin's. The migration also goes back
// and forth, and a user is never lost with a folder.
func TestUserOrganisingMigrationFillsSources(t *testing.T) {
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
	// A panel from before the migration: roll it (and what came after) back.
	const migration = 13
	before := int64(0)
	for _, src := range p.ListSources() {
		if src.Version < migration {
			before = src.Version
		}
	}
	if _, err := p.DownTo(ctx, before); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"INSERT INTO tariffs(id,name,duration_days,created_at) VALUES(5,'t',30,1)",
		`INSERT INTO users(id,name,sub_token,period_start,created_at,updated_at) VALUES
		 (41,'plain','t41',1,1,1), (42,'bought','t42',1,1,1), (43,'renewed anew','t43',1,1,1), (44,'renewed','t44',1,1,1),
		 (45,'imported','t45',1,1,1), (46,'trial','t46',1,1,1), (47,'imported after a payment','t47',1,1,1)`,
		// 42: a 'new' payment; 43: a renewal that made the user (revert says so); 44: a renewal of a user the admin made.
		`INSERT INTO payments(id,provider,payload,tg_id,kind,user_id,tariff_id,tariff_name,amount,currency,status,pay_url,created_at,revert) VALUES
		 (1,'stars','p1',7,'new',42,5,'t',1,'XTR','applied','u',10,''),
		 (2,'stars','p2',7,'renew',43,5,'t',1,'XTR','applied','u',10,'{"created":true}'),
		 (3,'stars','p3',7,'renew',44,5,'t',1,'XTR','applied','u',10,'{"prior":{}}'),
		 (4,'stars','p4',7,'new',46,5,'t',1,'XTR','applied','u',10,''),
		 (5,'stars','p5',7,'new',NULL,5,'t',1,'XTR','pending','u',10,'')`,
		"INSERT INTO legacy_sub_tokens(token,user_id,source) VALUES('old45',45,'marzban'),('old47',47,'marzban')",
		"INSERT INTO trials(tg_id,user_id,tariff_id,created_at) VALUES(8,46,5,1),(9,NULL,5,1)",
	} {
		if _, err := s.DB.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.UpTo(ctx, migration); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT id, source, hidden, folder_id IS NULL FROM users ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[int64]string{41: "admin", 42: "bot", 43: "bot", 44: "admin", 45: "import", 46: "trial", 47: "import"}
	got := 0
	for rows.Next() {
		var id, hidden int64
		var source string
		var noFolder bool
		if err := rows.Scan(&id, &source, &hidden, &noFolder); err != nil {
			t.Fatal(err)
		}
		if source != want[id] || hidden != 0 || !noFolder {
			t.Errorf("user %d: source %q hidden %d, want %q and visible", id, source, hidden, want[id])
		}
		got++
	}
	if got != len(want) {
		t.Fatalf("%d users, want %d", got, len(want))
	}

	// A folder goes without taking its users along.
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO user_folders(id,name,created_at) VALUES(1,'f',1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE users SET folder_id = 1 WHERE id IN (41, 42)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "DELETE FROM user_folders WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	var left, free int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*), count(*) FILTER (WHERE folder_id IS NULL) FROM users").Scan(&left, &free); err != nil || left != len(want) || free != len(want) {
		t.Fatalf("users after a folder is deleted: %d, %d without a folder, %v", left, free, err)
	}
	// A source the database does not know is refused.
	if _, err := s.DB.ExecContext(ctx, "UPDATE users SET source = 'elsewhere' WHERE id = 41"); err == nil {
		t.Fatal("an unknown source was accepted")
	}
}
