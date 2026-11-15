# Nhật ký học: conversion-pipeline

Mỗi dòng ghi một khái niệm đã hiểu và tự dùng được trong code của dự án. Mục đích là ôn lại trước phỏng vấn, và để Claude Code biết người học đã nắm những gì.

| Ngày | Phần code | Khái niệm | Hiểu đến đâu / còn thắc mắc |
| --- | --- | --- | --- |
| 2026-11-15 | `cmd/gen` | `math/rand` với seed riêng (`rand.New(rand.NewSource(seed))`) | Cùng seed thì cùng file; nhưng thêm hay bớt một lần gọi `rng` làm dịch toàn bộ dữ liệu phía sau, nên file đổi dù logic gần như không đổi |
| 2026-11-15 | `cmd/gen` | Dữ liệu test phải khớp thứ tự xử lý của hệ thống | Dòng `INVALID` dừng ở importer, không tới worker. Worker kiểm tra campaign trước rồi mới gọi API, nên dòng `clk_old`, `clk_err` và dòng trùng cần campaign tồn tại (1–200) thì mới ra đúng kết quả mong đợi |
| 2026-11-15 | `internal/csvparse` | Pure function và data race; `string` tính theo byte (`len`) hay theo rune (`utf8.RuneCountInString`) | Hàm không ghi vào vùng nhớ chung thì gọi song song an toàn. Bỏ lỗi gốc của `strconv`/`time` để thông báo lưu trong DB ngắn và ổn định |
| 2026-11-15 | `internal/csvparse` | `time.Time`: `==` và `Equal`; MySQL `DATETIME` | `==` so cả múi giờ, `Equal` chỉ so thời điểm. `DATETIME` không lưu múi giờ; driver chuyển theo `loc` của DSN (mặc định UTC), nên mọi service phải dùng chung DSN |
| 2026-11-15 | `internal/csvparse` | `strconv.ParseInt(s, base, bitSize)` | `base` là cơ số, `bitSize` là kiểu đích; tràn số thì trả `ErrRange`, không cắt bớt. **Cần ôn:** đọc `go doc` trước khi đoán tham số |
| 2026-11-15 | `internal/csvparse` | Sentinel error, `%w`, `errors.Is` (chưa dùng) | Không so `err.Error()`; khai báo `var ErrX = errors.New(...)`, bọc bằng `%w`, kiểm tra bằng `errors.Is`. Sẽ gặp lại ở `store` (`ErrDuplicate`) |
| 2026-11-15 | `compose.yaml`, `migrations/` | Healthcheck, `initdb.d`, bind mount | `--wait` chờ healthcheck; `mysqladmin ping -h 127.0.0.1` (TCP) để không pass sớm lúc init. `initdb.d` chỉ chạy khi volume trống. Bind mount tới thư mục chưa có thì Docker tạo bằng root |
| 2026-11-15 | `wiremock/mappings/` | Priority, catch-all mapping | Status lạ (404) không có trong bảng phân loại thì nên biến thành lỗi rõ tên (`400 missing_idempotency_key`), để bug lộ ra ngay thay vì bị retry che đi |
| 2026-11-15 | `internal/store` | `*sql.DB` là pool; `ExecContext`, `RowsAffected` với `INSERT IGNORE`; placeholder | Một câu `INSERT` nhiều giá trị đã atomic nên không cần transaction. `IGNORE` hạ cả lỗi khác (cắt chuỗi) thành warning, nên phải validate trước. Chỉ ghép placeholder, không ghép giá trị |
| 2026-11-15 | `internal/store` | Integration test, blank import, `sql.Null*`, `parseTime` | Test tự `t.Skip` khi thiếu `MYSQL_DSN`; `_` import để driver tự đăng ký; `sql.Open` không kết nối; thiếu `parseTime` thì `DATETIME` về dạng `[]byte` và `Scan` vào `time.Time` lỗi |

## Câu hỏi còn mở

- 

## Câu chuyện để kể khi phỏng vấn

Ghi lại các tình huống thật gặp trong lúc làm: lỗi khó, quyết định thiết kế, cách debug.

- Generator ban đầu cho dòng `clk_old` trỏ cả tới campaign không tồn tại, nên dòng đó ra `campaign_not_found` thay vì `click_expired`. Bài học: kiểm tra điều kiện nội bộ rẻ (campaign) trước khi gọi hệ thống bên ngoài, vì gọi trước sẽ tốn rate limit và có thể gửi conversion sai sang nền tảng quảng cáo.
