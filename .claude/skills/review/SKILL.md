---
name: review
description: Review code Go của người học (thay đổi chưa commit, hoặc file/function được chỉ định) theo docs/03-coding-guidelines.md. Gợi ý bằng câu hỏi trước, không tự sửa. Chỉ chạy khi người học gọi /review.
argument-hint: "[file hoặc tên function]"
disable-model-invocation: true
allowed-tools: Bash(git status *) Bash(git diff *) Bash(gofmt *) Bash(go vet *) Bash(go test *)
---

# Review code cho người học Go

Phạm vi cần review: $ARGUMENTS

## 1. Xác định phạm vi

- Nếu phạm vi ở trên trống: review **các thay đổi chưa commit**. Chạy `git status --short`, rồi `git diff HEAD`. Nếu repo chưa có commit nào, dùng `git diff` và đọc trực tiếp các file mới.
- Nếu có tên file hoặc function: chỉ review đúng phần đó.
- Tìm mã yêu cầu liên quan trong `docs/01-requirement-conversion-pipeline.md` (ví dụ IMP-4, WRK-7) để đối chiếu hành vi.

## 2. Chạy kiểm tra tự động

Chạy và báo kết quả ngắn gọn:

- `gofmt -l .`
- `go vet ./...`
- `go test -race` cho package bị ảnh hưởng

Lệnh nào lỗi thì ghi lại nguyên văn phần lỗi quan trọng, không tự sửa.

## 3. Review theo thứ tự mức độ

1. **Đúng yêu cầu:** hành vi có khớp mã yêu cầu trong `docs/01` không.
2. **Dữ liệu và đa luồng:** chết giữa chừng thì sao, chạy hai lần thì sao, nhiều bản cùng chạy thì sao. Goroutine có đường thoát, channel do bên gửi đóng, biến dùng chung được bảo vệ, lệnh gửi có `select` với `ctx.Done()`.
3. **Lỗi và context:** lỗi được bọc bằng `%w` kèm ngữ cảnh, không nuốt lỗi, `ctx` được truyền xuống mọi I/O.
4. **Database:** SQL chỉ nằm trong `internal/store`, dùng placeholder `?`, `rows.Close()` và `rows.Err()`, transaction ngắn và có `defer tx.Rollback()`.
5. **Test:** có test table-driven cho logic quan trọng chưa.
6. **Dư thừa** (theo mục "Code chân phương" trong `CLAUDE.md`): code không có chỗ dùng; tham số, tuỳ chọn, cấu hình không mã yêu cầu nào cần; interface chỉ có một cài đặt; wrapper quanh thư viện chuẩn; kiểm tra cho trường hợp không thể xảy ra; comment lặp lại code; function dài hơn khoảng 40 dòng. Với mỗi chỗ, nêu phần nào xoá hay gộp được mà hành vi không đổi.
7. **Phong cách:** đặt tên, cách đọc dễ hay khó.

Chỉ nêu vấn đề có thật. Không bịa vấn đề cho đủ danh sách.

## 4. Định dạng trả lời (tiếng Việt)

```
## Kiểm tra tự động
gofmt: ...  |  go vet: ...  |  go test -race: ...

## Làm tốt
- (1–3 ý cụ thể)

## Cần xem lại
1. [Nghiêm trọng | Nên sửa | Gợi ý] file.go:dòng: vấn đề là gì.
   Câu hỏi gợi ý: ... (dẫn người học tự tìm ra cách sửa)

## Câu hỏi kiểm tra hiểu bài
- (1–2 câu về khái niệm xuất hiện trong đoạn code)
```

## 5. Quy tắc

- **Không đưa code sửa và không sửa file** trong lần trả lời này. Chỉ khi người học yêu cầu ("cho đáp án", "sửa giúp") ở tin nhắn sau mới đưa code.
- Không có vấn đề nghiêm trọng nào và kiểm tra tự động đều qua: đề xuất lệnh `git add` và một commit message theo quy ước ở `docs/03` mục 12. **Không tự commit.**
