// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api4

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/v8/einterfaces/mocks"
)

// Phoenix experiment regression tests. They need the DB-backed API test helper.

func TestPhoenixRequestTrialLicenseWithoutLicenseManager(t *testing.T) {
	th := Setup(t).InitBasic(t)

	th.App.Srv().Platform().SetLicenseManager(nil)

	var vendorRequests atomic.Int32
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vendorRequests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer vendor.Close()
	originalCWSURL := *th.App.Config().CloudSettings.CWSURL
	th.App.UpdateConfig(func(cfg *model.Config) { *cfg.CloudSettings.CWSURL = vendor.URL })
	defer th.App.UpdateConfig(func(cfg *model.Config) { *cfg.CloudSettings.CWSURL = originalCWSURL })

	t.Run("system admin is satisfied locally without a license manager", func(t *testing.T) {
		resp, err := th.SystemAdminClient.RequestTrialLicense(context.Background(), 100)
		require.NoError(t, err)
		CheckOKStatus(t, resp)
		require.Zero(t, vendorRequests.Load(), "no request may reach the vendor license server")
	})

	t.Run("extended request is satisfied locally and does not need the Cloud implementation", func(t *testing.T) {
		th.App.Srv().Cloud = nil
		resp, err := th.SystemAdminClient.RequestTrialLicenseWithExtraFields(context.Background(), &model.TrialLicenseRequest{
			Users:                 100,
			TermsAccepted:         true,
			ReceiveEmailsAccepted: false,
			ContactName:           "Contact",
			ContactEmail:          "contact@example.com",
			CompanyName:           "Company",
			CompanySize:           "50-99",
			CompanyCountry:        "Vietnam",
		})
		require.NoError(t, err)
		CheckOKStatus(t, resp)
		require.Zero(t, vendorRequests.Load())
	})

	t.Run("permission guard is retained", func(t *testing.T) {
		resp, err := th.Client.RequestTrialLicense(context.Background(), 100)
		require.Error(t, err)
		CheckForbiddenStatus(t, resp)
	})

	t.Run("null body is a bad request, not a panic", func(t *testing.T) {
		r, err := th.SystemAdminClient.DoAPIRequestWithHeaders(context.Background(), http.MethodPost, "/trial-license", "null", nil)
		if r != nil {
			defer r.Body.Close()
		}
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, r.StatusCode)
		CheckErrorID(t, err, "api.license.request-trial.bad-request")
	})
}

func TestPhoenixCloudEndpointsNotLicenseGated(t *testing.T) {
	th := Setup(t).InitBasic(t)

	require.False(t, th.App.Channels().License().IsCloud(), "runtime license stays self-hosted")

	t.Run("Cloud implementation present: license-only denial is gone", func(t *testing.T) {
		cloud := &mocks.CloudInterface{}
		cloud.On("GetCloudProducts", mock.Anything, mock.Anything).Return([]*model.Product{}, nil)
		th.App.Srv().Cloud = cloud

		_, resp, err := th.SystemAdminClient.GetCloudProducts(context.Background())
		require.NoError(t, err)
		CheckOKStatus(t, resp)
	})

	t.Run("missing implementation is still reported", func(t *testing.T) {
		th.App.Srv().Cloud = nil

		_, resp, err := th.SystemAdminClient.GetCloudProducts(context.Background())
		require.Error(t, err)
		CheckBadRequestStatus(t, resp)
		CheckErrorID(t, err, "api.server.cws.needs_enterprise_edition")
	})

	t.Run("CloudSettings.Disable is still honored", func(t *testing.T) {
		th.App.Srv().Cloud = &mocks.CloudInterface{}
		th.App.UpdateConfig(func(cfg *model.Config) { cfg.CloudSettings.Disable = model.NewPointer(true) })
		defer th.App.UpdateConfig(func(cfg *model.Config) { cfg.CloudSettings.Disable = model.NewPointer(false) })

		_, resp, err := th.SystemAdminClient.GetCloudProducts(context.Background())
		require.Error(t, err)
		CheckUnprocessableEntityStatus(t, resp)
		CheckErrorID(t, err, "api.server.cws.disabled")
	})
}

func TestPhoenixIPFilteringNotLicenseGated(t *testing.T) {
	th := Setup(t).InitBasic(t)

	require.False(t, th.App.Channels().License().IsCloud(), "runtime license stays self-hosted")

	t.Run("implementation present: license-only denial is gone", func(t *testing.T) {
		ipFiltering := &mocks.IPFilteringInterface{}
		ipFiltering.On("GetIPFilters").Return(&model.AllowedIPRanges{}, nil)
		th.App.Srv().IPFiltering = ipFiltering

		_, resp, err := th.SystemAdminClient.GetIPFilters(context.Background())
		require.NoError(t, err)
		CheckOKStatus(t, resp)
	})

	t.Run("missing implementation is still reported", func(t *testing.T) {
		th.App.Srv().IPFiltering = nil

		_, resp, err := th.SystemAdminClient.GetIPFilters(context.Background())
		require.Error(t, err)
		CheckNotImplementedStatus(t, resp)
		CheckErrorID(t, err, "api.context.ip_filtering.not_available.app_error")
	})

	t.Run("permission guard is retained", func(t *testing.T) {
		th.App.Srv().IPFiltering = &mocks.IPFilteringInterface{}

		_, resp, err := th.Client.GetIPFilters(context.Background())
		require.Error(t, err)
		CheckForbiddenStatus(t, resp)
	})
}
