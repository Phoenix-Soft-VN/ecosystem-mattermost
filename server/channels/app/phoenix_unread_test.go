// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost/server/public/model"
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
