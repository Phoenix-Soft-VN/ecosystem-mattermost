# Làm mới header Chat theo Portal

**Status:** Accepted — yêu cầu trực tiếp, không có câu hỏi mở
**Date:** 2026-11-18
**Scope:** Frontend Mattermost PhoenixShell
**Reference:** `portal/src/components/tenant-shell/tenant-header.tsx` và `tenant-shell.module.css`

## Mục tiêu

Đưa header Chat về cùng ngôn ngữ với Portal: chiều cao thoáng hơn, brand gồm logo + tenant + tên ứng dụng, nhóm công cụ có divider, tài khoản hiển thị như một cụm thống nhất. Giữ nguyên hành vi launcher, mode preference, logout, snapshot Gateway và responsive.

## Phạm vi

- JSX: thêm nhãn `Chat`, fallback monogram cho tenant/account, nhóm account có nhãn phụ và divider.
- SCSS: dùng chiều cao 72px desktop / 64px mobile, hit-area tối thiểu 44px, panel/menu và segmented mode control đồng bộ bề mặt Portal.
- Test: header có brand app, account group, launcher và mode controls; không thay đổi shell authority hoặc navigation.

## Checks — Evidence: unit

Name: portal-like-header
Description: snapshot ready render có tenant display name, nhãn `Chat`, account display name, link `Đăng xuất`, launcher `Ứng dụng` và ba radio mode; fallback monogram xuất hiện khi logo/avatar thiếu.
Pass: các accessible roles/text trên có mặt, header không tạo thêm route hoặc request.

Name: header-responsive-contract
Description: header classes giữ vùng controls và main content trong shell; các test launcher, dark selection, authority failure hiện có vẫn PASS.
Pass: selector component suite PASS.

## Thực hiện và rollback

Mode: fast. Chỉ sửa `PhoenixShell` và stylesheet/test trong Mattermost source. Rollback bằng khôi phục commit header trước đó; không chạm Gateway, license, Chat data hay tenant permissions.
