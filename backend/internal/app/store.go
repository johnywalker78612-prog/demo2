package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Person struct {
	ShareCode               string     `json:"share_code,omitempty"`
	ID                      string     `json:"id"`
	FirstName               string     `json:"first_name"`
	LastName                string     `json:"last_name"`
	Email                   string     `json:"email,omitempty"`
	PhoneNumber             string     `json:"phone_number,omitempty"`
	DateOfBirth             string     `json:"date_of_birth,omitempty"`
	Nationality             string     `json:"nationality,omitempty"`
	PassportNumber          string     `json:"passport_number,omitempty"`
	RecordedStatus          string     `json:"recorded_status,omitempty"`
	AccompaniedBy           string     `json:"accompanied_by,omitempty"`
	ValidFrom               string     `json:"valid_from,omitempty"`
	ValidUntil              string     `json:"valid_until,omitempty"`
	EntriesAllowed          string     `json:"entries_allowed,omitempty"`
	NationalInsuranceNumber string     `json:"national_insurance_number,omitempty"`
	Unverified              bool       `json:"unverified"`
	EmailVerifiedAt         *time.Time `json:"email_verified_at,omitempty"`
	HasPhoto                bool       `json:"has_photo"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

// FullName is a display/search helper; JSON remains first_name and last_name.
func (p Person) FullName() string {
	return strings.TrimSpace(p.FirstName + " " + p.LastName)
}

// UnmarshalJSON preserves existing records written before names were separated.
// A full legacy name cannot be reliably split into given/family names, so retain
// it intact in FirstName until an administrator edits it. Explicit new fields win.
func (p *Person) UnmarshalJSON(data []byte) error {
	type personFields Person
	var decoded struct {
		personFields
		LegacyName string `json:"name"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(data, &present); err != nil {
		return err
	}
	*p = Person(decoded.personFields)
	_, hasFirst := present["first_name"]
	_, hasLast := present["last_name"]
	if !hasFirst && !hasLast {
		p.FirstName = strings.TrimSpace(decoded.LegacyName)
	}
	return nil
}

type Photo struct {
	Data        []byte `json:"data"`
	ContentType string `json:"content_type"`
}
type Share struct {
	ID        string     `json:"id"`
	PersonID  string     `json:"person_id"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// Hashes are stored separately so administrative list responses never disclose them.
type Verification struct {
	ID        string
	CodeHash  string
	Attempts  int
	PersonID  string
	Email     string
	ExpiresAt time.Time
}
type Message struct {
	LocalOnly   bool       `json:"local_only,omitempty"`
	HTML        string     `json:"html,omitempty"`
	ChallengeID string     `json:"challenge_id,omitempty"`
	ShareID     string     `json:"share_id,omitempty"`
	ExpiresAt   time.Time  `json:"expires_at,omitempty"`
	SentAt      *time.Time `json:"sent_at,omitempty"`
	PersonID    string     `json:"person_id"`
	ID          string     `json:"id"`
	To          string     `json:"to"`
	Subject     string     `json:"subject"`
	Body        string     `json:"body"`
	CreatedAt   time.Time  `json:"created_at"`
}
type AuditEvent struct {
	ID         string    `json:"id"`
	Action     string    `json:"action"`
	ResourceID string    `json:"resource_id,omitempty"`
	At         time.Time `json:"at"`
}
type database struct {
	AdminOTPs     map[string]adminOTP     `json:"admin_otps,omitempty"`
	People        map[string]Person       `json:"people"`
	Photos        map[string]Photo        `json:"photos"`
	Shares        map[string]Share        `json:"shares"`
	ShareHashes   map[string]string       `json:"share_hashes"`
	Verifications map[string]Verification `json:"verifications"`
	Outbox        map[string]Message      `json:"outbox"`
	Audit         []AuditEvent            `json:"audit"`
}

func emptyDB() database {
	return database{People: map[string]Person{}, Photos: map[string]Photo{}, Shares: map[string]Share{}, ShareHashes: map[string]string{}, Verifications: map[string]Verification{}, Outbox: map[string]Message{}, Audit: []AuditEvent{}}
}

type Store struct {
	mu      sync.RWMutex
	path    string
	aead    cipher.AEAD
	db      database
	pg      *sql.DB
	version int64
}

func OpenStore(path string, key []byte) (*Store, error) {
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
	s := &Store{path: path, aead: aead, db: emptyDB()}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) < aead.NonceSize() {
		return nil, errors.New("invalid encrypted database")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte("private-records-v1"))
	if err != nil {
		return nil, errors.New("cannot decrypt database: wrong key or corrupted file")
	}
	if err = json.Unmarshal(plain, &s.db); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Store) snapshot() database {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, _ := json.Marshal(s.db)
	var d database
	_ = json.Unmarshal(raw, &d)
	return d
}
func (s *Store) update(fn func(*database) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pg != nil {
		return s.updatePostgres(fn)
	}
	raw, err := json.Marshal(s.db)
	if err != nil {
		return err
	}
	var next database
	if err = json.Unmarshal(raw, &next); err != nil {
		return err
	}
	if err = fn(&next); err != nil {
		return err
	}
	raw, err = json.Marshal(next)
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	encrypted := s.aead.Seal(nonce, nonce, raw, []byte("private-records-v1"))
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".records-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(encrypted); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	s.db = next
	return nil
}
func event(d *database, action, id string) {
	d.Audit = append(d.Audit, AuditEvent{ID: randomToken("evt_"), Action: action, ResourceID: id, At: time.Now().UTC()})
}
