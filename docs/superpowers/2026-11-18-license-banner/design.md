# Bỏ banner non-production trong bản Phoenix

**Status:** Accepted — yêu cầu trực tiếp, không có câu hỏi mở
**Date:** 2026-11-18
**Scope:** Backend và frontend Mattermost, selector Playbooks

## 1. Yêu cầu
Không hiển thị `Non-production license. Test or staging use only.` trong bản Phoenix.

## 2. Phạm vi
Đặt IsNonProduction=false tại factory server và hai selector. Giữ nguyên cấu trúc banner, quyền truy cập, dữ liệu tenant và các trường license khác.

## 3. Hiện trạng
Ba nguồn đang ép cờ true; component announcement đọc getLicense và hiện banner khi cờ bằng chuỗi 'true'. Production đã xác minh trả cờ true.

## 4. Lựa chọn
Sửa metadata ở nguồn thay vì xóa component: API và cả hai frontend thống nhất, kể cả Redux nhận license cũ.

## 5. Công nghệ
Go, TypeScript, Jest và recipe build source hiện có; không thêm dependency.

## 6. Contract
API serialize is_non_production=false từ model và client-license IsNonProduction='false'; selector override cả đầu vào cũ có cờ 'true'.

## 7. Checks — Evidence: unit
Name: backend-license
Description: NewPermissiveLicense trả IsNonProduction=false; JSON có is_non_production=false, SKU vẫn advanced.
Pass: ba điều kiện đúng.

Name: selector-license
Description: license undefined, {}, hoặc license cũ IsNonProduction='true' đều trả chuỗi 'false', không mutate input; Cloud và memoization giữ contract F11.
Pass: selector tests của host và plugin PASS.

Name: rendered-banner
Description: render component thật với Redux có cờ 'true', 'false' hoặc thiếu cờ đều không có .announcement-bar và không có dòng cảnh báo.
Pass: mọi trường hợp không render banner; không mock selector.

## 8. Thực hiện
Một phase Mode: fast: patch, unit/lint, commit và build source sạch, deploy riêng Mattermost rồi kiểm tra API cùng asset hiện hành. Mutation: none; trả cờ true sẽ làm các check trên fail.

## 9. Rủi ro
Client giữ asset cũ cần tải lại. Giữ cấu hình Gateway URL đã sửa cho avatar, volume và 33 quyền Chat đang hoạt động.

## 10. Câu hỏi
Không có.

## 11. Blockers
Không có.

## 12. Hoàn thành
Ghi kết quả và rollback trong báo cáo; không nhận browser login khách hàng PASS nếu không thực hiện.
