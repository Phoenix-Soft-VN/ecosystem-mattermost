// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package platform

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/public/shared/request"
	"github.com/mattermost/mattermost/server/v8/channels/jobs"
	"github.com/mattermost/mattermost/server/v8/channels/store"
	"github.com/mattermost/mattermost/server/v8/channels/store/sqlstore"
	"github.com/mattermost/mattermost/server/v8/channels/utils"
	"github.com/mattermost/mattermost/server/v8/einterfaces"
)

const (
	LicenseEnv = "MM_LICENSE"
)

// JWTClaims custom JWT claims with the needed information for the
// renewal process
type JWTClaims struct {
	LicenseID   string `json:"license_id"`
	ActiveUsers int64  `json:"active_users"`
	jwt.RegisteredClaims
}

func (ps *PlatformService) LicenseManager() einterfaces.LicenseInterface {
	return ps.licenseManager
}

func (ps *PlatformService) SetLicenseManager(impl einterfaces.LicenseInterface) {
	ps.licenseManager = impl
}

func (ps *PlatformService) License() *model.License {
	// Phoenix experiment: installed, missing or removed licenses have the same
	// effective entitlements. Restore this file to resume signed-license gating.
	if license := ps.licenseValue.Load(); license != nil {
		return license
	}
	return model.NewPermissiveLicense()
}

func (ps *PlatformService) LoadLicense() {
	c := request.EmptyContext(ps.logger)

	// ENV var overrides all other sources of license.
	licenseStr := os.Getenv(LicenseEnv)
	if licenseStr != "" {
		license, appErr := utils.LicenseValidator.LicenseFromBytes([]byte(licenseStr))
		if appErr != nil {
			ps.logger.Error("Failed to read license set in environment.", mlog.Err(appErr))
			return
		}

		// skip the restrictions if license is a sanctioned trial
		if !license.IsSanctionedTrial() && license.IsTrialLicense() {
			canStartTrialLicense, err := ps.licenseManager.CanStartTrial()
			if err != nil {
				ps.logger.Error("Failed to validate trial eligibility.", mlog.Err(err))
				return
			}

			if !canStartTrialLicense {
				ps.logger.Info("Cannot start trial multiple times.")
				return
			}
		}

		if err := ps.ValidateAndSetLicenseBytes([]byte(licenseStr)); err != nil {
			ps.logger.Info("License key from ENV is invalid.", mlog.Err(err))
		} else {
			ps.logger.Info("License key from ENV is valid, unlocking enterprise features.")
		}
		return
	}

	licenseId := ""
	// Read from master: SaveLicense writes the active license ID then calls
	// LoadLicense, so a replica read can return a stale ID and revert the license.
	activeLicense, nErr := ps.Store.System().GetByNameWithContext(sqlstore.RequestContextWithMaster(c), model.SystemActiveLicenseId)
	if nErr == nil {
		licenseId = activeLicense.Value
	}

	if !model.IsValidId(licenseId) {
		// Lets attempt to load the file from disk since it was missing from the DB
		license, licenseBytes, err := utils.GetAndValidateLicenseFileFromDisk(*ps.Config().ServiceSettings.LicenseFileLocation)
		if err != nil {
			ps.logger.Warn("Failed to get license from disk", mlog.Err(err))
		} else {
			if _, err := ps.SaveLicense(licenseBytes); err != nil {
				ps.logger.Error("Failed to save license key loaded from disk.", mlog.Err(err))
			} else {
				licenseId = license.Id
			}
		}
	}

	record, nErr := ps.Store.License().Get(sqlstore.RequestContextWithMaster(c), licenseId)
	if nErr != nil {
		// LoadLicense reruns on a 24h timer, so clearing the license on a store
		// failure would drop every entitlement, and deactivate add-on plugins,
		// until the next successful read.
		var nfErr *store.ErrNotFound
		if !errors.As(nErr, &nfErr) {
			ps.logger.Error("Failed to read the license from the database, keeping the current license.", mlog.Err(nErr))
			return
		}

		if ps.Config().FeatureFlags.EnableMattermostEntry && model.BuildEnterpriseReady == "true" {
			ps.logger.Info("Mattermost Entry is enabled. Unlocking enterprise features.")

			if ps.LicenseManager() == nil {
				ps.logger.Warn("License manager not available, setting license to nil.")
				ps.SetLicense(nil)
				return
			}

			ps.SetLicense(ps.LicenseManager().NewMattermostEntryLicense(ps.telemetryId))
		} else {
			ps.logger.Warn("License key from https://mattermost.com required to unlock enterprise features.", mlog.Err(nErr))
			ps.SetLicense(nil)
		}
		return
	}

	err := ps.ValidateAndSetLicenseBytes([]byte(record.Bytes))
	if err != nil {
		ps.logger.Info("License key is invalid.")
	}

	ps.logger.Info("License key is valid, unlocking enterprise features.")
}

func (ps *PlatformService) SaveLicense(licenseBytes []byte) (*model.License, *model.AppError) {
	licenseStr, err := utils.LicenseValidator.ValidateLicense(licenseBytes)
	if err != nil {
		return nil, utils.NewLicenseValidationAppError("addLicense", err)
	}

	var license model.License
	if jsonErr := json.Unmarshal([]byte(licenseStr), &license); jsonErr != nil {
		return nil, model.NewAppError("addLicense", "api.unmarshal_error", nil, "", http.StatusInternalServerError).Wrap(jsonErr)
	}

	if license.Features == nil {
		return nil, model.NewAppError("addLicense", "api.license.add_license.invalid.app_error", nil, "", http.StatusBadRequest).Wrap(errors.New("license.Features is nil"))
	}

	if license.Features.Users == nil {
		return nil, model.NewAppError("addLicense", "api.license.add_license.invalid.app_error", nil, "", http.StatusBadRequest).Wrap(errors.New("license.Features.Users is nil"))
	}

	// Phoenix experiment: the uploaded license's seat count is not enforced (the runtime
	// license is the normalized permissive one), so no user count queries are needed.
	// The uploaded license remains stored as the record; its features are not applied.
	ps.logger.Info("Uploaded license is stored; the Phoenix permissive license stays effective.",
		mlog.String("uploaded_license_id", license.Id),
		mlog.String("effective_license_id", ps.License().Id),
	)

	if license.IsExpired() {
		return nil, model.NewAppError("addLicense", model.ExpiredLicenseError, nil, "", http.StatusBadRequest)
	}

	if *ps.Config().JobSettings.RunJobs && ps.Jobs != nil {
		if err := ps.Jobs.StopWorkers(); err != nil && !errors.Is(err, jobs.ErrWorkersNotRunning) {
			ps.logger.Warn("Stopping job server workers failed", mlog.Err(err))
		}
	}

	if *ps.Config().JobSettings.RunScheduler && ps.Jobs != nil {
		if err := ps.Jobs.StopSchedulers(); err != nil && !errors.Is(err, jobs.ErrSchedulersNotRunning) {
			ps.logger.Error("Stopping job server schedulers failed", mlog.Err(err))
		}
	}

	defer func() {
		// restart job server workers - this handles the edge case where a license file is uploaded, but the job server
		// doesn't start until the server is restarted, which prevents the 'run job now' buttons in system console from
		// functioning as expected
		if *ps.Config().JobSettings.RunJobs && ps.Jobs != nil {
			if err := ps.Jobs.StartWorkers(); err != nil {
				ps.logger.Error("Starting job server workers failed", mlog.Err(err))
			}
		}
		if *ps.Config().JobSettings.RunScheduler && ps.Jobs != nil {
			if err := ps.Jobs.StartSchedulers(); err != nil && !errors.Is(err, jobs.ErrSchedulersRunning) {
				ps.logger.Error("Starting job server schedulers failed", mlog.Err(err))
			}
		}
	}()

	if ok := ps.SetLicense(&license); !ok {
		return nil, model.NewAppError("addLicense", model.ExpiredLicenseError, nil, "", http.StatusBadRequest)
	}

	record := &model.LicenseRecord{}
	record.Id = license.Id
	record.Bytes = string(licenseBytes)

	if err := ps.Store.License().Save(record); err != nil {
		if appErr := ps.RemoveLicense(); appErr != nil {
			ps.logger.Error("Failed to remove license after saving it to the license store failed", mlog.Err(appErr))
		}
		var appErr *model.AppError
		switch {
		case errors.As(err, &appErr):
			return nil, appErr
		default:
			return nil, model.NewAppError("addLicense", "api.license.add_license.save.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
		}
	}

	sysVar := &model.System{}
	sysVar.Name = model.SystemActiveLicenseId
	sysVar.Value = license.Id
	if err := ps.Store.System().SaveOrUpdate(sysVar); err != nil {
		appErr := ps.RemoveLicense()
		if appErr != nil {
			ps.logger.Error("Failed to remove license after saving it to the system store failed", mlog.Err(appErr))
		}
		return nil, model.NewAppError("addLicense", "api.license.add_license.save_active.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	// only on prem licenses set this in the first place
	if !license.IsCloud() {
		_, err := ps.Store.System().PermanentDeleteByName(model.SystemHostedPurchaseNeedsScreening)
		if err != nil {
			ps.logger.Warn(fmt.Sprintf("Failed to remove %s system store key", model.SystemHostedPurchaseNeedsScreening))
		}
	}

	if err := ps.ReloadConfig(); err != nil {
		ps.logger.Warn("Failed to reload config after saving license", mlog.Err(err))
	}
	if appErr := ps.InvalidateAllCaches(); appErr != nil {
		ps.logger.Warn("Failed to invalidate cache after saving license", mlog.Err(appErr))
	}

	return &license, nil
}

func (ps *PlatformService) SetLicense(license *model.License) bool {
	// Normalize listener inputs as well as getters: otherwise license changes
	// could disable workers, plugins or SQL-store features behind their backs.
	license = model.NewPermissiveLicense()
	oldLicense := ps.licenseValue.Load()

	defer func() {
		for _, listener := range ps.licenseListeners {
			if oldLicense == nil {
				listener(nil, license)
			} else {
				listener(oldLicense, license)
			}
		}
	}()

	if license != nil {
		license.Features.SetDefaults()

		ps.licenseValue.Store(license)

		ps.clientLicenseValue.Store(utils.GetClientLicense(license))

		if oldLicense == nil || oldLicense.Id != license.Id {
			ps.logLicense("Set license", license)
		}

		return true
	}

	if oldLicense != nil {
		ps.logLicense("Cleared license", oldLicense)
	}

	ps.licenseValue.Store((*model.License)(nil))
	ps.clientLicenseValue.Store(map[string]string(nil))

	return false
}

func (ps *PlatformService) ValidateAndSetLicenseBytes(b []byte) error {
	licenseStr, err := utils.LicenseValidator.ValidateLicense(b)
	if err != nil {
		return errors.Wrap(err, "Failed to decode license from JSON")
	}

	var license model.License
	if err := json.Unmarshal([]byte(licenseStr), &license); err != nil {
		return errors.Wrap(err, "Failed to decode license from JSON")
	}

	ps.SetLicense(&license)
	return nil
}

func (ps *PlatformService) SetClientLicense(m map[string]string) {
	ps.clientLicenseValue.Store(utils.GetClientLicense(ps.License()))
}

func (ps *PlatformService) ClientLicense() map[string]string {
	if clientLicense, _ := ps.clientLicenseValue.Load().(map[string]string); clientLicense != nil {
		return clientLicense
	}
	return utils.GetClientLicense(ps.License())
}

func (ps *PlatformService) RemoveLicense() *model.AppError {
	if license := ps.licenseValue.Load(); license == nil {
		return nil
	}

	ps.logger.Info("Remove license.", mlog.String("id", model.SystemActiveLicenseId))

	sysVar := &model.System{}
	sysVar.Name = model.SystemActiveLicenseId
	sysVar.Value = ""

	if err := ps.Store.System().SaveOrUpdate(sysVar); err != nil {
		return model.NewAppError("RemoveLicense", "app.system.save.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}

	ps.SetLicense(nil)
	if err := ps.ReloadConfig(); err != nil {
		ps.logger.Warn("Failed to reload config after removing license", mlog.Err(err))
	}
	if appErr := ps.InvalidateAllCaches(); appErr != nil {
		ps.logger.Warn("Failed to invalidate cache after removing license", mlog.Err(appErr))
	}

	return nil
}

func (ps *PlatformService) AddLicenseListener(listener func(oldLicense, newLicense *model.License)) string {
	id := model.NewId()
	ps.licenseListeners[id] = listener
	return id
}

func (ps *PlatformService) RemoveLicenseListener(id string) {
	delete(ps.licenseListeners, id)
}

func (ps *PlatformService) GetSanitizedClientLicense() map[string]string {
	return utils.GetSanitizedClientLicense(ps.ClientLicense())
}

// RequestTrialLicense satisfies a trial request locally.
//
// Phoenix experiment: the permissive license is already effective, so no request is sent
// to the vendor license server and no trial license is invented or persisted. The effective
// license is re-applied so the client license map and license listeners are refreshed.
func (ps *PlatformService) RequestTrialLicense(trialRequest *model.TrialLicenseRequest) *model.AppError {
	if trialRequest == nil {
		return model.NewAppError("RequestTrialLicense", "api.license.request-trial.bad-request", nil, "", http.StatusBadRequest)
	}

	ps.SetLicense(model.NewPermissiveLicense())

	if ps.logger != nil {
		ps.logger.Info("Trial license request satisfied locally by the Phoenix permissive license; no vendor request was sent.")
	}

	return nil
}

func (ps *PlatformService) logLicense(message string, license *model.License) {
	if ps.logger == nil {
		return
	}

	logger := ps.logger.With(
		mlog.String("id", license.Id),
		mlog.Time("issued_at", model.GetTimeForMillis(license.IssuedAt)),
		mlog.Time("starts_at", model.GetTimeForMillis(license.StartsAt)),
		mlog.Time("expires_at", model.GetTimeForMillis(license.ExpiresAt)),
		mlog.String("sku_name", license.SkuName),
		mlog.String("sku_short_name", license.SkuShortName),
		mlog.Bool("is_trial", license.IsTrial),
		mlog.Bool("is_gov_sku", license.IsGovSku),
		mlog.Bool("is_non_production", license.IsNonProduction),
	)

	if license.Customer != nil {
		logger = logger.With(mlog.String("customer_id", license.Customer.Id))
	}

	if license.Features != nil {
		logger = logger.With(
			mlog.Int("features_users", *license.Features.Users),
			mlog.Map("features", license.Features.ToMap()),
		)
	}

	if len(license.AddOns) > 0 {
		logger = logger.With(mlog.Array("add_ons", license.AddOns))
	}

	logger.Info(message)
}
