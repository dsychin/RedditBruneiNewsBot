package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaimFailureRetryAndReplyTransitions(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "bot.db"))
	now := time.Date(2026, 10, 11, 1, 2, 3, 456000000, time.FixedZone("test", 9*60*60))

	claimed, err := s.Claim(ctx, "post-1", "testingground4bots", now)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = s.Claim(ctx, "post-1", "testingground4bots", now)
	require.NoError(t, err)
	assert.False(t, claimed)

	post, err := s.Get(ctx, "post-1")
	require.NoError(t, err)
	assert.Equal(t, StatusPending, post.Status)
	assert.Equal(t, 0, post.Attempts)
	require.NotNil(t, post.ClaimedAt)
	assert.Equal(t, now.UTC(), *post.ClaimedAt)

	nextAttempt := now.Add(time.Minute)
	require.NoError(t, s.RecordFailure(ctx, "post-1", now, &nextAttempt))
	post, err = s.Get(ctx, "post-1")
	require.NoError(t, err)
	assert.Equal(t, StatusPending, post.Status)
	assert.Equal(t, 1, post.Attempts)
	require.NotNil(t, post.FirstFailedAt)
	assert.Equal(t, now.UTC(), *post.FirstFailedAt)
	require.NotNil(t, post.NextAttemptAt)
	assert.Equal(t, nextAttempt.UTC(), *post.NextAttemptAt)
	assert.Nil(t, post.ClaimedAt)

	due, err := s.ListDue(ctx, nextAttempt.Add(-time.Second), 10)
	require.NoError(t, err)
	assert.Empty(t, due)
	due, err = s.ListDue(ctx, nextAttempt, 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, "post-1", due[0].ID)

	claimed, err = s.Claim(ctx, "post-1", "testingground4bots", nextAttempt.Add(-time.Second))
	require.NoError(t, err)
	assert.False(t, claimed)
	claimed, err = s.Claim(ctx, "post-1", "testingground4bots", nextAttempt)
	require.NoError(t, err)
	assert.True(t, claimed)

	secondFailureAt := nextAttempt.Add(time.Second)
	secondNextAttempt := secondFailureAt.Add(time.Minute)
	require.NoError(t, s.RecordFailure(ctx, "post-1", secondFailureAt, &secondNextAttempt))
	post, err = s.Get(ctx, "post-1")
	require.NoError(t, err)
	assert.Equal(t, 2, post.Attempts)
	require.NotNil(t, post.FirstFailedAt)
	assert.Equal(t, now.UTC(), *post.FirstFailedAt)

	claimed, err = s.Claim(ctx, "post-1", "testingground4bots", secondNextAttempt)
	require.NoError(t, err)
	assert.True(t, claimed)
	repliedAt := secondNextAttempt.Add(time.Second)
	require.NoError(t, s.MarkReplied(ctx, "post-1", repliedAt))
	require.NoError(t, s.MarkReplied(ctx, "post-1", repliedAt))
	post, err = s.Get(ctx, "post-1")
	require.NoError(t, err)
	assert.Equal(t, StatusReplied, post.Status)
	assert.Equal(t, 2, post.Attempts)
	assert.Nil(t, post.NextAttemptAt)
	assert.Nil(t, post.ClaimedAt)
	assert.False(t, post.UpdatedAt.Before(repliedAt))

	claimed, err = s.Claim(ctx, "post-1", "testingground4bots", repliedAt)
	require.NoError(t, err)
	assert.False(t, claimed)
}

func TestRecordFailureAndMarkSkipped(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "bot.db"))
	now := time.Date(2026, 10, 11, 1, 2, 3, 0, time.UTC)

	claimed, err := s.Claim(ctx, "permanent", "news", now)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, s.RecordFailure(ctx, "permanent", now, nil))

	post, err := s.Get(ctx, "permanent")
	require.NoError(t, err)
	assert.Equal(t, StatusSkipped, post.Status)
	assert.Equal(t, 1, post.Attempts)
	assert.Nil(t, post.NextAttemptAt)
	assert.Nil(t, post.ClaimedAt)

	claimed, err = s.Claim(ctx, "policy-skip", "news", now)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, s.MarkSkipped(ctx, "policy-skip", now))
	post, err = s.Get(ctx, "policy-skip")
	require.NoError(t, err)
	assert.Equal(t, StatusSkipped, post.Status)
	assert.Zero(t, post.Attempts)

	claimed, err = s.Claim(ctx, "invalid-retry", "news", now)
	require.NoError(t, err)
	require.True(t, claimed)
	require.ErrorContains(t, s.RecordFailure(ctx, "invalid-retry", now, &now), "must be after")
}

func TestOpenRecoversAbandonedClaimsAndPreservesRetryTimes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bot.db")
	now := time.Date(2026, 10, 11, 1, 2, 3, 0, time.UTC)
	s := openTestStore(t, path)

	claimed, err := s.Claim(ctx, "interrupted", "news", now)
	require.NoError(t, err)
	require.True(t, claimed)
	nextAttempt := now.Add(time.Hour)
	claimed, err = s.Claim(ctx, "will-retry", "news", now)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, s.RecordFailure(ctx, "will-retry", now, &nextAttempt))
	require.NoError(t, s.Close())

	reopened, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })

	post, err := reopened.Get(ctx, "interrupted")
	require.NoError(t, err)
	assert.Zero(t, post.Attempts)
	assert.Nil(t, post.FirstFailedAt)
	assert.Nil(t, post.ClaimedAt)
	claimed, err = reopened.Claim(ctx, "interrupted", "news", now)
	require.NoError(t, err)
	assert.True(t, claimed)

	post, err = reopened.Get(ctx, "will-retry")
	require.NoError(t, err)
	assert.Equal(t, 1, post.Attempts)
	assert.Equal(t, nextAttempt.UTC(), *post.NextAttemptAt)
	claimed, err = reopened.Claim(ctx, "will-retry", "news", now)
	require.NoError(t, err)
	assert.False(t, claimed)
	claimed, err = reopened.Claim(ctx, "will-retry", "news", nextAttempt)
	require.NoError(t, err)
	assert.True(t, claimed)
}

func TestOpenRefusesSecondInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.db")
	first := openTestStore(t, path)

	second, err := Open(path)
	require.ErrorIs(t, err, ErrAlreadyRunning)
	assert.Nil(t, second)

	require.NoError(t, first.Close())
	third, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, third.Close())
}

func TestConcurrentClaimsAreExclusive(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "bot.db"))
	now := time.Now().UTC()

	const workers = 12
	var wg sync.WaitGroup
	claims := make(chan bool, workers)
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := s.Claim(ctx, "same-post", "news", now)
			if err != nil {
				errs <- err
				return
			}
			claims <- claimed
		}()
	}
	wg.Wait()
	close(claims)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	var claimedCount int
	for claimed := range claims {
		if claimed {
			claimedCount++
		}
	}
	assert.Equal(t, 1, claimedCount)
}

func TestGetMissingPost(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "bot.db"))

	_, err := s.Get(context.Background(), "missing")
	require.ErrorIs(t, err, ErrNotFound)
	assert.False(t, errors.Is(err, ErrNotClaimed))
}

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}
