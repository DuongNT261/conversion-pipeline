---
name: ask
description: Hỏi đáp nhanh về source code của dự án (chỗ nào làm gì, vì sao viết vậy, luồng dữ liệu đi qua đâu, mã yêu cầu nào nằm ở file nào). Trả lời ngắn, dẫn đúng file:dòng, không sửa code. Chỉ chạy khi người học gọi /ask.
argument-hint: "<câu hỏi về code, ví dụ: chỗ nào chống ghi nhận trùng?>"
disable-model-invocation: true
allowed-tools: Bash(git log *) Bash(git grep *) Bash(git show *) Bash(go doc *)
---

# Hỏi đáp nhanh trong source

Câu hỏi: $ARGUMENTS

## 1. Tìm câu trả lời trong code, không đoán

- Tìm đúng chỗ trong repo bằng Grep/Read (`cmd/`, `internal/`, `migrations/`, `compose.yaml`, `Dockerfile`, `wiremock/`). Đọc đủ đoạn code liên quan trước khi trả lời.
- Câu hỏi về hành vi ("làm gì khi...", "vì sao...") thì đối chiếu thêm:
  - mã yêu cầu trong `docs/01` (IMP-x, REL-x, WRK-x, AC-x),
  - lý do thiết kế trong `docs/02`,
  - sự cố tương ứng trong `docs/04`,
  - bài học đã ghi trong `docs/learning-log.md` và `docs/interview-notes.md` (nếu file này có trên máy).
- Code không có thứ được hỏi thì nói rõ là **không có**, kèm chỗ gần nhất có liên quan. Không bịa tên hàm hay file.
- Câu hỏi về thư viện chuẩn hoặc dependency thì dùng `go doc` thay vì nhớ.
- Câu hỏi mơ hồ thì trả lời theo cách hiểu hợp lý nhất và nêu giả định trong một dòng, không hỏi lại.

## 2. Định dạng trả lời (tiếng Việt, ngắn)

```
**Trả lời:** 1–3 câu, đi thẳng vào câu hỏi.

**Trong code:**
- `file.go:dòng`: dòng hoặc khối đó làm gì (trích đoạn code ngắn nếu giúp rõ hơn, tối đa khoảng 10 dòng)

**Vì sao:** 1–2 câu, kèm mã yêu cầu / mục docs/02 / sự cố docs/04 nếu có.

**Nếu bị hỏi khi phỏng vấn:** (chỉ khi câu hỏi là "vì sao" hoặc "thế nào") 2–3 câu nói thành lời được.
```

- Câu hỏi kiểu "chỗ nào / file nào" thì chỉ cần phần **Trả lời** và **Trong code**.
- Câu hỏi về luồng ("dòng đi từ CSV tới API thế nào") thì liệt kê các bước theo thứ tự, mỗi bước một `file.go:dòng`.
- Không giảng lại toàn bộ khái niệm; người học cần thêm thì sẽ hỏi tiếp hoặc dùng `/quiz`.

## 3. Quy tắc

- **Chỉ đọc, không sửa file.** Thấy bug trong lúc tìm thì ghi một dòng "Lưu ý:" ở cuối, không tự sửa.
- Không chạy lệnh thay đổi trạng thái (git commit, docker, ghi DB). Có thể chạy `go test` cho đúng package liên quan nếu cần chứng minh hành vi, và báo kết quả.
- Code, tên hàm giữ nguyên tiếng Anh; giải thích bằng tiếng Việt.
- Kết thúc bằng: "Phần nào chưa rõ thì bạn hỏi lại nhé."
