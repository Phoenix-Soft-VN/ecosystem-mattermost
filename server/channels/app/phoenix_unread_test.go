// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package app

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
	"github.com/mattermost/mattermost/server/v8/channels/store/storetest/mocks"
)

// Break (M4): omitting CRT thread mentions in getUserBadgeCount / PhoenixNative.UnreadBadgeCount.
func TestPhoenixUnreadBadgeCountIncludesCRT(t *testing.T) {
	th := SetupWithStoreMock(t)

	mockStore := th.App.Srv().Store().(*mocks.Store)
	userStore := &mocks.UserStore{}
	threadStore := &mocks.ThreadStore{}
	prefStore := &mocks.PreferenceStore{}
	postStore := &mocks.PostStore{}
	postStore.On("GetMaxPostSize").Return(model.PostMessageMaxBytesV2, nil)
	systemStore := &mocks.SystemStore{}
	systemStore.On("GetByName", "UpgradedFromTE").Return(&model.System{Name: "UpgradedFromTE", Value: "false"}, nil)
	systemStore.On("GetByName", "InstallationDate").Return(&model.System{Name: "InstallationDate", Value: "10"}, nil)
	systemStore.On("GetByName", "FirstServerRunTimestamp").Return(&model.System{Name: "FirstServerRunTimestamp", Value: "10"}, nil)
	systemStore.On("Get").Return(model.StringMap{model.SystemServerId: "srv1"}, nil)
	systemStore.On("GetByNameWithContext", mock.Anything, model.SystemServerId).Return(&model.System{Name: model.SystemServerId, Value: "srv1"}, nil)

	mockStore.On("User").Return(userStore)
	mockStore.On("Thread").Return(threadStore)
	mockStore.On("Preference").Return(prefStore)
	mockStore.On("Post").Return(postStore)
	mockStore.On("System").Return(systemStore)
	mockStore.On("GetDBSchemaVersion").Return(1, nil)

	th.App.UpdateConfig(func(cfg *model.Config) {
		*cfg.ServiceSettings.CollapsedThreads = model.CollapsedThreadsDefaultOn
	})

	native := NewPhoenixNative(th.App)

	// CRT enabled for u1: channel mentions (2) + CRT thread unread mentions (3) = 5.
	prefStore.On("Get", "u1", model.PreferenceCategoryDisplaySettings, model.PreferenceNameCollapsedThreadsEnabled).
		Return(&model.Preference{UserId: "u1", Category: model.PreferenceCategoryDisplaySettings, Name: model.PreferenceNameCollapsedThreadsEnabled, Value: "on"}, nil)
	userStore.On("GetUnreadCount", "u1", true).Return(int64(2), nil)
	threadStore.On("GetTotalUnreadMentions", "u1", "", mock.Anything).Return(int64(3), nil)

	gotCRT, err := native.UnreadBadgeCount(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, 5, gotCRT)

	// CRT disabled for u2: channel mentions (4) only; Thread().GetTotalUnreadMentions not called.
	prefStore.On("Get", "u2", model.PreferenceCategoryDisplaySettings, model.PreferenceNameCollapsedThreadsEnabled).
		Return(&model.Preference{UserId: "u2", Category: model.PreferenceCategoryDisplaySettings, Name: model.PreferenceNameCollapsedThreadsEnabled, Value: "off"}, nil)
	userStore.On("GetUnreadCount", "u2", false).Return(int64(4), nil)

	gotNoCRT, err := native.UnreadBadgeCount(context.Background(), "u2")
	require.NoError(t, err)
	require.Equal(t, 4, gotNoCRT)

	userStore.AssertExpectations(t)
	threadStore.AssertExpectations(t)
}

type recordingUnreadNotifier struct {
	mu         sync.Mutex
	notified   []string
	activities []struct{ userID, sessionID string }
}

func (r *recordingUnreadNotifier) NotifyUser(userID string) {
	r.mu.Lock()
	r.notified = append(r.notified, userID)
	r.mu.Unlock()
}

func (r *recordingUnreadNotifier) NotifyUserActivity(userID, sessionID string) {
	r.mu.Lock()
	r.activities = append(r.activities, struct{ userID, sessionID string }{userID, sessionID})
	r.mu.Unlock()
}

func (r *recordingUnreadNotifier) reset() {
	r.mu.Lock()
	r.notified = nil
	r.activities = nil
	r.mu.Unlock()
}

// Break (I5b + I2 + I3): removing NotifyUnread* calls from SendNotifications, MarkChannelsAsViewed,
// MarkChannelAsUnreadFromPost (CRT and non-CRT branches), or UpdateThreadReadForUser.
func TestPhoenixAppHooksInvokeUnreadNotifier(t *testing.T) {
	th := Setup(t).InitBasic(t)

	rec := &recordingUnreadNotifier{}
	phoenix.SetDefaultUnreadNotifier(rec)
	t.Cleanup(func() { phoenix.SetDefaultUnreadNotifier(nil) })

	th.App.UpdateConfig(func(cfg *model.Config) {
		*cfg.ServiceSettings.CollapsedThreads = model.CollapsedThreadsDefaultOn
		*cfg.ServiceSettings.ThreadAutoFollow = true
		*cfg.EmailSettings.SendPushNotifications = false
	})

	u1 := th.BasicUser
	u2 := th.BasicUser2
	ch := th.BasicChannel
	rctx := th.Context.WithSession(&model.Session{Id: "sess-1", UserId: u1.Id})

	// 1. SendNotifications (mention in channel + CRT reply mention) -> fires NotifyUser after mac drain.
	rec.reset()
	rootPost := th.CreatePost(t, ch)
	mentionPost, _, appErr := th.App.CreatePost(th.Context, &model.Post{
		UserId:    u2.Id,
		ChannelId: ch.Id,
		RootId:    rootPost.Id,
		Message:   "ping @" + u1.Username,
	}, ch, model.CreatePostFlags{})
	require.Nil(t, appErr)
	require.Contains(t, rec.notified, u1.Id, "SendNotifications must fire NotifyUser for mentioned user")

	// 2. MarkChannelsAsViewed: plain unread without mentions must NOT fire; channel with mentions MUST fire.
	rec.reset()
	plainCh := th.CreateChannel(t, th.BasicTeam)
	th.AddUserToChannel(t, u1, plainCh)
	th.CreatePost(t, plainCh)
	_, appErr = th.App.MarkChannelsAsViewed(rctx, []string{plainCh.Id}, u1.Id, "sess-1", true, true)
	require.Nil(t, appErr)
	require.Empty(t, rec.activities, "plain unread channel view without mentions must not trigger unread push")

	_, appErr = th.App.MarkChannelsAsViewed(rctx, []string{ch.Id}, u1.Id, "sess-1", true, false)
	require.Nil(t, appErr)
	require.NotEmpty(t, rec.activities, "MarkChannelsAsViewed clearing mentions must fire NotifyUserActivity")
	require.Equal(t, u1.Id, rec.activities[0].userID)
	require.Equal(t, "sess-1", rec.activities[0].sessionID)

	// 3a. MarkChannelAsUnreadFromPost (CRT branch, I3) -> MUST fire NotifyUserActivity.
	rec.reset()
	_, appErr = th.App.MarkChannelAsUnreadFromPost(rctx, rootPost.Id, u1.Id, true)
	require.Nil(t, appErr)
	require.NotEmpty(t, rec.activities, "CRT MarkChannelAsUnreadFromPost must fire NotifyUserActivity")

	// 3b. MarkChannelAsUnreadFromPost (non-CRT root post branch) -> MUST fire NotifyUserActivity.
	rec.reset()
	_, appErr = th.App.MarkChannelAsUnreadFromPost(rctx, rootPost.Id, u1.Id, false)
	require.Nil(t, appErr)
	require.NotEmpty(t, rec.activities, "non-CRT root MarkChannelAsUnreadFromPost must fire NotifyUserActivity")

	// 3c. MarkChannelAsUnreadFromPost (non-CRT reply post branch) -> MUST fire NotifyUserActivity.
	rec.reset()
	_, appErr = th.App.MarkChannelAsUnreadFromPost(rctx, mentionPost.Id, u1.Id, false)
	require.Nil(t, appErr)
	require.NotEmpty(t, rec.activities, "non-CRT reply MarkChannelAsUnreadFromPost must fire NotifyUserActivity")

	// 4. UpdateThreadReadForUser -> MUST fire NotifyUserActivity.
	rec.reset()
	_, appErr = th.App.UpdateThreadReadForUser(rctx, "sess-1", u1.Id, th.BasicTeam.Id, rootPost.Id, mentionPost.CreateAt)
	require.Nil(t, appErr)
	require.NotEmpty(t, rec.activities, "UpdateThreadReadForUser must fire NotifyUserActivity")
}
