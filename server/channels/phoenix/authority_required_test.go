// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
package phoenix

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// Mutation: treat missing auth startup configuration as the nil-authority pure
// policy test fixture, allowing an existing customer session/background send.
func TestAuthorityRequired(t *testing.T) {
	f := newFixture(t)
	w := newWorld(f)
	p := w.policy()
	ctx := context.Background()
	_, err := p.ResolveActor(ctx, w.SessA1, w.A1)
	require.NoError(t, err, "warm the developer-only local policy cache")
	f.insert("channelmembers", map[string]any{"channelid": w.ChanA, "userid": w.A1})
	t.Setenv("PHOENIX_ENFORCE", "true")
	for _, who := range []struct{ session, user string }{{w.SessA1, w.A1}, {w.SessA2, w.A2}} {
		_, err = p.ResolveActor(ctx, who.session, who.user)
		var failure *Error
		require.True(t, errors.As(err, &failure))
		require.Equal(t, 503, failure.Status)
		require.Equal(t, CodeAuthorityDown, failure.Code)
	}
	require.False(t, p.NotificationAllowed(ctx, w.A1, []string{w.ChanA}), "queued native delivery cannot use missing live authority")
	operator, err := p.ResolveActor(ctx, w.SessOp, w.Op)
	require.NoError(t, err)
	require.True(t, operator.Operator, "explicit infrastructure registry remains separate")
}
