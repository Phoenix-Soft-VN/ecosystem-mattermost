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

// TestPhoenixMutedChannelMentionsInUnreadBadge proves against a real Postgres DB through
// the real CreatePost -> SendNotifications -> getUserBadgeCount / UnreadBadgeCount path
// that an explicit @user mention in a muted channel increases the unread badge count
// (both CRT off and CRT on, including a threaded reply mention), while plain posts and
// Mattermost's default-suppressed @channel/@all/@here mentions in a muted channel do not.
func TestPhoenixMutedChannelMentionsInUnreadBadge(t *testing.T) {
	th := Setup(t).InitBasic(t)

	rec := &recordingUnreadNotifier{}
	phoenix.SetDefaultUnreadNotifier(rec)
	t.Cleanup(func() { phoenix.SetDefaultUnreadNotifier(nil) })

	th.App.UpdateConfig(func(cfg *model.Config) {
		*cfg.EmailSettings.SendPushNotifications = false
		*cfg.EmailSettings.SendEmailNotifications = false
		*cfg.ServiceSettings.ThreadAutoFollow = true
	})

	u1 := th.BasicUser
	u2 := th.BasicUser2
	native := NewPhoenixNative(th.App)

	// Mark u1 online so @here would trigger if channel mentions were not muted.
	th.App.SetStatusOnline(u1.Id, false)

	muteChannel := func(t *testing.T, ch *model.Channel) {
		t.Helper()
		member, appErr := th.App.ToggleMuteChannel(th.Context, ch.Id, u1.Id)
		require.Nil(t, appErr)
		require.True(t, member.IsChannelMuted(), "channel must be muted")
		require.Equal(t, model.ChannelMarkUnreadMention, member.NotifyProps[model.MarkUnreadNotifyProp])
		require.Equal(t, model.IgnoreChannelMentionsDefault, member.NotifyProps[model.IgnoreChannelMentionsNotifyProp])
	}

	postAsU2 := func(t *testing.T, ch *model.Channel, rootID, msg string) *model.Post {
		t.Helper()
		p, _, appErr := th.App.CreatePost(th.Context, &model.Post{
			UserId:    u2.Id,
			ChannelId: ch.Id,
			RootId:    rootID,
			Message:   msg,
		}, ch, model.CreatePostFlags{})
		require.Nil(t, appErr)
		return p
	}

	setCRTForU1 := func(t *testing.T, enabled bool) {
		t.Helper()
		val := "off"
		cfgVal := model.CollapsedThreadsDisabled
		if enabled {
			val = "on"
			cfgVal = model.CollapsedThreadsDefaultOn
		}
		th.App.UpdateConfig(func(cfg *model.Config) {
			*cfg.ServiceSettings.CollapsedThreads = cfgVal
		})
		appErr := th.App.UpdatePreferences(th.Context, u1.Id, model.Preferences{{
			UserId:   u1.Id,
			Category: model.PreferenceCategoryDisplaySettings,
			Name:     model.PreferenceNameCollapsedThreadsEnabled,
			Value:    val,
		}})
		require.Nil(t, appErr)
		require.Equal(t, enabled, th.App.IsCRTEnabledForUser(th.Context, u1.Id))
	}

	t.Run("CRT_off", func(t *testing.T) {
		setCRTForU1(t, false)

		ch := th.CreateChannel(t, th.BasicTeam)
		th.AddUserToChannel(t, u1, ch)
		th.AddUserToChannel(t, u2, ch)
		_, appErr := th.App.MarkChannelsAsViewed(th.Context, []string{ch.Id}, u1.Id, "", true, false)
		require.Nil(t, appErr)

		badge, appErr := th.App.getUserBadgeCount(u1.Id, false)
		require.Nil(t, appErr)
		require.Equal(t, 0, badge)

		muteChannel(t, ch)

		// Plain non-mention post in muted channel must NOT increase badge.
		rec.reset()
		plainRoot := postAsU2(t, ch, "", "plain message in muted channel")
		badge, appErr = th.App.getUserBadgeCount(u1.Id, false)
		require.Nil(t, appErr)
		require.Equal(t, 0, badge, "plain post in muted channel must not increase badge")
		require.NotContains(t, rec.notified, u1.Id)

		// Mattermost documented default: @channel, @all, @here are ignored in a muted channel
		// when ignore_channel_mentions is "default" (mention_keywords.go).
		rec.reset()
		postAsU2(t, ch, "", "broadcast @channel")
		postAsU2(t, ch, "", "broadcast @all")
		postAsU2(t, ch, "", "broadcast @here")
		badge, appErr = th.App.getUserBadgeCount(u1.Id, false)
		require.Nil(t, appErr)
		require.Equal(t, 0, badge, "@channel/@all/@here in muted channel are suppressed by default")
		require.NotContains(t, rec.notified, u1.Id)

		// Explicit @member root post in muted channel MUST increase badge.
		rec.reset()
		postAsU2(t, ch, "", "hello @"+u1.Username)
		badge, appErr = th.App.getUserBadgeCount(u1.Id, false)
		require.Nil(t, appErr)
		require.Equal(t, 1, badge, "explicit @user root mention in muted channel must increase badge")
		gotNative, err := native.UnreadBadgeCount(context.Background(), u1.Id)
		require.NoError(t, err)
		require.Equal(t, 1, gotNative)
		require.Contains(t, rec.notified, u1.Id)

		// Explicit @member threaded reply in muted channel (CRT off) MUST also increase badge.
		rec.reset()
		postAsU2(t, ch, plainRoot.Id, "reply @"+u1.Username)
		badge, appErr = th.App.getUserBadgeCount(u1.Id, false)
		require.Nil(t, appErr)
		require.Equal(t, 2, badge, "explicit @user reply mention in muted channel (CRT off) must increase badge")
		gotNative, err = native.UnreadBadgeCount(context.Background(), u1.Id)
		require.NoError(t, err)
		require.Equal(t, 2, gotNative)
		require.Contains(t, rec.notified, u1.Id)

		// Clear mentions so the next subtest starts from 0.
		_, appErr = th.App.MarkChannelsAsViewed(th.Context, []string{ch.Id}, u1.Id, "", true, false)
		require.Nil(t, appErr)
	})

	t.Run("CRT_on", func(t *testing.T) {
		setCRTForU1(t, true)

		ch := th.CreateChannel(t, th.BasicTeam)
		th.AddUserToChannel(t, u1, ch)
		th.AddUserToChannel(t, u2, ch)
		_, appErr := th.App.MarkChannelsAsViewed(th.Context, []string{ch.Id}, u1.Id, "", true, false)
		require.Nil(t, appErr)

		badge, appErr := th.App.getUserBadgeCount(u1.Id, true)
		require.Nil(t, appErr)
		require.Equal(t, 0, badge)

		muteChannel(t, ch)

		// Plain non-mention root post in muted channel must NOT increase badge.
		rec.reset()
		plainRoot := postAsU2(t, ch, "", "plain root in muted channel (CRT on)")
		badge, appErr = th.App.getUserBadgeCount(u1.Id, true)
		require.Nil(t, appErr)
		require.Equal(t, 0, badge, "plain post in muted channel must not increase badge (CRT on)")
		require.NotContains(t, rec.notified, u1.Id)

		// Mattermost documented default: @channel, @all, @here are suppressed in muted channel.
		rec.reset()
		postAsU2(t, ch, "", "broadcast @channel")
		postAsU2(t, ch, "", "broadcast @all")
		postAsU2(t, ch, "", "broadcast @here")
		badge, appErr = th.App.getUserBadgeCount(u1.Id, true)
		require.Nil(t, appErr)
		require.Equal(t, 0, badge, "@channel/@all/@here in muted channel are suppressed by default (CRT on)")
		require.NotContains(t, rec.notified, u1.Id)

		// Explicit @member root post in muted channel MUST increase badge (MentionCountRoot = 1).
		rec.reset()
		postAsU2(t, ch, "", "root ping @"+u1.Username)
		badge, appErr = th.App.getUserBadgeCount(u1.Id, true)
		require.Nil(t, appErr)
		require.Equal(t, 1, badge, "explicit @user root mention in muted channel must increase badge (CRT on)")
		gotNative, err := native.UnreadBadgeCount(context.Background(), u1.Id)
		require.NoError(t, err)
		require.Equal(t, 1, gotNative)
		require.Contains(t, rec.notified, u1.Id)

		// Explicit @member threaded reply in muted channel (CRT on) MUST increase thread unread mentions.
		rec.reset()
		postAsU2(t, ch, plainRoot.Id, "thread reply ping @"+u1.Username)
		chMentions, err := th.App.Srv().Store().User().GetUnreadCount(u1.Id, true)
		require.NoError(t, err)
		require.Equal(t, int64(1), chMentions, "channel root mentions must stay 1")
		threadMentions, err := th.App.Srv().Store().Thread().GetTotalUnreadMentions(u1.Id, "", model.GetUserThreadsOpts{})
		require.NoError(t, err)
		require.Equal(t, int64(1), threadMentions, "CRT thread unread mentions must increment for explicit @user reply in muted channel")

		badge, appErr = th.App.getUserBadgeCount(u1.Id, true)
		require.Nil(t, appErr)
		require.Equal(t, 2, badge, "getUserBadgeCount (CRT on) must include root + threaded reply @user mentions in muted channel")
		gotNative, err = native.UnreadBadgeCount(context.Background(), u1.Id)
		require.NoError(t, err)
		require.Equal(t, 2, gotNative)
		require.Contains(t, rec.notified, u1.Id)

		// Plain non-mention follow-up reply in the same thread must NOT increase badge.
		rec.reset()
		postAsU2(t, ch, plainRoot.Id, "plain follow-up reply without mention")
		badge, appErr = th.App.getUserBadgeCount(u1.Id, true)
		require.Nil(t, appErr)
		require.Equal(t, 2, badge, "plain reply in followed thread in muted channel must not increase badge")
	})
}
