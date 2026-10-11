package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	_ "modernc.org/sqlite"
)

var (
	ErrAlreadyRunning = errors.New("another bot instance is already running")
	ErrNotFound       = errors.New("post not found")
	ErrNotClaimed     = errors.New("post is not claimed")
)

const (
	StatusPending Status = "pending"
	StatusReplied Status = "replied"
	StatusSkipped Status = "skipped"
)

type Status string

// Post is the persisted state for one Reddit submission.
type Post struct {
	ID            string
	Subreddit     string
	Status        Status
	Attempts      int
	FirstFailedAt *time.Time
	NextAttemptAt *time.Time
	ClaimedAt     *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Store persists post state and holds the process-wide database lock.
type Store struct {
	db   *sql.DB
	lock *flock.Flock
}

// Open opens the SQLite database, obtains the exclusive process lock, and
// recovers claims abandoned by a previous process.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path must not be empty")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	lock := flock.New(absolutePath + ".lock")
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire database lock: %w", err)
	}
	if !locked {
		err := fmt.Errorf("%w: %s.lock", ErrAlreadyRunning, absolutePath)
		return nil, errors.Join(err, lock.Close())
	}

	dbFile, err := os.OpenFile(absolutePath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create database file: %w", err), lock.Close())
	}
	if err := dbFile.Close(); err != nil {
		return nil, errors.Join(fmt.Errorf("close database file: %w", err), lock.Close())
	}

	db, err := sql.Open("sqlite", absolutePath)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open SQLite database: %w", err), lock.Close())
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &Store{db: db, lock: lock}
	if err := s.initialize(context.Background()); err != nil {
		return nil, errors.Join(err, db.Close(), lock.Close())
	}
	return s, nil
}

// Close closes the database and releases the process lock.
func (s *Store) Close() error {
	dbErr := s.db.Close()
	lockErr := s.lock.Close()
	return errors.Join(dbErr, lockErr)
}

// Claim inserts and claims a new post, or claims an existing pending post if
// its retry time is due. Terminal, future-due, or already-claimed posts are
// left untouched.
func (s *Store) Claim(ctx context.Context, postID, subreddit string, now time.Time) (bool, error) {
	if postID == "" {
		return false, errors.New("post ID must not be empty")
	}
	if subreddit == "" {
		return false, errors.New("subreddit must not be empty")
	}

	nowMillis := unixMillis(now)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin post claim: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO posts (
			post_id, subreddit, status, attempts, created_at, updated_at
		) VALUES (?, ?, 'pending', 0, ?, ?)
		ON CONFLICT(post_id) DO NOTHING`,
		postID, subreddit, nowMillis, nowMillis,
	)
	if err != nil {
		return false, fmt.Errorf("insert post for claim: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE posts
		SET claimed_at = ?, updated_at = ?
		WHERE post_id = ?
			AND status = 'pending'
			AND claimed_at IS NULL
			AND (next_attempt_at IS NULL OR next_attempt_at <= ?)`,
		nowMillis, nowMillis, postID, nowMillis,
	)
	if err != nil {
		return false, fmt.Errorf("claim post: %w", err)
	}
	claimed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check post claim: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit post claim: %w", err)
	}
	return claimed == 1, nil
}

// RecordFailure records a failed claimed attempt. A nil nextAttemptAt marks
// the failure permanent and moves the post to skipped.
func (s *Store) RecordFailure(ctx context.Context, postID string, failedAt time.Time, nextAttemptAt *time.Time) error {
	if postID == "" {
		return errors.New("post ID must not be empty")
	}
	var next any
	status := StatusSkipped
	if nextAttemptAt != nil {
		if !nextAttemptAt.After(failedAt) {
			return errors.New("next attempt time must be after failure time")
		}
		next = unixMillis(*nextAttemptAt)
		status = StatusPending
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE posts
		SET status = ?,
			attempts = attempts + 1,
			first_failed_at = COALESCE(first_failed_at, ?),
			next_attempt_at = ?,
			claimed_at = NULL,
			updated_at = ?
		WHERE post_id = ? AND status = 'pending' AND claimed_at IS NOT NULL`,
		status, unixMillis(failedAt), next, unixMillis(failedAt), postID,
	)
	if err != nil {
		return fmt.Errorf("record post failure: %w", err)
	}
	return requireClaimedUpdate(result, "record post failure")
}

// MarkReplied marks a claimed post as replied after Reddit accepts the comment.
// It is idempotent for posts already marked replied.
func (s *Store) MarkReplied(ctx context.Context, postID string, repliedAt time.Time) error {
	if postID == "" {
		return errors.New("post ID must not be empty")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE posts
		SET status = 'replied',
			next_attempt_at = NULL,
			claimed_at = NULL,
			updated_at = ?
		WHERE post_id = ? AND status = 'pending' AND claimed_at IS NOT NULL`,
		unixMillis(repliedAt), postID,
	)
	if err != nil {
		return fmt.Errorf("mark post replied: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check replied status update: %w", err)
	}
	if updated == 1 {
		return nil
	}

	post, err := s.Get(ctx, postID)
	if err != nil {
		return err
	}
	if post.Status == StatusReplied {
		return nil
	}
	return ErrNotClaimed
}

// MarkSkipped marks a claimed post as skipped without counting a processing
// failure, for example when policy makes it ineligible.
func (s *Store) MarkSkipped(ctx context.Context, postID string, skippedAt time.Time) error {
	if postID == "" {
		return errors.New("post ID must not be empty")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE posts
		SET status = 'skipped',
			next_attempt_at = NULL,
			claimed_at = NULL,
			updated_at = ?
		WHERE post_id = ? AND status = 'pending' AND claimed_at IS NOT NULL`,
		unixMillis(skippedAt), postID,
	)
	if err != nil {
		return fmt.Errorf("mark post skipped: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check skipped status update: %w", err)
	}
	if updated == 1 {
		return nil
	}

	post, err := s.Get(ctx, postID)
	if err != nil {
		return err
	}
	if post.Status == StatusSkipped {
		return nil
	}
	return ErrNotClaimed
}

// ListDue returns unclaimed pending posts whose retry time has arrived.
func (s *Store) ListDue(ctx context.Context, now time.Time, limit int) ([]Post, error) {
	if limit <= 0 {
		return nil, errors.New("limit must be positive")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT post_id, subreddit, status, attempts, first_failed_at,
			next_attempt_at, claimed_at, created_at, updated_at
		FROM posts
		WHERE status = 'pending'
			AND claimed_at IS NULL
			AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		ORDER BY COALESCE(next_attempt_at, created_at), created_at, post_id
		LIMIT ?`,
		unixMillis(now), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list due posts: %w", err)
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, fmt.Errorf("scan due post: %w", err)
		}
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due posts: %w", err)
	}
	return posts, nil
}

// Get returns the current persisted state for a post.
func (s *Store) Get(ctx context.Context, postID string) (Post, error) {
	post, err := scanPost(s.db.QueryRowContext(ctx, `
		SELECT post_id, subreddit, status, attempts, first_failed_at,
			next_attempt_at, claimed_at, created_at, updated_at
		FROM posts WHERE post_id = ?`, postID))
	if errors.Is(err, sql.ErrNoRows) {
		return Post{}, fmt.Errorf("%w: %s", ErrNotFound, postID)
	}
	if err != nil {
		return Post{}, fmt.Errorf("get post: %w", err)
	}
	return post, nil
}

func (s *Store) initialize(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to SQLite database: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("configure SQLite busy timeout: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS posts (
			post_id TEXT PRIMARY KEY,
			subreddit TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'replied', 'skipped')),
			attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
			first_failed_at INTEGER,
			next_attempt_at INTEGER,
			claimed_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK (status = 'pending' OR claimed_at IS NULL),
			CHECK (status = 'pending' OR next_attempt_at IS NULL)
		);
		CREATE INDEX IF NOT EXISTS posts_due_idx
			ON posts (status, next_attempt_at, created_at)
			WHERE status = 'pending' AND claimed_at IS NULL;`)
	if err != nil {
		return fmt.Errorf("initialize SQLite schema: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE posts
		SET claimed_at = NULL, updated_at = ?
		WHERE status = 'pending' AND claimed_at IS NOT NULL`,
		unixMillis(time.Now()),
	); err != nil {
		return fmt.Errorf("recover abandoned post claims: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanPost(row scanner) (Post, error) {
	var (
		post            Post
		status          string
		firstFailedAt   sql.NullInt64
		nextAttemptAt   sql.NullInt64
		claimedAt       sql.NullInt64
		createdAtMillis int64
		updatedAtMillis int64
	)
	if err := row.Scan(
		&post.ID,
		&post.Subreddit,
		&status,
		&post.Attempts,
		&firstFailedAt,
		&nextAttemptAt,
		&claimedAt,
		&createdAtMillis,
		&updatedAtMillis,
	); err != nil {
		return Post{}, err
	}
	post.Status = Status(status)
	post.CreatedAt = fromUnixMillis(createdAtMillis)
	post.UpdatedAt = fromUnixMillis(updatedAtMillis)
	post.FirstFailedAt = nullableTime(firstFailedAt)
	post.NextAttemptAt = nullableTime(nextAttemptAt)
	post.ClaimedAt = nullableTime(claimedAt)
	return post, nil
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := fromUnixMillis(value.Int64)
	return &result
}

func unixMillis(value time.Time) int64 {
	return value.UTC().UnixMilli()
}

func fromUnixMillis(value int64) time.Time {
	return time.UnixMilli(value).UTC()
}

func requireClaimedUpdate(result sql.Result, operation string) error {
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check %s: %w", operation, err)
	}
	if updated == 0 {
		return ErrNotClaimed
	}
	return nil
}
