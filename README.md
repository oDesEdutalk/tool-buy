# Tool Automation Trình Duyệt Bằng Go (Golang)

Tool tự động hóa trình duyệt điều khiển Chrome/Chromium qua CDP, tích hợp chế độ chống phát hiện bot (Stealth) và hỗ trợ mở website qua Proxy SOCKS5 (hỗ trợ cả proxy có mật khẩu và không có mật khẩu).

---

## 🛠️ Cài đặt & Build

Build file thực thi:
```powershell
go build -o tool_bot.exe main.go
```

---

## 🚀 Hướng dẫn sử dụng

### 1. Chạy với chuỗi dữ liệu tổng hợp (`-data`):
```powershell
go run main.go -data "4160467122625048|07|29|082|Elizabeth Bass|7920 N 107Th #9|Milwaukee|WI|53224|US|4143243575|lizjay2415@gmail.com" -address "820 Roxfield Ct, Buford GA 30518"
```
Hoặc dùng file exe:
```powershell
.\tool_bot.exe -data "4160467122625048|07|29|082|Elizabeth Bass|7920 N 107Th #9|Milwaukee|WI|53224|US|4143243575|lizjay2415@gmail.com" -address "820 Roxfield Ct, Buford GA 30518"
```

### 2. Các quy tắc phân tích chuỗi `-data`:
- **Email**: Tự động ghép từ Full Name bỏ khoảng trắng + `@gmail.com` (ví dụ: `Elizabeth Bass` -> `ElizabethBass@gmail.com`).
- **First Name & Last Name**: Tách tự động từ trường Full Name (`Elizabeth` & `Bass`).
- **Address & Suggestions**: Gõ địa chỉ vào ô input, tự động đợi bảng `SUGGESTIONS` xuất hiện và click chọn dòng gợi ý đầu tiên.
- **Phone**: Trích xuất và điền vào ô số điện thoại (`4143243575`).

---

## ⚙️ Các cờ tham số (Flags)

| Cờ | Mặc định | Mô tả |
| :--- | :--- | :--- |
| `-data` | `4160467122625048|07|...` | Chuỗi dữ liệu tổng hợp (CC, Month, Year, CVV, Full Name, Address, City, State, Zip, Country, Phone, Email) |
| `-address` | `820 Roxfield Ct, Buford GA 30518` | Địa chỉ tìm kiếm gợi ý (ghi đè nếu cần) |
| `-url` | `https://www.on.com/en-us/products/5-court-shorts-m-1mg1019/...` | Link sản phẩm cần mua |
| `-size` | `S` | Size sản phẩm cần chọn (S, M, L, XL, ...) |
| `-proxy` | Proxy mặc định | SOCKS5 proxy (`host:port:user:pass` hoặc `socks5://...`) |
| `-headless` | `false` | `true` để chạy ẩn |
