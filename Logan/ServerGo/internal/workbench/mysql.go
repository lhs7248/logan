package workbench

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"
)

// MySQLStore uses an explicitly migrated namespace row. A dedicated advisory
// lock connection fences whole-runtime workers and a revision CAS rejects stale
// state. This first adapter intentionally allows one workbench writer per
// namespace; it does not claim multi-instance job execution or horizontal scale.
type MySQLStore struct {
	db                  *gorm.DB
	lease               *sql.Conn
	namespace, lockName string
	revision            int64
	mu                  sync.Mutex
	closed              bool
}

func NewMySQLStore(db *gorm.DB, namespace string) (*MySQLStore, error) {
	if db == nil || namespace == "" || len(namespace) > 128 {
		return nil, errors.New("database and bounded workbench namespace required")
	}
	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, err
	}
	name := "workbench:" + digest([]byte(namespace))[:48]
	var locked int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", name).Scan(&locked); err != nil || locked != 1 {
		conn.Close()
		return nil, errors.New("workbench database lease unavailable")
	}
	return &MySQLStore{db: db, lease: conn, namespace: namespace, lockName: name}, nil
}
func (m *MySQLStore) Load() (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("store closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var revision int64
	var payload []byte
	err := m.db.WithContext(ctx).Raw("SELECT revision, payload FROM workbench_state WHERE namespace = ?", m.namespace).Row().Scan(&revision, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		m.revision = 0
		return emptyState(), nil
	}
	if err != nil {
		return nil, err
	}
	state := emptyState()
	if err = json.Unmarshal(payload, state); err != nil || state.Objects == nil || state.Idempotency == nil {
		return nil, errors.New("invalid database state")
	}
	m.revision = revision
	return state, nil
}
func (m *MySQLStore) Save(state *State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("store closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var held int
	if err := m.lease.QueryRowContext(ctx, "SELECT IS_USED_LOCK(?) = CONNECTION_ID()", m.lockName).Scan(&held); err != nil || held != 1 {
		return errors.New("database runtime lease lost")
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	next := m.revision + 1
	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if m.revision == 0 {
			return tx.Exec("INSERT INTO workbench_state (namespace, revision, payload) VALUES (?, ?, ?)", m.namespace, next, payload).Error
		}
		result := tx.Exec("UPDATE workbench_state SET payload = ?, revision = ? WHERE namespace = ? AND revision = ?", payload, next, m.namespace, m.revision)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("database state revision conflict")
		}
		return nil
	})
	if err == nil {
		m.revision = next
	}
	return err
}
func (m *MySQLStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var released int
	_ = m.lease.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", m.lockName).Scan(&released)
	return m.lease.Close()
}
