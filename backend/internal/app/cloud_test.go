package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Opt-in integration test. Creates/drops only its own random schema; use an
// isolated Neon branch. No real data or email recipients are used.
func TestPostgresPersistence(t *testing.T) {
	rawURL := os.Getenv("RECORDS_TEST_DATABASE_URL")
	if rawURL == "" {
		t.Skip("set RECORDS_TEST_DATABASE_URL to an isolated Postgres branch")
	}
	control, err := connectPostgres(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	schema := "records_test_" + hash(randomToken(""))[:16]
	if _, err := control.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	defer control.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	connection := u.String()
	if err := MigratePostgres(connection); err != nil {
		t.Fatal(err)
	}
	if err := MigratePostgres(connection); err != nil {
		t.Fatal(err)
	}
	key := []byte(strings.Repeat("k", 32))
	a, err := OpenPostgresStore(connection, key)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	localPath := filepath.Join(t.TempDir(), "records.enc")
	local, err := OpenStore(localPath, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := local.update(func(d *database) error {
		d.People["fictional"] = Person{ID: "fictional", FirstName: "Example"}
		d.Photos["fictional"] = Photo{Data: []byte("sample"), ContentType: "image/png"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.ImportLocalFile(localPath, key); err != nil {
		t.Fatal(err)
	}
	if err := a.ImportLocalFile(localPath, key); err == nil {
		t.Fatal("import overwrote cloud records")
	}
	b, err := OpenPostgresStore(connection, key)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.snapshot().People["fictional"].FirstName != "Example" || string(b.snapshot().Photos["fictional"].Data) != "sample" {
		t.Fatal("data did not survive reopening")
	}
	if wrong, err := OpenPostgresStore(connection, []byte(strings.Repeat("x", 32))); err == nil {
		wrong.Close()
		t.Fatal("wrong encryption key accepted")
	}
	var wg sync.WaitGroup
	for _, s := range []*Store{a, b} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			for i := 0; i < 4; i++ {
				if err := s.update(func(d *database) error { event(d, "test", "fictional"); return nil }); err != nil {
					t.Error(err)
					return
				}
			}
		}(s)
	}
	wg.Wait()
	if err := a.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(a.snapshot().Audit) != 8 {
		t.Fatal("concurrent writers lost updates")
	}
	if err := a.update(func(d *database) error { delete(d.People, "fictional"); return errors.New("abort") }); err == nil {
		t.Fatal("expected error")
	}
	if err := b.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.snapshot().People["fictional"]; !ok {
		t.Fatal("failed mutation committed")
	}
	srv, err := New(b, Config{AdminPassword: strings.Repeat("p", 12), APIToken: strings.Repeat("t", 32)})
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/admin", nil))
	if w.Code != 503 {
		t.Fatalf("unavailable database served stale data: %d", w.Code)
	}
}
