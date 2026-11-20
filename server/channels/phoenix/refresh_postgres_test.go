// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
package phoenix

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Use the native migrated PostgreSQL schema: an in-memory driver cannot model
// a row lock held by one connection while another connection tries to update it.
func TestUnifiedRefreshPostgres(t *testing.T) {
	f := newFixture(t)
	w := newWorld(f)
	store := NewStore(f.db, nil)
	var status atomic.Int32
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		code := int(status.Load())
		w.WriteHeader(code)
		if code == http.StatusOK {
			_, _ = fmt.Fprint(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":300}`)
		}
	}))
	defer upstream.Close()

	newAuthority := func() *Authority {
		a := NewAuthority(&AuthorityClient{HTTP: upstream.Client()})
		a.Store, a.TokenURL = store, upstream.URL
		return a
	}
	newSession := func(t *testing.T) ActorContext {
		t.Helper()
		id := w.session("sub-a1", "sid-refresh", w.TenantA, w.A1)
		_, err := f.db.Exec(`UPDATE phoenix_session_bindings SET access_token_enc='old-access', refresh_token_enc='old-refresh',
			access_expires_at=$2, idle_expires_at=$3, absolute_expires_at=$3 WHERE session_id=$1`,
			id, time.Now().Add(-time.Minute).UnixMilli(), time.Now().Add(time.Hour).UnixMilli())
		require.NoError(t, err)
		status.Store(http.StatusOK)
		calls.Store(0)
		return ActorContext{SessionID: id, UserID: w.A1, TenantID: w.TenantA, Sub: "sub-a1", SID: "sid-refresh"}
	}
	check := func(t *testing.T, a *Authority, actor ActorContext, method string) error {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return a.Check(WithRequestMethod(ctx, method), actor)
	}

	t.Run("rotated tokens commit before releasing the row lock", func(t *testing.T) {
		actor := newSession(t)
		a := newAuthority()
		require.NoError(t, check(t, a, actor, http.MethodGet))
		b, err := store.SessionBindingByID(context.Background(), actor.SessionID)
		require.NoError(t, err)
		require.Equal(t, "new-access", b.AccessTokenEnc)
		require.Equal(t, "new-refresh", b.RefreshTokenEnc)
		require.Greater(t, b.AccessExpiresAtMS, time.Now().UnixMilli())
		require.NoError(t, check(t, a, actor, http.MethodGet))
		require.EqualValues(t, 1, calls.Load())
	})

	t.Run("separate app instances serialize refresh", func(t *testing.T) {
		actor := newSession(t)
		var wg sync.WaitGroup
		errs := make(chan error, 6)
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- check(t, newAuthority(), actor, http.MethodGet)
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		require.EqualValues(t, 1, calls.Load())
	})

	t.Run("outage marker commits and grace is bounded", func(t *testing.T) {
		actor := newSession(t)
		status.Store(http.StatusServiceUnavailable)
		a := newAuthority()
		require.NoError(t, check(t, a, actor, http.MethodGet))
		b, err := store.SessionBindingByID(context.Background(), actor.SessionID)
		require.NoError(t, err)
		require.Positive(t, b.OutageSinceMS)
		require.Error(t, check(t, a, actor, http.MethodPost))
		_, err = f.db.Exec(`UPDATE phoenix_session_bindings SET outage_since=$2 WHERE session_id=$1`, actor.SessionID, time.Now().Add(-6*time.Minute).UnixMilli())
		require.NoError(t, err)
		require.Error(t, check(t, a, actor, http.MethodGet))
	})

	t.Run("native revocation runs after releasing the row lock", func(t *testing.T) {
		actor := newSession(t)
		status.Store(http.StatusBadRequest)
		a := newAuthority()
		var revokeErr error
		a.OnInactive = func(ctx context.Context, id string) {
			// The real native session FK cascades to the locked binding row.
			_, revokeErr = f.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=$1`, id)
		}
		err := check(t, a, actor, http.MethodGet)
		require.Error(t, err)
		require.NoError(t, revokeErr)
		var count int
		require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM phoenix_session_bindings WHERE session_id=$1`, actor.SessionID).Scan(&count))
		require.Zero(t, count)
	})

	t.Run("failed persistence does not report a successful refresh", func(t *testing.T) {
		actor := newSession(t)
		_, err := f.db.Exec(`CREATE FUNCTION reject_refresh_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected persistence failure'; END $$;
			CREATE TRIGGER reject_refresh_write BEFORE UPDATE OF access_token_enc ON phoenix_session_bindings FOR EACH ROW EXECUTE FUNCTION reject_refresh_write()`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := f.db.Exec(`DROP TRIGGER reject_refresh_write ON phoenix_session_bindings; DROP FUNCTION reject_refresh_write()`)
			require.NoError(t, err)
		})
		require.ErrorIs(t, check(t, newAuthority(), actor, http.MethodGet), ErrAuthorityUnavailable)
		b, err := store.SessionBindingByID(context.Background(), actor.SessionID)
		require.NoError(t, err)
		require.Equal(t, "old-refresh", b.RefreshTokenEnc)
	})

	t.Run("waiting for another transaction respects cancellation", func(t *testing.T) {
		actor := newSession(t)
		tx, err := f.db.Begin()
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = tx.Exec(`SELECT session_id FROM phoenix_session_bindings WHERE session_id=$1 FOR UPDATE`, actor.SessionID)
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		start := time.Now()
		require.ErrorIs(t, newAuthority().Check(ctx, actor), ErrAuthorityUnavailable)
		require.Less(t, time.Since(start), time.Second)
		require.Zero(t, calls.Load())
	})
}
