# Hướng Dẫn & Tài Liệu Logic Hệ Thống Chạy Đa Luồng (Multi-Thread Checkout)

Tài liệu chi tiết về kiến trúc, luồng xử lý và hướng dẫn vận hành công cụ checkout tự động đa luồng bằng **Golang + Chromium (Go-Rod)**.

---

## 1. Sơ Đồ Quy Trình Hoạt Động (Architecture Flow)

```mermaid
flowchart TD
    A[Khởi động Chương trình] --> B[Quét phần cứng: CPU & RAM thực tế]
    B --> C[Tính toán & Đưa ra Khuyến nghị số Luồng]
    C --> D[Nhập số luồng muốn chạy: 1 - 50]
    D --> E[Đọc file: proxies.txt, cards.txt, discounts.txt]
    E --> F[Khởi tạo Worker Pool & Semaphore giới hạn luồng]
    
    subgraph Worker_Execution [Mỗi Luồng Chạy Độc Lập]
        F --> G[Cấp phát Proxy SOCKS5 + Bridge Local Port riêng]
        G --> H[Tạo thư mục UserData riêng biệt cho trình duyệt]
        H --> I[Mở trình duyệt Chromium Headless + Kích hoạt Stealth]
        I --> J[Truy cập Website & Tắt Popup Cookies]
        J --> K[Chọn Size sản phẩm & Bấm Add to Bag]
        K --> L[Chuyển hướng sang Checkout]
        L --> M[Điền Email, Họ Tên, Gợi ý Địa chỉ & SĐT]
        M --> N[Áp dụng Mã Khuyến Mại / Giảm giá]
        N --> O[Điền thông tin Thẻ tín dụng vào Iframe bảo mật]
        O --> P[Chờ cập nhật tiền & Bấm Pay Now]
    end

    P -->|Thành công| Q[Ghi log vào results_success.txt]
    P -->|Thất bại| R[Ghi log vào results_failed.txt]
    Q --> S[Tổng kết Báo cáo Hoàn tất]
    R --> S
```

---

## 2. Chi Tiết Logic Từng Bước

### Bước 1: Kiểm Tra Tài Nguyên Máy & Đánh Giá Giới Hạn Luồng
- Hệ thống gọi API Windows `GlobalMemoryStatusEx` từ thư viện `kernel32.dll` để lấy:
  - **Số nhân CPU (`runtime.NumCPU()`)**
  - **Tổng RAM & RAM Khả Dụng thực tế (Available RAM)**
- **Công thức ước tính an toàn:**
  - Mỗi trình duyệt Chromium Headless tiêu hao khoảng **250MB – 350MB RAM**.
  - Dành ra **1.5GB RAM** cho Hệ điều hành và tác vụ nền.
  - $\text{Số luồng tối đa} = \frac{\text{RAM khả dụng} - 1.5\text{GB}}{320\text{MB}}$
  - $\text{Số luồng khuyến nghị} = \text{CPU Cores} \times 2$ (đảm bảo máy không bị giật lag).

---

### Bước 2: Khởi Tạo Môi Trường Độc Lập Cho Từng Luồng (Isolate Environment)
Để chạy 10 – 50 luồng đồng thời mà không bị xung đột, mỗi luồng được cấp:
1. **Local SOCKS5 Bridge Port riêng (`127.0.0.1:0`)**:
   - Tự động bắt cổng TCP ngẫu nhiên để xác thực Proxy Upstream mà không bao giờ trùng lặp port giữa các luồng.
2. **User Data Directory riêng (`os.MkdirTemp`)**:
   - Mỗi trình duyệt có bộ nhớ cache, cookie, local storage tách biệt hoàn toàn.
3. **Bộ Sinh & Tiêm Vân Tay Chuẩn BitBrowser (BitBrowser-Grade Fingerprint)**:
   - Mỗi luồng tự động tạo một hồ sơ phần cứng và vân tay độc nhất, không bao giờ trùng lặp:
     - **CPU Cores (`hardwareConcurrency`)**: Ngẫu nhiên 4, 8, 12, 16, 24, 32 cores.
     - **RAM (`deviceMemory`)**: Ngẫu nhiên 8GB, 16GB, 32GB.
     - **Card đồ họa (WebGL GPU)**: Giả lập GPU rời/tích hợp thực tế (NVIDIA RTX 3060/3070/4060, Intel Iris Xe, UHD 630...).
     - **Canvas & AudioContext Noise**: Bơm nhiễu siêu việt độc lập trên mỗi luồng để vượt qua kiểm tra hash của Cloudflare/Shopify.
     - **Bảo vệ WebRTC**: Kích hoạt cờ chống rò rỉ địa chỉ IP thật qua giao thức WebRTC.

---

### Bước 3: Phân Bổ Dữ Liệu & Cơ Chế Quản Lý Proxy (ProxyManager)
- **Proxy Độc Lập (1 Luồng = 1 Proxy)**: Mỗi luồng/tác vụ nhận đúng **1 Proxy riêng biệt** từ `proxies.txt`, hoàn toàn không dùng chung hay trùng lặp proxy giữa các luồng.
- **Thất Bại Tự Động Đổi Proxy Khác**:
  - Khi một tác vụ gặp lỗi (dù là lỗi mạng `net::ERR_CONNECTION_CLOSED`, proxy die hay cổng thanh toán báo lỗi/từ chối thẻ):
  - Hệ thống ghi nhận proxy đó vào **`proxies_failed.txt`** kèm lý do cụ thể.
  - Tự động lấy **proxy khả dụng tiếp theo** trong danh sách `proxies.txt` để thử lại đơn hàng ngay lập tức.
  - Loại bỏ các proxy đã hỏng khỏi `proxies.txt`.
- **Thành Công Giữ Nguyên Proxy Cho Phiên Tiếp Theo**:
  - Khi thanh toán thành công với một proxy, tool sẽ tự động cập nhật lại file `proxies.txt`, **đưa proxy thành công lên vị trí số 1 (dòng đầu tiên)**.
  - Ở các phiên làm việc tiếp theo (hoặc khi bật lại tool), proxy này sẽ tiếp tục được ưu tiên sử dụng để mua hàng.
- **Xử Lý Khi Thiếu Proxy**: Nếu số lượng proxy trong `proxies.txt` ít hơn số thẻ/profile, hệ thống sẽ **chỉ sử dụng số proxy còn lại trong file** tương ứng với số tác vụ khả thi, đồng thời gửi cảnh báo đến Telegram.
- **Cảnh Báo Telegram Tự Động**: Khi file `proxies.txt` hết proxy hoặc toàn bộ proxy đã bị lỗi không thể chạy tiếp, tool sẽ tự động gửi thông báo khẩn cấp đến tài khoản Telegram của bạn.
- **Mã giảm giá**: `discounts[i % len(discounts)]`
- **Thẻ & Profile**: Mỗi tác vụ lấy 1 dòng từ `cards.txt`.

---

### Bước 4: Quy Trình Thao Tác Trình Duyệt (Human-like Actions)
1. **Xử lý Cookies**: Quét và bấm tắt tự động banner cookie (`onetrust-accept`, `Accept all`).
2. **Chọn Size**: Tự động mở dropdown chọn size theo tham số (`-size S/M/L/XL`).
3. **Thêm giỏ hàng & Checkout**: Bấm `Add to bag` và đợi container giỏ hàng xuất hiện để bấm `Checkout`.
4. **Điền thông tin Checkout**:
   - Gõ Email, Họ tên, Địa chỉ với tốc độ người thật (Delay tự nhiên 45–95ms/ký tự, dừng ngắt quãng tự nhiên ở dấu chấm/ký tự đặc biệt 120–250ms, ngắt quãng cụm 4 số thẻ 200–380ms để chống bot detection).
   - Gõ địa chỉ, quét tọa độ dòng gợi ý (Suggestions dropdown) và click bằng chuột CDP.
   - Chờ API xác thực địa chỉ tự động cập nhật State/Zip và tải biểu phí vận chuyển.
   - Gõ số điện thoại.
5. **Nhập Mã giảm giá**: Tìm ô `name="reductions"`, xóa dữ liệu cũ và áp dụng mã khuyến mại.
6. **Điền Thẻ tín dụng**:
   - Cuộn tới khu vực thanh toán và quét qua các khung Iframe bảo mật của Shopify (`shopifycs`).
   - Điền số thẻ, ngày hết hạn (tự động phân tích định dạng `MM / YY`), mã bảo mật CVV và Tên chủ thẻ.
7. **Xác nhận thanh toán (Pay Now)**:
   - Đợi hệ thống cập nhật tổng tiền sau giảm giá.
   - Bấm `Pay now` (tự động thử lại tối đa 6 lần retry, tổng cộng 7 lần thử nếu có lỗi hoặc cần nhập lại thẻ).

---

### Bước 5: Báo Cáo & Xuất File Kết Quả
- Sử dụng **Mutex (`sync.Mutex`)** để bảo vệ an toàn đa luồng khi ghi file:
  - `results_success.txt`: Lưu các đơn đã thanh toán thành công (kèm email, thẻ đã che số, proxy).
  - `results_failed.txt`: Lưu các trường hợp lỗi chi tiết để kiểm tra lại.
  - `proxies_failed.txt`: Lưu danh sách các proxy bị chết, hỏng, lỗi `ERR_CONNECTION_CLOSED`.

---

## 3. Cấu Trúc Các File Dữ Liệu

### 1. `proxies.txt`
Mỗi dòng 1 Proxy Socks5 riêng biệt, hỗ trợ các định dạng:
```text
global.rotip.trustsource.live:20000:USER501664-zone-custom-region-US-session-68042532:41b761
socks5://user:pass@1.2.3.4:1080
127.0.0.1:1080
```

### 2. `cards.txt`
Mỗi dòng 1 profile cách nhau bằng dấu gạch đứng `|`:
```text
Số thẻ|Tháng|Năm|CVV|Họ tên|Địa chỉ|Thành phố|Bang|Zip code|Quốc gia|Số điện thoại|Email
4160467122625048|07|29|082|Elizabeth Bass|7920 N 107Th #9|Milwaukee|WI|53224|US|4143243575|lizjay2415@gmail.com
```

### 3. `discounts.txt`
Mỗi dòng 1 mã giảm giá:
```text
IDME_ON_20_JOLDOZ
IDME_ON_20_PROMO2
```

### 4. `telegram.txt` (Cấu hình thông báo Bot Telegram)
```text
# Lấy BOT_TOKEN từ @BotFather và CHAT_ID từ @userinfobot hoặc @RawDataBot
BOT_TOKEN=1234567890:AAHxxxxxxxxx
CHAT_ID=987654321
```

---

## 4. Hướng Dẫn Chạy Chương Trình

> **Lưu ý quan trọng**: Dự án này viết bằng **Golang**, không dùng lệnh `npm`. Hãy sử dụng lệnh `go run` hoặc chạy trực tiếp file `.exe`.

### Cách 1: Chạy file thực thi `.exe` (Khuyên dùng)
```powershell
# Chạy trực tiếp (Tool sẽ quét RAM/CPU và hỏi bạn nhập số luồng)
.\tool-buy-multi.exe

# Chạy chỉ định sẵn 10 luồng
.\tool-buy-multi.exe -threads 10

# Chạy hiển thị màn hình trình duyệt (không ẩn)
.\tool-buy-multi.exe -threads 5 -headless=false
```

### Cách 2: Chạy trực tiếp qua Go
```powershell
go run main_multi.go
```

### Cách 3: Biên dịch lại file `.exe` sau khi chỉnh sửa
```powershell
go build -o tool-buy-multi.exe main_multi.go
```
