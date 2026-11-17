# Ghi nhận patch license thử nghiệm Mattermost

- Ngày: 2026-11-16.
- Repository: `sources/mattermost`; branch `phoenix/v12.0.2`.
- Commit gốc: `78e1dee2e0ea59fda12239215cdf79473e943e28`.
- Phạm vi: 7 file source (6 Go, 1 TypeScript) và duy nhất file Markdown này.
- Chưa commit, push, deploy, restart hoặc thay image/plugin runtime.

## Hành vi sau patch

Các helper entitlement/tier trả trạng thái cho phép. Hết hạn, grace period và trial trả `false`; license được coi đã bắt đầu; `DaysToExpiration()` trả `36500`.

Runtime dùng license mô phỏng SKU `advanced` khi thiếu license. `SetLicense`, kể cả nhận license thật tier thấp hoặc `nil`, chuẩn hóa sang mô phỏng trước khi lưu, tạo client map và phát listener. Điều này giữ worker, plugin, SQL store và webapp nhất quán.

License mô phỏng bật 30 feature boolean, gồm `AutoTranslation`; `Cloud=false` giữ đúng loại triển khai self-hosted. Số seat hiển thị `1000000`, `IsSeatCountEnforced=false`, `Limits=nil` theo quy ước không giới hạn. Ngày hết hạn hiển thị `2100-01-01 UTC`, tránh vượt miền JavaScript Date và tràn số khi đổi sang duration.

Không xóa rải rác hàng trăm điều kiện endpoint: các điều kiện `License() == nil`, `license.Features.*` và kiểm tra tier nhận trạng thái cho phép từ các điểm trung tâm. Gate generic API/property/ID-only push và gate add-on riêng trên webapp được bỏ trực tiếp.

## Toàn bộ chỗ sửa

| File | Hàm/điểm sửa | Thay đổi |
| --- | --- | --- |
| `server/public/model/license.go` | `IsExpired`, `IsPastGracePeriod`, `IsWithinExpirationPeriod` | Trả `false`. |
| `server/public/model/license.go` | `DaysToExpiration`, `IsStarted`, `IsTrialLicense`, `IsSanctionedTrial` | Lần lượt `36500`, `true`, `false`, `false`. |
| `server/public/model/license.go` | `HasEnterpriseMarketplacePlugins`, `HasRemoteClusterService`, `HasSharedChannels`, `HasMHPNS`, `HasAddOn` | Trả `true`, kể cả license nil. |
| `server/public/model/license.go` | `MinimumProfessionalLicense`, `MinimumEnterpriseLicense`, `MinimumEnterpriseAdvancedLicense` | Trả `true`. |
| `server/public/model/license.go` | `NewPermissiveLicense` (mới) | Tạo license mô phỏng Advanced có đủ feature; bỏ import không còn dùng. |
| `server/public/pluginapi/license.go` | `IsEnterpriseLicensedOrDevelopment`, `IsE10LicensedOrDevelopment`, `IsE20LicensedOrDevelopment`, `IsEnterpriseAdvancedLicensedOrDevelopment` | Trả `true`, bao gồm caller gửi nil/SKU thấp. |
| `server/channels/app/platform/license.go` | `License` | Đọc trạng thái đã chuẩn hóa, fallback license mô phỏng khi chưa có. |
| `server/channels/app/platform/license.go` | `SetLicense` | Chuẩn hóa đầu vào trước khi lưu/phát listener. |
| `server/channels/app/platform/license.go` | `ClientLicense`, `SetClientLicense` | Client map nhất quán; không nhận `IsLicensed=false` từ setter. |
| `server/channels/api4/handlers.go` | `requireLicense` | Luôn trả nil. |
| `server/channels/app/properties/license_check.go` | `LicenseCheckHook.requireLicense` | Luôn trả nil; cập nhật comment. |
| `server/channels/app/notification_push.go` | `BuildPushNotificationMessage` | Bỏ `notificationInterface.CheckLicense()` khỏi điều kiện downgrade ID-only; giữ fallback khi implementation không có. |
| `webapp/channels/src/utils/addons.ts` | `licenseHasAddOn`, `isUnlicensedAddOn` | Add-on tên không rỗng có entitlement; không chặn trang cấu hình do license. Tên rỗng vẫn không hợp lệ. |

## Các phần giữ và giới hạn

- `IsCloud`, `IsMattermostEntry`, structural validation của `LicenseRecord`/`TrialLicenseRequest`, serializer/default/constructor test fixture giữ ý nghĩa dữ liệu. Không ép mọi boolean thành true vì sẽ làm license đồng thời Cloud/Entry/trial/expired và kích hoạt sai đường xử lý.
- Xác thực chữ ký/parse file license vẫn giữ; file hỏng/giả chữ ký vẫn bị từ chối khi upload. Runtime mô phỏng không cần upload license.
- Quyền truy cập, xác thực, guard Phoenix/tenant, feature flag, cấu hình và kiểm tra implementation/service vẫn giữ.
- Checkout không chứa đầy đủ implementation Enterprise. `server/enterprise/external_imports.go` tham chiếu `github.com/mattermost/enterprise/...` cho LDAP, SAML, cluster, ABAC, retention, notification, auto-translation, v.v. Patch không tạo các implementation này và không chứng minh chúng chạy được.
- Plugin tải riêng/binary đã build sẵn chưa tự đổi theo source. Dịch vụ bên ngoài vẫn có kiểm tra riêng; sửa helper local không cấp quyền dùng dịch vụ của nhà cung cấp.
- Các file giấy phép/copyright/NOTICE không bị sửa. Khi mua license, đảo patch bên dưới để quay lại source trước thử nghiệm.

## Kiểm chứng

| Check | Kết quả |
| --- | --- |
| `gofmt` trên 6 file Go | PASS. |
| `git diff --check` | PASS; thông báo LF/CRLF theo cấu hình Git Windows. |
| Từ `server`: `go build ./public/model ./public/pluginapi ./channels/api4 ./channels/app/platform ./channels/app/properties ./channels/utils` | PASS, exit 0. |
| Từ `server`: `go build -tags sourceavailable ./cmd/mattermost ./cmd/mmctl` | PASS, exit 0. Kiểm tra compile, chưa nạp runtime. |
| Từ `webapp/channels`: `node ../node_modules/eslint/bin/eslint.js src/utils/addons.ts` | PASS, exit 0. |
| Go smoke bên dưới: `go run <file-tam.go>` từ `server` | PASS: 58 assertions; không cần DB. |
| Frontend smoke: transpile TypeScript rồi chạy module với license undefined/rỗng/false/add-on khác | PASS: 12 assertions; không thay full UI/tsc/build. |
| Từ `server`: `go test -json ./public/model -run '^(TestLicense\|TestMinimum\|TestIsCloud\|TestTrialLicense)'` trên source cuối | FAIL: 12 test cấp cao về gate gốc; PASS: 6 test cấp cao về feature defaults/map, IsCloud, LicenseRecord validation/pre-save và TrialLicenseRequest validation. Không sửa assertion cũ để che khác biệt có chủ đích. |
| Lần chạy suite gốc trước chỉnh cuối: `go test ./public/model ./channels/app/platform ./channels/app/properties ./channels/api4` | FAIL: test license gốc, `TestFindManifest_FolderPermission` trên Windows; ba package cần DB thất bại `password authentication failed for user mmuser (28P01)`. |
| `git -c core.autocrlf=false apply --reverse --check` với patch trong tài liệu | PASS; chưa đảo patch trên workspace thật. |
| Đảo patch trên bản sao tạm, so bytes với `git show HEAD:<file>` | PASS: khôi phục byte-identical 7/7 file về commit gốc. |
| HTTP/browser/runtime/production | NOT RUN. |

12 test cấp cao FAIL trên source cuối: `TestLicenseIsExpired`, `TestLicenseIsPastGracePeriod`, `TestLicenseIsStarted`, `TestLicense_IsTrialLicense`, `TestLicense_IsSanctionedTrial`, `TestLicenseHasSharedChannels`, `TestLicenseHasMHPNS`, `TestLicenseHasAddOn`, `TestLicenseAddOnsJSON`, `TestMinimumProfessionalLicense`, `TestMinimumEnterpriseLicense`, `TestMinimumEnterpriseAdvancedLicense`.

Lỗi thiết lập kiểm chứng đã sửa: chạy ESLint từ `webapp/channels` để dùng đúng `eslint.config.mjs`; tránh MaxInt cho timestamp/limits; sửa smoke count cho đúng 30 feature; dùng override `core.autocrlf=false` cho thao tác apply patch LF. Không thay đổi cấu hình Git toàn cục.

## Dấu vân tay source

Blob trước patch thuộc commit gốc ở đầu tài liệu. SHA-256 sau patch là bytes working tree khi ghi báo cáo. Nếu source sửa tiếp, kiểm tra diff trước khi khôi phục.

| File | Git blob trước patch | SHA-256 sau patch |
| --- | --- | --- |
| `server/public/model/license.go` | `ea29fefd13b96996ca4f362bf764770ef0c0e744` | `a0f0f1656ffd39a6084c7cd4c511ece51c4385f9abd0572d3f1693b3c9bbb1fb` |
| `server/public/pluginapi/license.go` | `42b3c78b12fc1a754e3bed23344ebab3de84fd40` | `c4ddb787b42dd0f934c4b85d9f4233b7c4d4d4324df153e96ece32c6780c4aa4` |
| `server/channels/app/platform/license.go` | `497705f5f0356a16fa276b5134388de8e480fe97` | `87bdb9a7e8a60f12c03a0d2f9307a9e780bbabd9d66d9eb0632a6492b8b952d8` |
| `server/channels/api4/handlers.go` | `fb4265b76c1164c97a8679a039e16c84e514eff1` | `6edf810cb09d8fb5b28805a60cfd08814f29e2551efcae7a12f5d92fcd477f0b` |
| `server/channels/app/properties/license_check.go` | `63a13dd4381cae8ceca8453e9f700f5f50d12836` | `d9c30dde14784a2289f55186b1d5a32ba06e8d4f6f9833d9076bc67245714f68` |
| `server/channels/app/notification_push.go` | `59a13c03ed6f2d93887eadb4f86c680cd7bf87fa` | `ce3c35d231c5ea37d7328fdf7c120b907d098a9ed19419749b09c53810ac22ef` |
| `webapp/channels/src/utils/addons.ts` | `930e8f3fcf4a5e72f6c4ce6747c8d3546a7978e6` | `4cd36f68f397cf6b6435f57b621c4a9bbda94136e0bdc7d9a40096e48564a91e` |

## Khôi phục

Giữ file này để có patch chính xác của lượt thử nghiệm. Python bên dưới chạy từ `sources/mattermost`; kiểm tra trước và dừng nếu source xung đột. Đảo patch trả về **commit fork Phoenix gốc**, giữ các patch Phoenix có trước lượt này. Không restore cả repo hoặc `git reset --hard`.

```python
from pathlib import Path
import re
import subprocess

# Chạy từ sources/mattermost. Dừng nếu source đã thay đổi gây xung đột.
report = Path("PHOENIX_LICENSE_BYPASS_EXPERIMENT.md").read_text(encoding="utf-8")
patch = re.search(r"(?ms)^```diff\n(.*?)^```\s*$", report).group(1).encode("utf-8")
# Override chỉ cho lệnh này để patch LF chạy đúng với Git autocrlf trên Windows.
cmd = ["git", "-c", "core.autocrlf=false", "apply", "--reverse"]
subprocess.run(cmd + ["--check"], input=patch, check=True)
# Chỉ chạy dòng dưới khi thực sự muốn khôi phục:
subprocess.run(cmd, input=patch, check=True)
```

Sau khi đảo, rebuild artifact nếu cần đổi runtime; đảo source không tự đổi container/plugin đang chạy. Có thể xóa báo cáo sau khi đã khôi phục và lưu bằng chứng cần thiết.

## Go smoke chạy lại

Lưu code vào file `.go` tạm ngoài repo rồi chạy `go run <file>` từ `sources/mattermost/server`. Không kết nối DB, không khởi động server, không thay runtime.

```go
package main

import (
    "fmt"
    "reflect"
    "strconv"
    "github.com/mattermost/mattermost/server/public/model"
    "github.com/mattermost/mattermost/server/public/pluginapi"
    "github.com/mattermost/mattermost/server/v8/channels/app/platform"
    "github.com/mattermost/mattermost/server/v8/channels/app/properties"
    "github.com/mattermost/mattermost/server/v8/channels/utils"
)

func check(ok bool, label string) {
    if !ok { panic(label) }
    fmt.Println("PASS", label)
}
func main() {
    l := model.NewPermissiveLicense()
    check(model.IsValidId(l.Id), "valid experimental ID")
    check(l.Limits == nil && !l.IsSeatCountEnforced, "no seat or metered history/call limits")
    check(l.ExpiresAt > model.GetMillis() && l.ExpiresAt < 8640000000000000 && l.DaysToExpiration() > 60, "safe future expiry and day arithmetic")
    fields := reflect.ValueOf(l.Features).Elem()
    typ := fields.Type()
    count := 0
    for i:=0; i<fields.NumField(); i++ {
        if fields.Field(i).Type().String() != "*bool" { continue }
        enabled := fields.Field(i).Elem().Bool()
        if typ.Field(i).Name == "Cloud" { check(!enabled, "self-hosted Cloud metadata") } else {
            check(enabled, "feature "+typ.Field(i).Name); count++
        }
    }
    check(count == 30, "all declared feature booleans covered")
    for _, sample := range []*model.License{nil, {}, model.NewTestLicenseSKU("professional"), model.NewTestLicenseSKU("unknown")} {
        check(model.MinimumProfessionalLicense(sample) && model.MinimumEnterpriseLicense(sample) && model.MinimumEnterpriseAdvancedLicense(sample), "tier checks accept nil/lower/unknown")
        check(sample.HasEnterpriseMarketplacePlugins() && sample.HasRemoteClusterService() && sample.HasSharedChannels() && sample.HasMHPNS() && sample.HasAddOn("arbitrary-addon"), "entitlement checks accept nil/lower/unknown")
        check(pluginapi.IsEnterpriseLicensedOrDevelopment(nil,sample) && pluginapi.IsE10LicensedOrDevelopment(nil,sample) && pluginapi.IsE20LicensedOrDevelopment(nil,sample) && pluginapi.IsEnterpriseAdvancedLicensedOrDevelopment(nil,sample), "plugin API tier checks bypassed")
        check(!sample.IsExpired() && !sample.IsPastGracePeriod() && !sample.IsWithinExpirationPeriod() && sample.IsStarted(), "expiry gates permit all licenses")
    }
    ps := &platform.PlatformService{}
    check(ps.License().SkuShortName == "advanced", "platform missing license fallback")
    cl := ps.ClientLicense()
    check(cl["IsLicensed"] == "true" && cl["SkuShortName"] == "advanced" && cl["Cloud"] == "false", "client/server entitlement agreement")
    expiry, err := strconv.ParseInt(cl["ExpiresAt"],10,64)
    check(err == nil && expiry == l.ExpiresAt, "client expiry serializes safely")
    ps.SetClientLicense(map[string]string{"IsLicensed":"false", "LDAP":"false"})
    check(ps.ClientLicense()["IsLicensed"] == "true" && ps.ClientLicense()["LDAP"] == "true", "restrictive client update remains permissive")
    hook:= properties.NewLicenseCheckHook(func()*model.License{panic("license provider must not be called")},"managed")
    field:= &model.PropertyField{GroupID:"managed"}
    returned, hookErr:=hook.PreCreatePropertyField(nil,field)
    check(returned==field && hookErr==nil, "managed property create bypass")
    check(hook.PreDeletePropertyValue(nil,"managed","value")==nil, "managed property delete bypass")
    _, validationErr:=utils.LicenseValidator.ValidateLicense([]byte("invalid"))
    check(validationErr!=nil,"malformed signed license still rejected")
}


```

## Patch chính xác của lượt sửa

Dùng cho `git apply --reverse`; bao gồm mọi file source được sửa, không gồm chính báo cáo này.

```diff
diff --git a/server/channels/api4/handlers.go b/server/channels/api4/handlers.go
index fb4265b76c..e96a7b3edf 100644
--- a/server/channels/api4/handlers.go
+++ b/server/channels/api4/handlers.go
@@ -235,10 +235,6 @@ func (api *API) RateLimitedHandler(apiHandler http.Handler, settings model.RateL
 }
 
 func requireLicense(c *Context) *model.AppError {
-	if c.App.Channels().License() == nil {
-		err := model.NewAppError("", "api.license_error", nil, "", http.StatusNotImplemented)
-		return err
-	}
 	return nil
 }
 
diff --git a/server/channels/app/notification_push.go b/server/channels/app/notification_push.go
index 59a13c03ed..011557b0de 100644
--- a/server/channels/app/notification_push.go
+++ b/server/channels/app/notification_push.go
@@ -807,7 +807,10 @@ func (a *App) BuildPushNotificationMessage(rctx request.CTX, contentsConfig stri
 	var msg *model.PushNotification
 
 	notificationInterface := a.ch.Notification
-	if (notificationInterface == nil || notificationInterface.CheckLicense() != nil) && contentsConfig == model.IdLoadedNotification {
+	// Phoenix experimental build: the ID-only notification mode is not gated by
+	// the Enterprise license check. Keep only the dependency check for a missing
+	// notification implementation.
+	if notificationInterface == nil && contentsConfig == model.IdLoadedNotification {
 		contentsConfig = model.GenericNotification
 	}
 
diff --git a/server/channels/app/platform/license.go b/server/channels/app/platform/license.go
index 497705f5f0..35762feda7 100644
--- a/server/channels/app/platform/license.go
+++ b/server/channels/app/platform/license.go
@@ -44,7 +44,12 @@ func (ps *PlatformService) SetLicenseManager(impl einterfaces.LicenseInterface)
 }
 
 func (ps *PlatformService) License() *model.License {
-	return ps.licenseValue.Load()
+	// Phoenix experiment: installed, missing or removed licenses have the same
+	// effective entitlements. Restore this file to resume signed-license gating.
+	if license := ps.licenseValue.Load(); license != nil {
+		return license
+	}
+	return model.NewPermissiveLicense()
 }
 
 func (ps *PlatformService) LoadLicense() {
@@ -255,6 +260,9 @@ func (ps *PlatformService) SaveLicense(licenseBytes []byte) (*model.License, *mo
 }
 
 func (ps *PlatformService) SetLicense(license *model.License) bool {
+	// Normalize listener inputs as well as getters: otherwise license changes
+	// could disable workers, plugins or SQL-store features behind their backs.
+	license = model.NewPermissiveLicense()
 	oldLicense := ps.licenseValue.Load()
 
 	defer func() {
@@ -307,14 +315,14 @@ func (ps *PlatformService) ValidateAndSetLicenseBytes(b []byte) error {
 }
 
 func (ps *PlatformService) SetClientLicense(m map[string]string) {
-	ps.clientLicenseValue.Store(m)
+	ps.clientLicenseValue.Store(utils.GetClientLicense(ps.License()))
 }
 
 func (ps *PlatformService) ClientLicense() map[string]string {
 	if clientLicense, _ := ps.clientLicenseValue.Load().(map[string]string); clientLicense != nil {
 		return clientLicense
 	}
-	return map[string]string{"IsLicensed": "false"}
+	return utils.GetClientLicense(ps.License())
 }
 
 func (ps *PlatformService) RemoveLicense() *model.AppError {
diff --git a/server/channels/app/properties/license_check.go b/server/channels/app/properties/license_check.go
index 63a13dd438..efed53125a 100644
--- a/server/channels/app/properties/license_check.go
+++ b/server/channels/app/properties/license_check.go
@@ -15,9 +15,8 @@ var ErrLicenseRequired = errors.New("license_error: an Enterprise license is req
 // LicenseProvider is a function that returns the current license.
 type LicenseProvider func() *model.License
 
-// LicenseCheckHook enforces license requirements for property operations on
-// specific groups. Operations on groups without a license requirement pass
-// through without checks.
+// LicenseCheckHook retains its API for the Phoenix experiment but permits
+// property operations regardless of license. Other property hooks still apply.
 type LicenseCheckHook struct {
 	BasePropertyHook
 	licenseProvider LicenseProvider
@@ -26,8 +25,7 @@ type LicenseCheckHook struct {
 
 var _ PropertyHook = (*LicenseCheckHook)(nil)
 
-// NewLicenseCheckHook creates a hook that requires an Enterprise license for
-// all field and value operations on the given property groups.
+// NewLicenseCheckHook retains the upstream constructor for easy restoration.
 func NewLicenseCheckHook(licenseProvider LicenseProvider, managedGroupIDs ...string) *LicenseCheckHook {
 	ids := make(map[string]struct{}, len(managedGroupIDs))
 	for _, id := range managedGroupIDs {
@@ -39,16 +37,10 @@ func NewLicenseCheckHook(licenseProvider LicenseProvider, managedGroupIDs ...str
 	}
 }
 
-// requireLicense returns ErrLicenseRequired when groupID is in the managed set
-// and no Enterprise license is active. Unmanaged groups and licensed calls
-// return nil.
+// requireLicense is disabled for the Phoenix experiment.
 func (h *LicenseCheckHook) requireLicense(groupID string) error {
-	if _, managed := h.managedGroupIDs[groupID]; !managed {
-		return nil
-	}
-	if !model.MinimumEnterpriseLicense(h.licenseProvider()) {
-		return ErrLicenseRequired
-	}
+	// Phoenix experimental build: property groups remain governed by their
+	// normal permission checks, without an Enterprise license gate.
 	return nil
 }
 
diff --git a/server/public/model/license.go b/server/public/model/license.go
index ea29fefd13..3b8a8591d4 100644
--- a/server/public/model/license.go
+++ b/server/public/model/license.go
@@ -5,10 +5,7 @@ package model
 
 import (
 	"encoding/json"
-	"fmt"
 	"net/http"
-	"slices"
-	"strings"
 	"time"
 )
 
@@ -363,28 +360,25 @@ func (f *Features) SetDefaults() {
 }
 
 func (l *License) IsExpired() bool {
-	return l.ExpiresAt < GetMillis()
+	// Phoenix experimental build: feature access is not revoked by expiry.
+	return false
 }
 
 func (l *License) IsPastGracePeriod() bool {
-	timeDiff := GetMillis() - l.ExpiresAt
-	return timeDiff > LicenseGracePeriod
+	// Phoenix experimental build: keep Enterprise features available after expiry.
+	return false
 }
 
 func (l *License) IsWithinExpirationPeriod() bool {
-	days := l.DaysToExpiration()
-	return days <= 60 && days >= 58
+	return false
 }
 
 func (l *License) DaysToExpiration() int {
-	dif := l.ExpiresAt - GetMillis()
-	d, _ := time.ParseDuration(fmt.Sprint(dif) + "ms")
-	days := d.Hours() / 24
-	return int(days)
+	return 36500
 }
 
 func (l *License) IsStarted() bool {
-	return l.StartsAt < GetMillis()
+	return true
 }
 
 // Cloud preview is a cloud license, that is also a trial, and the difference between the start and end date is exactly 1 hour.
@@ -397,61 +391,61 @@ func (l *License) IsCloud() bool {
 }
 
 func (l *License) IsTrialLicense() bool {
-	return l.IsTrial || (l.ExpiresAt-l.StartsAt) == trialDuration.Milliseconds() || (l.ExpiresAt-l.StartsAt) == adminTrialDuration.Milliseconds()
+	return false
 }
 
 func (l *License) IsSanctionedTrial() bool {
-	duration := l.ExpiresAt - l.StartsAt
-
-	return l.IsTrialLicense() &&
-		(duration >= sanctionedTrialDurationLowerBound.Milliseconds() || duration <= sanctionedTrialDurationUpperBound.Milliseconds())
+	return false
 }
 
 func (l *License) HasEnterpriseMarketplacePlugins() bool {
-	return *l.Features.EnterprisePlugins ||
-		l.SkuShortName == LicenseShortSkuE20 ||
-		MinimumProfessionalLicense(l)
+	return true
 }
 
 func (l *License) HasRemoteClusterService() bool {
-	if l == nil {
-		return false
-	}
-
-	// If SharedChannels is enabled then RemoteClusterService must be enabled.
-	if l.HasSharedChannels() {
-		return true
-	}
-
-	return (l.Features != nil && l.Features.RemoteClusterService != nil && *l.Features.RemoteClusterService) ||
-		MinimumProfessionalLicense(l)
+	return true
 }
 
 func (l *License) HasSharedChannels() bool {
-	if l == nil {
-		return false
-	}
-
-	return (l.Features != nil && l.Features.SharedChannels != nil && *l.Features.SharedChannels) ||
-		MinimumProfessionalLicense(l)
+	return true
 }
 
 // HasMHPNS reports whether the license grants access to the Mattermost hosted push notification service.
 func (l *License) HasMHPNS() bool {
-	return l != nil && l.Features != nil && l.Features.MHPNS != nil && *l.Features.MHPNS
+	return true
 }
 
 // HasAddOn reports whether the license grants the named add-on. Unlike the
 // SKU-derived helpers above, this deliberately has no minimum-license fallback:
 // an add-on is bought separately from the tier.
 func (l *License) HasAddOn(addOn string) bool {
-	if l == nil {
-		return false
-	}
+	return true
+}
 
-	return slices.ContainsFunc(l.AddOns, func(a string) bool {
-		return strings.EqualFold(a, addOn)
-	})
+// NewPermissiveLicense returns the in-process license used by the Phoenix
+// experimental build instead of installed license entitlements. It deliberately
+// keeps Cloud false so a self-hosted server does not impersonate Mattermost Cloud,
+// while enabling the Enterprise Advanced feature surface for local evaluation.
+func NewPermissiveLicense() *License {
+	features := &Features{AutoTranslation: new(true)}
+	features.SetDefaults()
+	// Stay within 32-bit and JavaScript integer ranges. Seats are not enforced.
+	users := 1_000_000
+	features.Users = &users
+	// A nil Limits means no metered restrictions, matching a paid license that
+	// omits limits. MaxInt values overflow plugins' duration conversions and
+	// positive PostHistory would unnecessarily enable history truncation jobs.
+	return &License{
+		Id:              "phoenixlicenseexperiment01",
+		IssuedAt:        1,
+		StartsAt:        1,
+		ExpiresAt:       4102444800000, // 2100-01-01 UTC; valid in JavaScript Date.
+		Customer:        &Customer{Id: "phoenixlicenseexperiment01", Name: "Phoenix Experimental"},
+		Features:        features,
+		SkuName:         "Enterprise Advanced (Phoenix Experimental)",
+		SkuShortName:    LicenseShortSkuEnterpriseAdvanced,
+		IsNonProduction: true,
+	}
 }
 
 // NewTestLicense returns a license that expires in the future and has the given features.
@@ -533,16 +527,16 @@ func (lr *LicenseRecord) PreSave() {
 // MinimumProfessionalLicense returns true if the provided license is at least a professional license.
 // Higher tier licenses also satisfy the condition.
 func MinimumProfessionalLicense(license *License) bool {
-	return license != nil && LicenseToLicenseTier[license.SkuShortName] >= ProfessionalTier
+	return true
 }
 
 // MinimumEnterpriseLicense returns true if the provided license is at least a enterprise license.
 // Higher tier licenses also satisfy the condition.
 func MinimumEnterpriseLicense(license *License) bool {
-	return license != nil && LicenseToLicenseTier[license.SkuShortName] >= EnterpriseTier
+	return true
 }
 
 // MinimumEnterpriseAdvancedLicense returns true if the provided license is at least an Enterprise Advanced license.
 func MinimumEnterpriseAdvancedLicense(license *License) bool {
-	return license != nil && LicenseToLicenseTier[license.SkuShortName] >= EnterpriseAdvancedTier
+	return true
 }
diff --git a/server/public/pluginapi/license.go b/server/public/pluginapi/license.go
index 42b3c78b12..67b4eaa31d 100644
--- a/server/public/pluginapi/license.go
+++ b/server/public/pluginapi/license.go
@@ -8,11 +8,8 @@ import (
 // Enterprise License, or has `EnableDeveloper` and `EnableTesting` configuration settings
 // enabled signaling a non-production, developer mode.
 func IsEnterpriseLicensedOrDevelopment(config *model.Config, license *model.License) bool {
-	if license != nil {
-		return true
-	}
-
-	return IsConfiguredForDevelopment(config)
+	// Phoenix experimental build: preserve API, bypass entitlement checks.
+	return true
 }
 
 // isValidSkuShortName returns whether the SKU short name is one of the known strings;
@@ -34,54 +31,20 @@ func isValidSkuShortName(license *model.License) bool {
 // Enterprise E10 License or a Mattermost Professional License, or has `EnableDeveloper` and
 // `EnableTesting` configuration settings enabled, signaling a non-production, developer mode.
 func IsE10LicensedOrDevelopment(config *model.Config, license *model.License) bool {
-	if model.MinimumProfessionalLicense(license) {
-		return true
-	}
-
-	if !isValidSkuShortName(license) {
-		// As a fallback for licenses whose SKU short name is unknown, make a best effort to try
-		// and use the presence of a known E10/Professional feature as a check to determine licensing.
-		if license != nil &&
-			license.Features != nil &&
-			license.Features.LDAP != nil &&
-			*license.Features.LDAP {
-			return true
-		}
-	}
-
-	return IsConfiguredForDevelopment(config)
+	return true
 }
 
 // IsE20LicensedOrDevelopment returns true when the server is licensed with a legacy Mattermost
 // Enterprise E20 License or a Mattermost Enterprise License, or has `EnableDeveloper` and
 // `EnableTesting` configuration settings enabled, signaling a non-production, developer mode.
 func IsE20LicensedOrDevelopment(config *model.Config, license *model.License) bool {
-	if model.MinimumEnterpriseLicense(license) {
-		return true
-	}
-
-	if !isValidSkuShortName(license) {
-		// As a fallback for licenses whose SKU short name is unknown, make a best effort to try
-		// and use the presence of a known E20/Enterprise feature as a check to determine licensing.
-		if license != nil &&
-			license.Features != nil &&
-			license.Features.FutureFeatures != nil &&
-			*license.Features.FutureFeatures {
-			return true
-		}
-	}
-
-	return IsConfiguredForDevelopment(config)
+	return true
 }
 
 // IsEnterpriseAdvancedLicensedOrDevelopment returns true when the server is licensed with a Mattermost
 // Enterprise Advanced License, or has `EnableDeveloper` and `EnableTesting` configuration settings
 func IsEnterpriseAdvancedLicensedOrDevelopment(config *model.Config, license *model.License) bool {
-	if license != nil && license.SkuShortName == model.LicenseShortSkuEnterpriseAdvanced {
-		return true
-	}
-
-	return IsConfiguredForDevelopment(config)
+	return true
 }
 
 // IsConfiguredForDevelopment returns true when the server has `EnableDeveloper` and `EnableTesting`
diff --git a/webapp/channels/src/utils/addons.ts b/webapp/channels/src/utils/addons.ts
index 930e8f3fcf..e8d03fa8d6 100644
--- a/webapp/channels/src/utils/addons.ts
+++ b/webapp/channels/src/utils/addons.ts
@@ -3,34 +3,13 @@
 
 import type {ClientLicense} from '@mattermost/types/config';
 
-// Comma-separated because ClientLicense is Record<string, string>. Split rather
-// than substring match, so 'crossguard' is not satisfied by 'crossguard-premium'.
-export function licenseHasAddOn(license: ClientLicense | undefined, addOn: string): boolean {
-    // ''.split(',') is [''], which an empty target would match.
-    if (!addOn || license?.IsLicensed !== 'true') {
-        return false;
-    }
-
-    const target = addOn.toLowerCase();
-    return (license.AddOns ?? '').split(',').some((granted) => granted.toLowerCase() === target);
-}
-
-// The license starts as {} and loads asynchronously. GetClientLicense always sets
-// IsLicensed, so its presence is what distinguishes loaded from empty.
-function isLicenseLoaded(license: ClientLicense | undefined): boolean {
-    return license?.IsLicensed !== undefined;
+// Phoenix experiment: mirror the server's permissive HasAddOn helper. Empty
+// names are still invalid; actual plugin availability is checked separately.
+export function licenseHasAddOn(_license: ClientLicense | undefined, addOn: string): boolean {
+    return Boolean(addOn);
 }
 
-// False while the license is still loading, otherwise a licensed admin deep-linking
-// to the page gets a flash of "your license does not include it".
+// Phoenix experiment: suppress license-only plugin configuration restrictions.
 export function isUnlicensedAddOn(requiredAddOn: string | undefined, license: ClientLicense | undefined): boolean {
-    if (!requiredAddOn) {
-        return false;
-    }
-
-    if (!isLicenseLoaded(license)) {
-        return false;
-    }
-
-    return !licenseHasAddOn(license, requiredAddOn);
+    return Boolean(requiredAddOn && !licenseHasAddOn(license, requiredAddOn));
 }
```
