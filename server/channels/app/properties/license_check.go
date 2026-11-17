// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package properties

import (
	"errors"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/request"
)

var ErrLicenseRequired = errors.New("license_error: an Enterprise license is required")

// LicenseProvider is a function that returns the current license.
type LicenseProvider func() *model.License

// LicenseCheckHook retains its API for the Phoenix experiment but permits
// property operations regardless of license. Other property hooks still apply.
type LicenseCheckHook struct {
	BasePropertyHook
	licenseProvider LicenseProvider
	managedGroupIDs map[string]struct{}
}

var _ PropertyHook = (*LicenseCheckHook)(nil)

// NewLicenseCheckHook retains the upstream constructor for easy restoration.
func NewLicenseCheckHook(licenseProvider LicenseProvider, managedGroupIDs ...string) *LicenseCheckHook {
	ids := make(map[string]struct{}, len(managedGroupIDs))
	for _, id := range managedGroupIDs {
		ids[id] = struct{}{}
	}
	return &LicenseCheckHook{
		licenseProvider: licenseProvider,
		managedGroupIDs: ids,
	}
}

// requireLicense is disabled for the Phoenix experiment.
func (h *LicenseCheckHook) requireLicense(groupID string) error {
	// Phoenix experimental build: property groups remain governed by their
	// normal permission checks, without an Enterprise license gate.
	return nil
}

// Field pre-hooks

func (h *LicenseCheckHook) PreCreatePropertyField(_ request.CTX, field *model.PropertyField) (*model.PropertyField, error) {
	return field, h.requireLicense(field.GroupID)
}

func (h *LicenseCheckHook) PreUpdatePropertyField(_ request.CTX, groupID string, field *model.PropertyField) (*model.PropertyField, error) {
	return field, h.requireLicense(groupID)
}

func (h *LicenseCheckHook) PreUpdatePropertyFields(_ request.CTX, groupID string, fields []*model.PropertyField) ([]*model.PropertyField, error) {
	return fields, h.requireLicense(groupID)
}

func (h *LicenseCheckHook) PreDeletePropertyField(_ request.CTX, groupID string, _ string) error {
	return h.requireLicense(groupID)
}

func (h *LicenseCheckHook) PreCountPropertyFields(_ request.CTX, groupID string) error {
	return h.requireLicense(groupID)
}

// Field option hooks

func (h *LicenseCheckHook) PreChangePropertyFieldOptions(_ request.CTX, field *model.PropertyField) error {
	return h.requireLicense(field.GroupID)
}

func (h *LicenseCheckHook) PostGetPropertyFieldOptions(_ request.CTX, field *model.PropertyField, _ *model.PropertyFieldOptionPageFilter, options []*model.PropertyFieldOption) ([]*model.PropertyFieldOption, error) {
	return options, h.requireLicense(field.GroupID)
}

func (h *LicenseCheckHook) PreGetPropertyFieldOptions(_ request.CTX, field *model.PropertyField, filter *model.PropertyFieldOptionPageFilter) (*model.PropertyFieldOptionPageFilter, error) {
	return filter, h.requireLicense(field.GroupID)
}

// Field post-hooks

func (h *LicenseCheckHook) PostGetPropertyField(_ request.CTX, field *model.PropertyField) (*model.PropertyField, error) {
	return field, h.requireLicense(field.GroupID)
}

func (h *LicenseCheckHook) PostGetPropertyFields(_ request.CTX, fields []*model.PropertyField) ([]*model.PropertyField, error) {
	if len(fields) == 0 {
		return fields, nil
	}
	return fields, h.requireLicense(fields[0].GroupID)
}

// Value pre-hooks

func (h *LicenseCheckHook) PreCreatePropertyValue(_ request.CTX, value *model.PropertyValue) (*model.PropertyValue, error) {
	return value, h.requireLicense(value.GroupID)
}

func (h *LicenseCheckHook) PreCreatePropertyValues(_ request.CTX, values []*model.PropertyValue) ([]*model.PropertyValue, error) {
	if len(values) == 0 {
		return values, nil
	}
	return values, h.requireLicense(values[0].GroupID)
}

func (h *LicenseCheckHook) PreUpdatePropertyValue(_ request.CTX, groupID string, value *model.PropertyValue) (*model.PropertyValue, error) {
	return value, h.requireLicense(groupID)
}

func (h *LicenseCheckHook) PreUpdatePropertyValues(_ request.CTX, groupID string, values []*model.PropertyValue) ([]*model.PropertyValue, error) {
	return values, h.requireLicense(groupID)
}

func (h *LicenseCheckHook) PreUpsertPropertyValue(_ request.CTX, value *model.PropertyValue) (*model.PropertyValue, error) {
	return value, h.requireLicense(value.GroupID)
}

func (h *LicenseCheckHook) PreUpsertPropertyValues(_ request.CTX, values []*model.PropertyValue) ([]*model.PropertyValue, error) {
	if len(values) == 0 {
		return values, nil
	}
	return values, h.requireLicense(values[0].GroupID)
}

func (h *LicenseCheckHook) PreDeletePropertyValue(_ request.CTX, groupID string, _ string) error {
	return h.requireLicense(groupID)
}

func (h *LicenseCheckHook) PreDeletePropertyValuesForTarget(_ request.CTX, groupID string, _ string, _ string) error {
	return h.requireLicense(groupID)
}

func (h *LicenseCheckHook) PreDeletePropertyValuesForField(_ request.CTX, groupID string, _ string) error {
	return h.requireLicense(groupID)
}

// Value post-hooks

func (h *LicenseCheckHook) PostGetPropertyValue(_ request.CTX, value *model.PropertyValue) (*model.PropertyValue, error) {
	if value == nil {
		return value, nil
	}
	return value, h.requireLicense(value.GroupID)
}

func (h *LicenseCheckHook) PostGetPropertyValues(_ request.CTX, values []*model.PropertyValue) ([]*model.PropertyValue, error) {
	if len(values) == 0 {
		return values, nil
	}
	return values, h.requireLicense(values[0].GroupID)
}
