// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package platform

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest/mock"
	"github.com/mattermost/mattermost/server/v8/channels/store"
	"github.com/mattermost/mattermost/server/v8/channels/store/storetest/mocks"
)

// Phoenix experiment regression tests. They use the store mock but the package TestMain
// still needs the DB test infrastructure.

func phoenixSignedLicense(payload string) []byte {
	raw := append([]byte(payload), bytes.Repeat([]byte{'A'}, 256)...)
	return []byte(base64.StdEncoding.EncodeToString(raw))
}

// phoenixLicenseStores extends the store mock with only what SaveLicense,
// its cache invalidation and the follow-up LoadLicense need. UserStore only accepts
// ClearCaches, so any user counting (Count, AnalyticsGetSingleChannelGuestCount) panics.
func phoenixLicenseStores(th *TestHelper) (*mocks.LicenseStore, *mocks.SystemStore) {
	licenseStore := &mocks.LicenseStore{}
	systemStore := &mocks.SystemStore{}
	userStore := &mocks.UserStore{}
	userStore.On("ClearCaches").Return()
	teamStore, channelStore, postStore := &mocks.TeamStore{}, &mocks.ChannelStore{}, &mocks.PostStore{}
	fileStore, hookStore, trackStore := &mocks.FileInfoStore{}, &mocks.WebhookStore{}, &mocks.DeliveryTrackingStore{}
	for _, c := range []interface {
		On(string, ...any) *testifymock.Call
	}{teamStore, channelStore, postStore, fileStore, hookStore, trackStore} {
		c.On("ClearCaches").Return()
	}

	// Extend the helper's store mock, which already serves Status and Close.
	st := th.Service.Store.(*mocks.Store)
	st.On("License").Return(licenseStore)
	st.On("System").Return(systemStore)
	st.On("User").Return(userStore)
	st.On("Team").Return(teamStore)
	st.On("Channel").Return(channelStore)
	st.On("Post").Return(postStore)
	st.On("FileInfo").Return(fileStore)
	st.On("Webhook").Return(hookStore)
	st.On("DeliveryTracking").Return(trackStore)

	// LoadLicense, run by InvalidateAllCaches, reads the active license back; report it absent.
	systemStore.On("GetByNameWithContext", mock.Anything, model.SystemActiveLicenseId).
		Return(&model.System{Name: model.SystemActiveLicenseId, Value: "phoenixtestlicense0000000001"}, nil).Maybe()
	licenseStore.On("Get", mock.Anything, "phoenixtestlicense0000000001").
		Return(nil, store.NewErrNotFound("License", "phoenixtestlicense0000000001")).Maybe()

	return licenseStore, systemStore
}

func TestPhoenixSaveLicenseSkipsSeatCount(t *testing.T) {
	th := SetupWithStoreMock(t)
	licenseStore, systemStore := phoenixLicenseStores(th)
	licenseStore.On("Save", mock.MatchedBy(func(r *model.LicenseRecord) bool {
		return r.Id == "phoenixtestlicense0000000001"
	})).Return(nil).Once()
	systemStore.On("SaveOrUpdate", mock.Anything).Return(nil)
	systemStore.On("PermanentDeleteByName", mock.Anything).Return(nil, nil)

	license, appErr := th.Service.SaveLicense(phoenixSignedLicense(`{"id":"phoenixtestlicense0000000001","features":{"users":1}}`))
	require.Nil(t, appErr)
	require.Equal(t, 1, *license.Features.Users)

	require.Equal(t, "phoenixlicenseexperiment01", th.Service.License().Id)
	require.Equal(t, 1_000_000, *th.Service.License().Features.Users)
	licenseStore.AssertNumberOfCalls(t, "Save", 1)
}

func TestPhoenixSaveLicenseStructuralAndStoreErrors(t *testing.T) {
	t.Run("missing features", func(t *testing.T) {
		th := SetupWithStoreMock(t)
		phoenixLicenseStores(th)
		_, appErr := th.Service.SaveLicense(phoenixSignedLicense(`{"id":"phoenixtestlicense0000000001"}`))
		require.NotNil(t, appErr)
		require.Equal(t, "api.license.add_license.invalid.app_error", appErr.Id)
		require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	})

	t.Run("missing users", func(t *testing.T) {
		th := SetupWithStoreMock(t)
		phoenixLicenseStores(th)
		_, appErr := th.Service.SaveLicense(phoenixSignedLicense(`{"id":"phoenixtestlicense0000000001","features":{}}`))
		require.NotNil(t, appErr)
		require.Equal(t, "api.license.add_license.invalid.app_error", appErr.Id)
		require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	})

	t.Run("store save failure is reported", func(t *testing.T) {
		th := SetupWithStoreMock(t)
		licenseStore, systemStore := phoenixLicenseStores(th)
		licenseStore.On("Save", mock.Anything).Return(errors.New("disk full"))
		systemStore.On("SaveOrUpdate", mock.Anything).Return(nil)

		_, appErr := th.Service.SaveLicense(phoenixSignedLicense(`{"id":"phoenixtestlicense0000000001","features":{"users":1}}`))
		require.NotNil(t, appErr)
		require.Equal(t, "api.license.add_license.save.app_error", appErr.Id)
		require.Equal(t, http.StatusInternalServerError, appErr.StatusCode)
	})
}

func TestPhoenixRequestTrialLicenseIsLocal(t *testing.T) {
	th := SetupWithStoreMock(t)
	require.Nil(t, th.Service.LicenseManager())

	var vendorRequests atomic.Int32
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vendorRequests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer vendor.Close()
	th.Service.UpdateConfig(func(cfg *model.Config) { *cfg.CloudSettings.CWSURL = vendor.URL })

	appErr := th.Service.RequestTrialLicense(&model.TrialLicenseRequest{Users: 100, TermsAccepted: true, Email: "a@example.com"})
	require.Nil(t, appErr)
	require.Zero(t, vendorRequests.Load())
	require.Equal(t, "advanced", th.Service.License().SkuShortName)

	appErr = th.Service.RequestTrialLicense(nil)
	require.NotNil(t, appErr)
	require.Equal(t, "api.license.request-trial.bad-request", appErr.Id)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Zero(t, vendorRequests.Load())
}
