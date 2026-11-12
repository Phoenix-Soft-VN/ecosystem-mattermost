// Copyright (c) Phoenix-Soft-VN. Licensed under the surrounding fork terms.
package platform

import (
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
	"github.com/stretchr/testify/require"
	"testing"
)

// Mutation: treating a nil invalidated session as a fresh challenge leaves
// an authenticated revoked socket open without frames.
func TestPhoenixInvalidatedSocket(t *testing.T) {
	phoenix.SetDefault(phoenix.NewPolicy(nil, nil))
	t.Cleanup(func() { phoenix.SetDefault(nil) })
	wc := &WebConn{}
	wc.SetSessionToken("")
	require.True(t, wc.phoenixStillAuthorized(), "new socket may await native authentication")
	wc.UserId = model.NewId()
	wc.SetSession(&model.Session{Id: model.NewId(), UserId: wc.UserId})
	wc.InvalidateCache()
	require.False(t, wc.phoenixStillAuthorized(), "native invalidation must close an identified socket with no valid session")
}
