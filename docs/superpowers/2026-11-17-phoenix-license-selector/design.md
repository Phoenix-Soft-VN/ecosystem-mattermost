# License hợp lệ từ getLicense

**Status:** Accepted — yêu cầu trực tiếp, không có câu hỏi mở
**Date:** 2026-11-17
**Scope:** Frontend
**Owner surface:** mattermost-redux/selectors/entities/general.getLicense

## 1. Mô tả yêu cầu
Selector trả license Enterprise Advanced của bản Phoenix thử nghiệm kể cả khi Redux chưa có license hoặc có license hết hạn/tier thấp. Actor: webapp và plugin sử dụng selector.

## 2. Phạm vi hiện thực
Chuẩn hóa selector, kiểm thử và ghi nhận trong PHOENIX_LICENSE_BYPASS_EXPERIMENT.md. Không thay đổi permission, cấu hình sản phẩm hoặc phân loại Cloud thực tế.
Playbooks bundle riêng mattermost-redux 11.8.0: thay import ở src/license.ts sang selector src/phoenix_license.ts với cùng contract; không sửa node_modules hoặc thêm dependency/cross-repo build import. Các check license rỗng/hết hạn/Cloud/cache áp dụng cho cả hai selector.

## 3. Hiện trạng
`general.ts` trả nguyên object Redux. Backend `model.NewPermissiveLicense` dùng SKU `advanced`, 1.000.000 seats, hạn 2100 và đánh dấu non-production. `ClientLicense` là `Record<string, string>`.

## 4. Các hướng đã cân nhắc
Chọn overlay license tại selector thay vì sửa Redux store: mọi caller nhận cùng contract, không mutate state. Dùng createSelector theo object license gốc để giữ reference ổn định.

## 5. Công nghệ
TypeScript, createSelector và Jest hiện có; không thêm dependency.

## 6. Technical
Overlay IsLicensed, SKU, seats, thời hạn, các feature boolean được backend xuất qua GetClientLicense. Cloud theo dữ liệu gốc, mặc định false; IsGovSku và AddOns giữ metadata gốc. Không giả lập dịch vụ Enterprise bị thiếu. Rollback: bỏ overlay, khôi phục getter cũ.

## 7. Chi tiết
Name: empty-license
Description: license undefined hoặc {} trả IsLicensed='true', SkuShortName='advanced', Users='1000000', ExpiresAt='4102444800000', IsTrial='false', IsNonProduction='true'; isFreeEdition=false.
Pass: giá trị trên đúng; mọi field kết quả là string; Cloud='false'.

Name: expired-license
Description: license IsLicensed='false', SKU='entry', ExpiresAt='1', LDAP='false', Compliance='false' vẫn trả license hợp lệ, LDAP/Compliance='true'; object gốc không đổi.
Pass: overlay thắng dữ liệu cũ, object đóng băng không bị mutate.

Name: classification-and-cache
Description: Cloud='true' hoặc 'false' giữ nguyên; IsGovSku và AddOns, metadata tùy biến được giữ; cùng object license trên state khác trả cùng reference; object license mới được phản ánh.
Pass: isCloudLicense đúng theo Cloud; reference ổn định và cập nhật khi license thay đổi.

Name: mobile-policy
Description: EnableMobileFileUpload='false' hoặc EnableMobileFileDownload='false' vẫn trả false dù license gốc IsLicensed/Compliance='false', vì license hiệu lực có Compliance='true'.
Pass: giữ policy cấu hình sau chuẩn hóa; cập nhật bốn kỳ vọng upstream tương ứng từ true sang false.

## 8. Phase hiện thực
Phase 1 — Mode: fast; Evidence: unit. Viết overlay, thêm các ca trên, chạy selector suite và lint có phạm vi. Mutation: none (Mode: fast); bỏ overlay hoặc bỏ memoization sẽ làm các ca tương ứng thất bại.

## 9. Rủi ro
Caller cần license thương mại thực tế nay nhận license thử nghiệm. Suite upstream mô phỏng edition cũ có thể lệch ngữ nghĩa; ghi nhận riêng, không bỏ qua các regression mới.

## 10. Câu hỏi
Không có.

## 11. Blockers
Không có.

## 12. Bước tiếp theo
Thực hiện patch và kiểm chứng theo các check đã chốt.
