package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	appErr "sleigh-runtime/server/internal/errors"

	_ "modernc.org/sqlite"
)

type SandboxRecord struct {
	ID           string
	SessionID    string
	Image        string
	Status       string
	Labels       map[string]string
	Created      string
	LastAccessed string
}

type SnapshotRecord struct {
	ID        string
	SandboxID string
	ImageRef  string
	Type      string
	HostPath  string
	BaseID    string
	Created   string
}

type MountRecord struct {
	ID            string
	SandboxID     string
	HostPath      string
	ContainerPath string
	Mode          string
	Created       string
}

type WarmPoolEntry struct {
	SandboxID string
	Image     string
	MemoryMB  int64
	CreatedAt string
}

type ExecTaskRecord struct {
	ID          string
	SandboxID   string
	Command     string
	Status      string
	Stdout      string
	Stderr      string
	ExitCode    *int
	Error       string
	Recovery    string
	StartedAt   string
	CompletedAt string
}

type Store struct {
	db *sql.DB
}

func Open(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Reduce SQLITE_BUSY under concurrent API traffic.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	if _, err := db.ExecContext(context.Background(), `PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set sqlite busy_timeout pragma: %w", err)
	}
	_, _ = db.ExecContext(context.Background(), `PRAGMA journal_mode = WAL`)
	_, _ = db.ExecContext(context.Background(), `PRAGMA synchronous = NORMAL`)

	if err := migrate(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CreateSandbox(ctx context.Context, record SandboxRecord) error {
	labels, err := json.Marshal(record.Labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}

	const query = `
INSERT INTO sandboxes (id, session_id, image, status, labels_json, created_at, updated_at, last_accessed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`
	_, err = s.db.ExecContext(
		ctx,
		query,
		record.ID,
		record.SessionID,
		record.Image,
		record.Status,
		string(labels),
		record.Created,
		record.Created,
		record.Created,
	)
	if err != nil {
		return fmt.Errorf("insert sandbox: %w", err)
	}

	return nil
}

func (s *Store) GetSandbox(ctx context.Context, id string) (SandboxRecord, error) {
	const query = `
SELECT id, session_id, image, status, labels_json, created_at, last_accessed_at
FROM sandboxes
WHERE id = ?
`
	row := s.db.QueryRowContext(ctx, query, id)

	var (
		record     SandboxRecord
		labelsJSON string
	)
	err := row.Scan(
		&record.ID,
		&record.SessionID,
		&record.Image,
		&record.Status,
		&labelsJSON,
		&record.Created,
		&record.LastAccessed,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return SandboxRecord{}, appErr.ErrNotFound
		}
		return SandboxRecord{}, fmt.Errorf("query sandbox: %w", err)
	}

	if labelsJSON != "" {
		if err := json.Unmarshal([]byte(labelsJSON), &record.Labels); err != nil {
			return SandboxRecord{}, fmt.Errorf("unmarshal labels: %w", err)
		}
	}

	return record, nil
}

func (s *Store) UpdateSandboxStatus(ctx context.Context, id, status string) error {
	const query = `
UPDATE sandboxes
SET status = ?, updated_at = ?
WHERE id = ?
`
	result, err := s.db.ExecContext(ctx, query, status, now(), id)
	if err != nil {
		return fmt.Errorf("update sandbox status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return appErr.ErrNotFound
	}

	return nil
}

func (s *Store) UpdateSandboxImage(ctx context.Context, id, image string) error {
	const query = `
UPDATE sandboxes
SET image = ?, updated_at = ?
WHERE id = ?
`
	result, err := s.db.ExecContext(ctx, query, image, now(), id)
	if err != nil {
		return fmt.Errorf("update sandbox image: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return appErr.ErrNotFound
	}

	return nil
}

func (s *Store) UpdateSandboxAssignment(
	ctx context.Context,
	id string,
	sessionID string,
	labels map[string]string,
) error {
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("marshal assignment labels: %w", err)
	}
	const query = `
UPDATE sandboxes
SET session_id = ?, labels_json = ?, updated_at = ?
WHERE id = ?
`
	result, err := s.db.ExecContext(ctx, query, sessionID, string(labelsJSON), now(), id)
	if err != nil {
		return fmt.Errorf("update sandbox assignment: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("assignment rows affected: %w", err)
	}
	if rows == 0 {
		return appErr.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSandbox(ctx context.Context, id string) error {
	const query = `DELETE FROM sandboxes WHERE id = ?`
	result, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete sandbox: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return appErr.ErrNotFound
	}

	return nil
}

func (s *Store) CreateMount(ctx context.Context, record MountRecord) error {
	const query = `
INSERT INTO sandbox_mounts (id, sandbox_id, host_path, container_path, mode, created_at)
VALUES (?, ?, ?, ?, ?, ?)
`
	_, err := s.db.ExecContext(
		ctx,
		query,
		record.ID,
		record.SandboxID,
		record.HostPath,
		record.ContainerPath,
		record.Mode,
		record.Created,
	)
	if err != nil {
		return fmt.Errorf("insert sandbox mount: %w", err)
	}
	return nil
}

func (s *Store) DeleteMount(ctx context.Context, sandboxID, mountID string) error {
	const query = `
DELETE FROM sandbox_mounts
WHERE sandbox_id = ? AND id = ?
`
	result, err := s.db.ExecContext(ctx, query, sandboxID, mountID)
	if err != nil {
		return fmt.Errorf("delete sandbox mount: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("mount delete rows affected: %w", err)
	}
	if rows == 0 {
		return appErr.ErrNotFound
	}
	return nil
}

func (s *Store) ListMounts(ctx context.Context, sandboxID string) ([]MountRecord, error) {
	const query = `
SELECT id, sandbox_id, host_path, container_path, mode, created_at
FROM sandbox_mounts
WHERE sandbox_id = ?
ORDER BY created_at ASC
`
	rows, err := s.db.QueryContext(ctx, query, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("query sandbox mounts: %w", err)
	}
	defer rows.Close()

	result := make([]MountRecord, 0)
	for rows.Next() {
		var record MountRecord
		if err := rows.Scan(
			&record.ID,
			&record.SandboxID,
			&record.HostPath,
			&record.ContainerPath,
			&record.Mode,
			&record.Created,
		); err != nil {
			return nil, fmt.Errorf("scan sandbox mount: %w", err)
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sandbox mounts: %w", err)
	}
	return result, nil
}

func (s *Store) ListSandboxesBySession(ctx context.Context, sessionID string) ([]SandboxRecord, error) {
	const query = `
SELECT id, session_id, image, status, labels_json, created_at, last_accessed_at
FROM sandboxes
WHERE session_id = ?
ORDER BY created_at ASC
`
	rows, err := s.db.QueryContext(ctx, query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query sandboxes by session: %w", err)
	}
	defer rows.Close()

	records := make([]SandboxRecord, 0)
	for rows.Next() {
		var (
			record     SandboxRecord
			labelsJSON string
		)
		if err := rows.Scan(
			&record.ID,
			&record.SessionID,
			&record.Image,
			&record.Status,
			&labelsJSON,
			&record.Created,
			&record.LastAccessed,
		); err != nil {
			return nil, fmt.Errorf("scan sandbox by session: %w", err)
		}
		if labelsJSON != "" {
			if err := json.Unmarshal([]byte(labelsJSON), &record.Labels); err != nil {
				return nil, fmt.Errorf("unmarshal sandbox labels: %w", err)
			}
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sandboxes by session: %w", err)
	}

	return records, nil
}

func (s *Store) CreateWarmPoolEntry(ctx context.Context, entry WarmPoolEntry) error {
	const query = `
INSERT INTO warm_pool_entries (sandbox_id, image, memory_mb, created_at)
VALUES (?, ?, ?, ?)
`
	_, err := s.db.ExecContext(
		ctx,
		query,
		entry.SandboxID,
		entry.Image,
		entry.MemoryMB,
		entry.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert warm pool entry: %w", err)
	}
	return nil
}

func (s *Store) AcquireWarmPoolEntry(ctx context.Context, image string, memoryMB int64) (string, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, fmt.Errorf("begin warm pool acquire tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	const selectQuery = `
SELECT sandbox_id
FROM warm_pool_entries
WHERE image = ? AND memory_mb = ?
ORDER BY created_at ASC
LIMIT 1
`
	var sandboxID string
	err = tx.QueryRowContext(ctx, selectQuery, image, memoryMB).Scan(&sandboxID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, fmt.Errorf("query warm pool entry: %w", err)
	}

	const deleteQuery = `
DELETE FROM warm_pool_entries
WHERE sandbox_id = ?
`
	if _, err := tx.ExecContext(ctx, deleteQuery, sandboxID); err != nil {
		return "", false, fmt.Errorf("delete warm pool entry: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", false, fmt.Errorf("commit warm pool acquire tx: %w", err)
	}
	return sandboxID, true, nil
}

func (s *Store) CountWarmPoolAvailable(ctx context.Context, image string, memoryMB int64) (int, error) {
	const query = `
SELECT COUNT(1)
FROM warm_pool_entries
WHERE image = ? AND memory_mb = ?
`
	var count int
	if err := s.db.QueryRowContext(ctx, query, image, memoryMB).Scan(&count); err != nil {
		return 0, fmt.Errorf("count warm pool entries: %w", err)
	}
	return count, nil
}

func (s *Store) UpdateSandboxLastAccess(ctx context.Context, id, accessedAt string) error {
	const query = `
UPDATE sandboxes
SET last_accessed_at = ?, updated_at = ?
WHERE id = ?
`
	result, err := s.db.ExecContext(ctx, query, accessedAt, now(), id)
	if err != nil {
		return fmt.Errorf("update sandbox last access: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("last access rows affected: %w", err)
	}
	if rows == 0 {
		return appErr.ErrNotFound
	}
	return nil
}

func (s *Store) ListIdleSandboxesBefore(ctx context.Context, before string) ([]SandboxRecord, error) {
	const query = `
SELECT id, session_id, image, status, labels_json, created_at, last_accessed_at
FROM sandboxes
WHERE session_id <> '' AND last_accessed_at <> '' AND last_accessed_at < ?
ORDER BY last_accessed_at ASC
`
	rows, err := s.db.QueryContext(ctx, query, before)
	if err != nil {
		return nil, fmt.Errorf("query idle sandboxes: %w", err)
	}
	defer rows.Close()

	result := make([]SandboxRecord, 0)
	for rows.Next() {
		var (
			record     SandboxRecord
			labelsJSON string
		)
		if err := rows.Scan(
			&record.ID,
			&record.SessionID,
			&record.Image,
			&record.Status,
			&labelsJSON,
			&record.Created,
			&record.LastAccessed,
		); err != nil {
			return nil, fmt.Errorf("scan idle sandbox: %w", err)
		}
		if labelsJSON != "" {
			if err := json.Unmarshal([]byte(labelsJSON), &record.Labels); err != nil {
				return nil, fmt.Errorf("unmarshal idle sandbox labels: %w", err)
			}
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate idle sandboxes: %w", err)
	}
	return result, nil
}

func (s *Store) CreateSnapshot(ctx context.Context, snapshot SnapshotRecord) error {
	snapshotType := snapshot.Type
	if snapshotType == "" {
		snapshotType = "container"
	}
	const query = `
INSERT INTO snapshots (id, sandbox_id, image_ref, snapshot_type, source_host_path, base_snapshot_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
`
	_, err := s.db.ExecContext(
		ctx,
		query,
		snapshot.ID,
		snapshot.SandboxID,
		snapshot.ImageRef,
		snapshotType,
		snapshot.HostPath,
		snapshot.BaseID,
		snapshot.Created,
	)
	if err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}

	return nil
}

func (s *Store) ListSnapshots(ctx context.Context, sandboxID string) ([]SnapshotRecord, error) {
	const query = `
SELECT id, sandbox_id, image_ref, snapshot_type, source_host_path, base_snapshot_id, created_at
FROM snapshots
WHERE sandbox_id = ?
ORDER BY created_at DESC
`
	rows, err := s.db.QueryContext(ctx, query, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("query snapshots: %w", err)
	}
	defer rows.Close()

	snapshots := make([]SnapshotRecord, 0)
	for rows.Next() {
		var snapshot SnapshotRecord
		if err := rows.Scan(
			&snapshot.ID,
			&snapshot.SandboxID,
			&snapshot.ImageRef,
			&snapshot.Type,
			&snapshot.HostPath,
			&snapshot.BaseID,
			&snapshot.Created,
		); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate snapshots: %w", err)
	}

	return snapshots, nil
}

func (s *Store) GetSnapshot(ctx context.Context, sandboxID, snapshotID string) (SnapshotRecord, error) {
	const query = `
SELECT id, sandbox_id, image_ref, snapshot_type, source_host_path, base_snapshot_id, created_at
FROM snapshots
WHERE sandbox_id = ? AND id = ?
`
	row := s.db.QueryRowContext(ctx, query, sandboxID, snapshotID)

	var snapshot SnapshotRecord
	if err := row.Scan(
		&snapshot.ID,
		&snapshot.SandboxID,
		&snapshot.ImageRef,
		&snapshot.Type,
		&snapshot.HostPath,
		&snapshot.BaseID,
		&snapshot.Created,
	); err != nil {
		if err == sql.ErrNoRows {
			return SnapshotRecord{}, appErr.ErrNotFound
		}
		return SnapshotRecord{}, fmt.Errorf("query snapshot: %w", err)
	}

	return snapshot, nil
}

func (s *Store) GetLatestWorkspaceSnapshot(ctx context.Context, sandboxID string) (SnapshotRecord, error) {
	const query = `
SELECT id, sandbox_id, image_ref, snapshot_type, source_host_path, base_snapshot_id, created_at
FROM snapshots
WHERE sandbox_id = ? AND snapshot_type = 'workspace'
ORDER BY created_at DESC
LIMIT 1
`
	row := s.db.QueryRowContext(ctx, query, sandboxID)
	var snapshot SnapshotRecord
	if err := row.Scan(
		&snapshot.ID,
		&snapshot.SandboxID,
		&snapshot.ImageRef,
		&snapshot.Type,
		&snapshot.HostPath,
		&snapshot.BaseID,
		&snapshot.Created,
	); err != nil {
		if err == sql.ErrNoRows {
			return SnapshotRecord{}, appErr.ErrNotFound
		}
		return SnapshotRecord{}, fmt.Errorf("query latest workspace snapshot: %w", err)
	}
	return snapshot, nil
}

func (s *Store) CreateExecTask(ctx context.Context, record ExecTaskRecord) error {
	const query = `
INSERT INTO exec_tasks (
  id, sandbox_id, command, status, stdout, stderr, exit_code, error, recovery, started_at, completed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`
	var exitCode any
	if record.ExitCode != nil {
		exitCode = *record.ExitCode
	}

	_, err := s.db.ExecContext(
		ctx,
		query,
		record.ID,
		record.SandboxID,
		record.Command,
		record.Status,
		record.Stdout,
		record.Stderr,
		exitCode,
		record.Error,
		record.Recovery,
		record.StartedAt,
		nullableString(record.CompletedAt),
	)
	if err != nil {
		return fmt.Errorf("insert exec task: %w", err)
	}
	return nil
}

func (s *Store) UpdateExecTask(ctx context.Context, record ExecTaskRecord) error {
	const query = `
UPDATE exec_tasks
SET status = ?, stdout = ?, stderr = ?, exit_code = ?, error = ?, recovery = ?, completed_at = ?
WHERE id = ? AND sandbox_id = ?
`
	var exitCode any
	if record.ExitCode != nil {
		exitCode = *record.ExitCode
	}

	result, err := s.db.ExecContext(
		ctx,
		query,
		record.Status,
		record.Stdout,
		record.Stderr,
		exitCode,
		record.Error,
		record.Recovery,
		nullableString(record.CompletedAt),
		record.ID,
		record.SandboxID,
	)
	if err != nil {
		return fmt.Errorf("update exec task: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return appErr.ErrNotFound
	}
	return nil
}

func (s *Store) GetExecTask(ctx context.Context, sandboxID, execID string) (ExecTaskRecord, error) {
	const query = `
SELECT id, sandbox_id, command, status, stdout, stderr, exit_code, error, recovery, started_at, completed_at
FROM exec_tasks
WHERE sandbox_id = ? AND id = ?
`
	row := s.db.QueryRowContext(ctx, query, sandboxID, execID)

	var (
		record    ExecTaskRecord
		exitCode  sql.NullInt64
		completed sql.NullString
	)
	if err := row.Scan(
		&record.ID,
		&record.SandboxID,
		&record.Command,
		&record.Status,
		&record.Stdout,
		&record.Stderr,
		&exitCode,
		&record.Error,
		&record.Recovery,
		&record.StartedAt,
		&completed,
	); err != nil {
		if err == sql.ErrNoRows {
			return ExecTaskRecord{}, appErr.ErrNotFound
		}
		return ExecTaskRecord{}, fmt.Errorf("query exec task: %w", err)
	}
	if exitCode.Valid {
		value := int(exitCode.Int64)
		record.ExitCode = &value
	}
	if completed.Valid {
		record.CompletedAt = completed.String
	}
	return record, nil
}

func (s *Store) GetLatestExecTaskBySandbox(ctx context.Context, sandboxID string) (ExecTaskRecord, error) {
	const query = `
SELECT id, sandbox_id, command, status, stdout, stderr, exit_code, error, recovery, started_at, completed_at
FROM exec_tasks
WHERE sandbox_id = ?
ORDER BY started_at DESC
LIMIT 1
`
	row := s.db.QueryRowContext(ctx, query, sandboxID)

	var (
		record    ExecTaskRecord
		exitCode  sql.NullInt64
		completed sql.NullString
	)
	if err := row.Scan(
		&record.ID,
		&record.SandboxID,
		&record.Command,
		&record.Status,
		&record.Stdout,
		&record.Stderr,
		&exitCode,
		&record.Error,
		&record.Recovery,
		&record.StartedAt,
		&completed,
	); err != nil {
		if err == sql.ErrNoRows {
			return ExecTaskRecord{}, appErr.ErrNotFound
		}
		return ExecTaskRecord{}, fmt.Errorf("query latest exec task: %w", err)
	}
	if exitCode.Valid {
		value := int(exitCode.Int64)
		record.ExitCode = &value
	}
	if completed.Valid {
		record.CompletedAt = completed.String
	}
	return record, nil
}

func (s *Store) NextSessionSequence(ctx context.Context, sessionID string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx for session sequence: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	const upsert = `
INSERT INTO session_event_sequences (session_id, last_seq)
VALUES (?, 0)
ON CONFLICT(session_id) DO NOTHING
`
	if _, err := tx.ExecContext(ctx, upsert, sessionID); err != nil {
		return 0, fmt.Errorf("upsert session sequence: %w", err)
	}

	const increment = `
UPDATE session_event_sequences
SET last_seq = last_seq + 1
WHERE session_id = ?
`
	if _, err := tx.ExecContext(ctx, increment, sessionID); err != nil {
		return 0, fmt.Errorf("increment session sequence: %w", err)
	}

	const query = `SELECT last_seq FROM session_event_sequences WHERE session_id = ?`
	var seq int64
	if err := tx.QueryRowContext(ctx, query, sessionID).Scan(&seq); err != nil {
		return 0, fmt.Errorf("query session sequence: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit session sequence tx: %w", err)
	}
	return seq, nil
}

func (s *Store) ListExecTasksBySession(
	ctx context.Context,
	sessionID string,
	limit int,
	cursorStartedAt string,
	cursorExecID string,
) ([]ExecTaskRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	baseSelect := `
SELECT e.id, e.sandbox_id, e.command, e.status, e.stdout, e.stderr, e.exit_code, e.error, e.recovery, e.started_at, e.completed_at
FROM exec_tasks e
JOIN sandboxes s ON s.id = e.sandbox_id
WHERE s.session_id = ?
`

	withCursor := `
AND ((e.started_at < ?) OR (e.started_at = ? AND e.id < ?))
ORDER BY e.started_at DESC, e.id DESC
LIMIT ?
`
	withoutCursor := `
ORDER BY e.started_at DESC, e.id DESC
LIMIT ?
`

	var (
		rows *sql.Rows
		err  error
	)
	if cursorStartedAt != "" && cursorExecID != "" {
		rows, err = s.db.QueryContext(
			ctx,
			baseSelect+withCursor,
			sessionID,
			cursorStartedAt,
			cursorStartedAt,
			cursorExecID,
			limit,
		)
	} else {
		rows, err = s.db.QueryContext(
			ctx,
			baseSelect+withoutCursor,
			sessionID,
			limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("query exec tasks by session: %w", err)
	}
	defer rows.Close()

	result := make([]ExecTaskRecord, 0)
	for rows.Next() {
		var (
			record    ExecTaskRecord
			exitCode  sql.NullInt64
			completed sql.NullString
		)
		if err := rows.Scan(
			&record.ID,
			&record.SandboxID,
			&record.Command,
			&record.Status,
			&record.Stdout,
			&record.Stderr,
			&exitCode,
			&record.Error,
			&record.Recovery,
			&record.StartedAt,
			&completed,
		); err != nil {
			return nil, fmt.Errorf("scan exec task by session: %w", err)
		}
		if exitCode.Valid {
			value := int(exitCode.Int64)
			record.ExitCode = &value
		}
		if completed.Valid {
			record.CompletedAt = completed.String
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exec tasks by session: %w", err)
	}

	return result, nil
}

func (s *Store) CleanupExecTasksBefore(ctx context.Context, before string) (int64, error) {
	const query = `
DELETE FROM exec_tasks
WHERE completed_at IS NOT NULL AND completed_at < ?
`
	result, err := s.db.ExecContext(ctx, query, before)
	if err != nil {
		return 0, fmt.Errorf("cleanup exec tasks: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("cleanup rows affected: %w", err)
	}
	return rows, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	const schema = `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS sandboxes (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL DEFAULT '',
  image TEXT NOT NULL,
  status TEXT NOT NULL,
  labels_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_accessed_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS snapshots (
  id TEXT PRIMARY KEY,
  sandbox_id TEXT NOT NULL,
  image_ref TEXT NOT NULL,
  snapshot_type TEXT NOT NULL DEFAULT 'container',
  source_host_path TEXT NOT NULL DEFAULT '',
  base_snapshot_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  FOREIGN KEY(sandbox_id) REFERENCES sandboxes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS exec_tasks (
  id TEXT PRIMARY KEY,
  sandbox_id TEXT NOT NULL,
  command TEXT NOT NULL,
  status TEXT NOT NULL,
  stdout TEXT NOT NULL DEFAULT '',
  stderr TEXT NOT NULL DEFAULT '',
  exit_code INTEGER,
  error TEXT NOT NULL DEFAULT '',
  recovery TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  completed_at TEXT,
  FOREIGN KEY(sandbox_id) REFERENCES sandboxes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS session_event_sequences (
  session_id TEXT PRIMARY KEY,
  last_seq INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS sandbox_mounts (
  id TEXT PRIMARY KEY,
  sandbox_id TEXT NOT NULL,
  host_path TEXT NOT NULL,
  container_path TEXT NOT NULL,
  mode TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(sandbox_id) REFERENCES sandboxes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS warm_pool_entries (
  sandbox_id TEXT PRIMARY KEY,
  image TEXT NOT NULL,
  memory_mb INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(sandbox_id) REFERENCES sandboxes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_sandboxes_session_id_id
ON sandboxes(session_id, id);

CREATE INDEX IF NOT EXISTS idx_exec_tasks_sandbox_started_id
ON exec_tasks(sandbox_id, started_at DESC, id DESC);
`

	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate sqlite schema: %w", err)
	}
	_, _ = db.ExecContext(ctx, `ALTER TABLE sandboxes ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE exec_tasks ADD COLUMN recovery TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE snapshots ADD COLUMN snapshot_type TEXT NOT NULL DEFAULT 'container'`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE snapshots ADD COLUMN source_host_path TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE snapshots ADD COLUMN base_snapshot_id TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE sandboxes ADD COLUMN last_accessed_at TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `UPDATE sandboxes SET last_accessed_at = created_at WHERE last_accessed_at = ''`)
	return nil
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
