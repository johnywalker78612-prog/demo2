package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// This adapter preserves the encrypted local format for a small, single-service
// deployment. All mutations lock one row; it is not a large-dataset SQL model.
const maxCloudSnapshot = 32 << 20

//go:embed migrations/001_cloud_store.sql
var cloudStoreMigration string

func connectPostgres(url string) (*sql.DB, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, errors.New("invalid database connection configuration")
	}
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(time.Minute)
	db.SetConnMaxLifetime(5 * time.Minute)
	return db, nil
}

// MigratePostgres uses the direct URL; ordinary application traffic can be pooled.
func MigratePostgres(url string) error {
	db, err := connectPostgres(url)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = db.ExecContext(ctx, cloudStoreMigration)
	if err != nil {
		return errors.New("database migration failed; check direct connection and permissions")
	}
	return nil
}

func OpenPostgresStore(url string, key []byte) (*Store, error) {
	if len(key) != 32 {
		return nil, errors.New("data key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	db, err := connectPostgres(url)
	if err != nil {
		return nil, err
	}
	s := &Store{pg: db, aead: aead, db: emptyDB()}
	raw, err := s.encryptDatabase(s.db)
	if err != nil {
		db.Close()
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = db.ExecContext(ctx, `INSERT INTO private_records_cloud_state (id, payload) VALUES (1, $1) ON CONFLICT (id) DO NOTHING`, raw)
	if err != nil {
		db.Close()
		return nil, errors.New("cannot open cloud store; check connection and run -migrate first")
	}
	if err = s.refresh(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s.pg != nil {
		return s.pg.Close()
	}
	return nil
}

func (s *Store) encryptDatabase(d database) ([]byte, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxCloudSnapshot {
		return nil, errors.New("cloud store exceeds the 32 MiB demo limit")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, raw, []byte("private-records-v1")), nil
}

func (s *Store) decryptDatabase(raw []byte) (database, error) {
	d := emptyDB()
	n := s.aead.NonceSize()
	if len(raw) < n || len(raw) > maxCloudSnapshot+n+s.aead.Overhead() {
		return d, errors.New("invalid cloud snapshot size")
	}
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], []byte("private-records-v1"))
	if err != nil {
		return d, errors.New("cannot decrypt cloud database; check RECORDS_DATA_KEY")
	}
	err = json.Unmarshal(plain, &d)
	return d, err
}

// Refresh at request boundaries; fetch the payload only when its version changes.
func (s *Store) refresh(ctx context.Context) error {
	if s.pg == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var version int64
	var raw []byte
	err := s.pg.QueryRowContext(ctx, `SELECT version, CASE WHEN version <> $1 THEN payload ELSE NULL END FROM private_records_cloud_state WHERE id = 1`, s.version).Scan(&version, &raw)
	if err != nil {
		return errors.New("cloud store unavailable")
	}
	if version == s.version {
		return nil
	}
	d, err := s.decryptDatabase(raw)
	if err != nil {
		return err
	}
	s.db, s.version = d, version
	return nil
}

// Caller holds s.mu. Read latest state under a row lock, so overlapping Render
// deploys cannot overwrite each other's updates with an older in-memory copy.
func (s *Store) updatePostgres(fn func(*database) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := s.pg.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("cloud store unavailable")
	}
	defer tx.Rollback()
	var raw []byte
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT payload, version FROM private_records_cloud_state WHERE id = 1 FOR UPDATE`).Scan(&raw, &version); err != nil {
		return errors.New("cannot load cloud store")
	}
	d, err := s.decryptDatabase(raw)
	if err != nil {
		return err
	}
	if err = fn(&d); err != nil {
		return err
	}
	raw, err = s.encryptDatabase(d)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE private_records_cloud_state SET payload = $1, version = version + 1 WHERE id = 1`, raw); err != nil {
		return errors.New("cannot save cloud store")
	}
	if err = tx.Commit(); err != nil {
		return errors.New("cloud store commit failed")
	}
	s.db, s.version = d, version+1
	return nil
}

// ImportLocalFile refuses to replace any existing cloud data. The local file and
// encryption key are retained unchanged; the separate Neon API is untouched.
func (s *Store) ImportLocalFile(path string, key []byte) error {
	if s.pg == nil {
		return errors.New("import requires DATABASE_URL")
	}
	local, err := OpenStore(path, key)
	if err != nil {
		return err
	}
	return s.update(func(d *database) error {
		if len(d.People)+len(d.Photos)+len(d.Shares)+len(d.ShareHashes)+len(d.Verifications)+len(d.Outbox)+len(d.Audit) != 0 {
			return errors.New("cloud store is not empty; refusing to overwrite")
		}
		*d = local.snapshot()
		return nil
	})
}
