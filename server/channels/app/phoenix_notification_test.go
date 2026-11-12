// Copyright (c) Phoenix-Soft-VN. Licensed under the surrounding fork terms.
package app

import (
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
	"github.com/stretchr/testify/require"
	"testing"
)

// Mutation: removing either transport guard allows unbound content to reach the native serializer.
func TestPhoenixNotificationTransport(t *testing.T) {
	th := Setup(t).InitBasic(t)
	p := phoenix.NewPolicy(th.App.Srv().Store().GetInternalMasterDB(), nil)
	p.SetDrift("", nil)
	phoenix.SetDefault(p)
	t.Cleanup(func() { phoenix.SetDefault(nil) })
	notification := buildTestPostNotification(th.BasicPost, th.BasicChannel, th.BasicUser)
	result, err := th.App.sendNotificationEmail(th.Context, notification, th.BasicUser2, th.BasicTeam, nil)
	require.NoError(t, err)
	require.Nil(t, result, "unbound recipient must be stopped before rendering or delivery")
	require.Nil(t, th.App.sendPushNotificationSync(th.Context, th.BasicPost, th.BasicUser2, th.BasicChannel, "channel", "sender", true, false, ""))
}
