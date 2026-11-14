---
name: quiz
description: Tạo câu hỏi kiểu phỏng vấn Backend Go dựa trên đúng đoạn code người học vừa viết, chờ trả lời rồi chấm và đưa câu trả lời mẫu. Chỉ chạy khi người học gọi /quiz.
argument-hint: "[file, function hoặc chủ đề]"
disable-model-invocation: true
allowed-tools: Bash(git status *) Bash(git diff *) Bash(git log *)
---

# Quiz phỏng vấn từ code của chính người học

Phạm vi: $ARGUMENTS

## 1. Xác định code để hỏi

- Nếu phạm vi ở trên trống: dùng các thay đổi chưa commit (`git diff HEAD`). Nếu không có thay đổi nào, dùng commit gần nhất (`git log -1 -p`).
- Nếu là tên file hoặc function: đọc đúng phần đó.
- Nếu là chủ đề (ví dụ "channel", "transaction"): tìm chỗ dùng chủ đề đó trong code của dự án và hỏi dựa trên chỗ ấy.
- Đọc `docs/learning-log.md` để biết người học đã nắm gì. Không hỏi về khái niệm thuộc giai đoạn chưa học (xem lộ trình trong `CLAUDE.md`).

## 2. Đặt 4 câu hỏi cùng lúc, đánh số

1. **Đọc code:** "Dòng ... trong `file.go` làm gì, và vì sao cần nó?"
2. **Tình huống lỗi:** "Nếu ... (chết giữa chừng, chạy hai lần, hai worker cùng chạy, DB chậm) thì đoạn code này xử lý thế nào?"
3. **Khái niệm:** so sánh hoặc giải thích một khái niệm Go hay backend xuất hiện trong code (ví dụ buffered và unbuffered channel, `WaitGroup` và `errgroup`).
4. **Mở rộng kiểu phỏng vấn:** liên hệ production, ví dụ "Nếu file lớn gấp 100 lần thì sao?", "Scale lên 10 instance cần lưu ý gì?".

Mỗi câu ngắn gọn, trả lời được bằng 2–4 câu. **Không đưa đáp án, không gợi ý.** Kết thúc bằng: "Bạn trả lời lần lượt nhé, không biết câu nào thì cứ ghi 'bỏ qua'."

## 3. Khi người học trả lời

Với từng câu:

- Đánh giá: **Đúng** / **Đúng một phần** / **Chưa đúng**.
- Giải thích ngắn chỗ thiếu hoặc sai, chỉ vào đúng dòng code liên quan.
- Đưa **câu trả lời mẫu kiểu phỏng vấn**: 2–4 câu, nói thành lời được, có ví dụ cụ thể từ dự án.

Cuối cùng:

- Tóm tắt một điểm mạnh và một điểm cần ôn thêm.
- Đề xuất 1–2 dòng cho `docs/learning-log.md` (khái niệm còn yếu hoặc vừa nắm chắc). Chỉ ghi vào file khi người học đồng ý.

Trả lời bằng tiếng Việt, giữ thuật ngữ kỹ thuật tiếng Anh.
