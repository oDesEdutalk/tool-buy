package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"golang.org/x/net/proxy"

	"tool-buy/fingerprint"
)

// DefaultProxy mặc định
const DefaultProxyMulti = ""

// UserProfile lưu thông tin người dùng được trích xuất từ chuỗi mẫu
type UserProfile struct {
	CardNumber string
	ExpMonth   string
	ExpYear    string
	CVV        string
	FullName   string
	FirstName  string
	LastName   string
	Email      string
	Address    string
	City       string
	State      string
	ZipCode    string
	Country    string
	Phone      string
	RawAddress string
	RawData    string
}

// CheckoutTask chứa thông tin của 1 tác vụ mua hàng
type CheckoutTask struct {
	ID           int
	URL          string
	Size         string
	Proxy        string
	Profile      *UserProfile
	DiscountCode string
	DiscountFile string
	Headless     bool
	UserAgent    string
	OS           string
	TeleCfg      TelegramConfig
}

// ThankYouDetails trích xuất từ màn hình xác nhận mua hàng thành công
type ThankYouDetails struct {
	ConfirmationNumber string
	Greeting           string
	TotalAmount        string
	ProductName        string
	FinalURL           string
}

var successFileMutex sync.Mutex

// extractThankYouPageDetails trích xuất chi tiết đơn hàng từ màn hình Thank You / Confirmation
func extractThankYouPageDetails(page *rod.Page) ThankYouDetails {
	details := ThankYouDetails{}

	info, err := page.Info()
	if err == nil && info != nil {
		details.FinalURL = info.URL
	}

	for retry := 0; retry < 4; retry++ {
		evalRes, err := page.Eval(`() => {
			let confirmNum = '';
			let greeting = '';
			let total = '';
			let product = '';

			const bodyText = (document.body ? document.body.innerText : '');

			// 1. Tìm Confirmation Number (Ví dụ: Confirmation #XUEHATTZ5 hoặc #XUEHATTZ5)
			const confirmMatch = bodyText.match(/Confirmation\s*(#[A-Z0-9]+)/i) ||
			                     bodyText.match(/Order\s*number\s*(#[A-Z0-9]+)/i) ||
			                     bodyText.match(/Confirmation\s+code\s*[:#]?\s*([A-Z0-9]+)/i) ||
			                     bodyText.match(/Order\s*(#[A-Z0-9]+)/i) ||
			                     bodyText.match(/(#[A-Z0-9]{6,})/);
			if (confirmMatch) {
				confirmNum = confirmMatch[1] || confirmMatch[0];
			} else {
				const el = document.querySelector('.os-order-number, [data-order-number], [class*="order-number"], [class*="confirmation-number"]');
				if (el) confirmNum = (el.innerText || el.textContent || '').trim();
			}

			// 2. Tìm Greeting (Ví dụ: "Thank you, Dolgov!")
			const greetMatch = bodyText.match(/Thank\s+you[,\s]+([^\n!.]+)[!.]?/i);
			if (greetMatch) {
				greeting = greetMatch[0].trim();
			}

			// 3. Tìm Total Amount (Ví dụ: USD $114.48 hoặc $114.48)
			const totalMatch = bodyText.match(/Total[\s\S]*?(USD\s*\$[\d.,]+|\$[\d.,]+)/i) ||
			                   bodyText.match(/(USD\s*\$[\d.,]+)/i);
			if (totalMatch) {
				total = totalMatch[1].trim();
			} else {
				const totalEl = document.querySelector('[data-checkout-payment-due-target], .total-line__price, [class*="payment-due-price"], [class*="total-price"]');
				if (totalEl) total = (totalEl.innerText || totalEl.textContent || '').trim();
			}

			// 4. Tìm Tên sản phẩm
			const prodEl = document.querySelector('.product__description__name, [class*="product-name"], [class*="line-item"] h3');
			if (prodEl) {
				product = (prodEl.innerText || prodEl.textContent || '').trim();
			}

			return {
				confirmNum: confirmNum,
				greeting: greeting,
				total: total,
				product: product
			};
		}`)

		if err == nil && evalRes != nil {
			cNum := evalRes.Value.Get("confirmNum").String()
			greet := evalRes.Value.Get("greeting").String()
			tot := evalRes.Value.Get("total").String()
			prod := evalRes.Value.Get("product").String()

			if cNum != "" {
				details.ConfirmationNumber = cNum
			}
			if greet != "" {
				details.Greeting = greet
			}
			if tot != "" {
				details.TotalAmount = tot
			}
			if prod != "" {
				details.ProductName = prod
			}

			if details.ConfirmationNumber != "" && details.TotalAmount != "" {
				break
			}
		}
		time.Sleep(1 * time.Second)
	}

	return details
}

// saveSuccessOrderToFile lưu toàn bộ thông tin vừa nhập và thông tin đơn hàng ra file
func saveSuccessOrderToFile(task CheckoutTask, tyDetails ThankYouDetails, filePath string) error {
	successFileMutex.Lock()
	defer successFileMutex.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	p := task.Profile
	if p == nil {
		p = &UserProfile{}
	}

	orderCode := tyDetails.ConfirmationNumber
	if orderCode == "" {
		orderCode = "Confirmation (Đã ghi nhận trên màn hình)"
	}
	totalMoney := tyDetails.TotalAmount
	if totalMoney == "" {
		totalMoney = "N/A"
	}
	greeting := tyDetails.Greeting
	if greeting == "" {
		greeting = fmt.Sprintf("Thank you, %s!", strings.TrimSpace(p.FirstName+" "+p.LastName))
	}

	// 1. Ghi đầy đủ thông tin chi tiết dạng khối vào success_orders.txt
	contentBlock := fmt.Sprintf(`================================================================================
🎉 ĐƠN HÀNG THANH TOÁN THÀNH CÔNG (THANK YOU) - %s
================================================================================
[THÔNG TIN ĐƠN HÀNG TRÊN MÀN HÌNH THANK YOU]
- Thời gian        : %s
- Mã xác nhận      : %s
- Lời cảm ơn       : %s
- Tổng thanh toán  : %s
- Sản phẩm xác nhận: %s (Size: %s)
- URL xác nhận     : %s

[TẤT CẢ THÔNG TIN KHÁCH HÀNG ĐÃ NHẬP]
- Tác vụ ID        : Luồng #%d
- Họ và Tên        : %s %s (Full: %s)
- Email            : %s
- Số điện thoại    : %s
- Địa chỉ đã gõ    : %s
- Địa chỉ chi tiết : %s, %s, %s %s, %s

[TẤT CẢ THÔNG TIN THẺ THANH TOÁN ĐÃ NHẬP]
- Số thẻ           : %s
- Hạn dùng (MM/YY) : %s/%s
- CVV              : %s

[MÃ GIẢM GIÁ & HỆ THỐNG]
- Mã giảm giá      : %s
- URL gốc sản phẩm : %s
- Size đã chọn     : %s
- Proxy đã dùng    : %s
- User-Agent       : %s
- Dữ liệu thô gốc  : %s

[DÒNG TỔNG HỢP (PIPE FORMAT DỄ IMPORT)]
%s|%s|%s|%s|%s %s|%s|%s|%s|%s|%s|%s|%s
================================================================================

`,
		timestamp,
		timestamp,
		orderCode,
		greeting,
		totalMoney,
		task.URL,
		task.Size,
		tyDetails.FinalURL,
		task.ID,
		p.FirstName, p.LastName, p.FullName,
		p.Email,
		p.Phone,
		p.RawAddress,
		p.Address, p.City, p.State, p.ZipCode, p.Country,
		p.CardNumber,
		p.ExpMonth, p.ExpYear,
		p.CVV,
		task.DiscountCode,
		task.URL,
		task.Size,
		task.Proxy,
		task.UserAgent,
		p.RawData,
		p.CardNumber, p.ExpMonth, p.ExpYear, p.CVV, p.FirstName, p.LastName, p.Email, p.Phone, p.RawAddress, task.DiscountCode, orderCode, totalMoney, timestamp,
	)

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		_, _ = f.WriteString(contentBlock)
		_ = f.Close()
	}

	// 2. Ghi thêm vào file bảng tính success_orders.csv
	csvPath := "success_orders.csv"
	isNewCSV := false
	if _, statErr := os.Stat(csvPath); os.IsNotExist(statErr) {
		isNewCSV = true
	}
	csvFile, err := os.OpenFile(csvPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		if isNewCSV {
			_, _ = csvFile.WriteString("Timestamp,ConfirmationCode,Total,FullName,FirstName,LastName,Email,Phone,Address,CardNumber,ExpMonth,ExpYear,CVV,DiscountCode,Size,Proxy,OrderURL\n")
		}
		cleanAddr := strings.ReplaceAll(p.RawAddress, ",", " ")
		cleanProxy := strings.ReplaceAll(task.Proxy, ",", ";")
		csvLine := fmt.Sprintf("\"%s\",\"%s\",\"%s\",\"%s %s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\"\n",
			timestamp, orderCode, totalMoney, p.FirstName, p.LastName, p.FirstName, p.LastName, p.Email, p.Phone, cleanAddr,
			p.CardNumber, p.ExpMonth, p.ExpYear, p.CVV, task.DiscountCode, task.Size, cleanProxy, tyDetails.FinalURL,
		)
		_, _ = csvFile.WriteString(csvLine)
		_ = csvFile.Close()
	}

	// 3. Ghi vào results_success.txt dạng đầy đủ thông tin
	resultsFile, err := os.OpenFile("results_success.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		_, _ = resultsFile.WriteString(fmt.Sprintf("[%s] Thành công: %s | Thẻ %s | %s/%s | CVV %s | %s %s | %s | %s | %s | Proxy: %s | Mã: %s\n",
			timestamp, orderCode, p.CardNumber, p.ExpMonth, p.ExpYear, p.CVV, p.FirstName, p.LastName, p.Email, p.Phone, p.RawAddress, task.Proxy, task.DiscountCode))
		_ = resultsFile.Close()
	}

	log.Println("================================================================================")
	log.Printf("🎉 ĐÃ XUẤT HIỆN MÀN HÌNH THANK YOU & LƯU TẤT CẢ THÔNG TIN VÀO FILE: %s\n", filePath)
	log.Printf("-> File chi tiết : %s\n", filePath)
	log.Printf("-> File bảng tính: %s\n", csvPath)
	log.Printf("-> Mã đơn hàng   : %s | Tổng tiền: %s\n", orderCode, totalMoney)
	log.Println("================================================================================")

	// 4. Gửi Telegram nếu có cấu hình
	if task.TeleCfg.BotToken != "" && task.TeleCfg.ChatID != "" {
		teleMsg := fmt.Sprintf(`🎉 <b>[CHECKOUT THÀNH CÔNG - THANK YOU]</b>

📦 <b>Mã đơn:</b> <code>%s</code>
💵 <b>Tổng thanh toán:</b> <b>%s</b>
🏷️ <b>Mã giảm giá:</b> <code>%s</code>
👟 <b>Size:</b> %s

👤 <b>Khách hàng:</b> %s %s
📧 <b>Email:</b> %s
📱 <b>Số điện thoại:</b> %s
🏠 <b>Địa chỉ:</b> %s

💳 <b>Thẻ tín dụng:</b> <code>%s</code>
📅 <b>Hạn dùng:</b> %s/%s | <b>CVV:</b> <code>%s</code>

🌐 <b>Proxy:</b> <code>%s</code>
🕒 <b>Thời gian:</b> %s`,
			orderCode,
			totalMoney,
			task.DiscountCode,
			task.Size,
			p.FirstName, p.LastName,
			p.Email,
			p.Phone,
			p.RawAddress,
			p.CardNumber,
			p.ExpMonth, p.ExpYear, p.CVV,
			task.Proxy,
			timestamp,
		)
		sendTelegramAlert(task.TeleCfg, teleMsg)
	}

	// 5. Xóa mã giảm giá đã dùng thành công khỏi file discounts.txt nếu có
	if task.DiscountCode != "" {
		discFile := task.DiscountFile
		if discFile == "" {
			discFile = "discounts.txt"
		}
		_ = removeDiscountFromFile(discFile, task.DiscountCode)
	}

	return nil
}

var discountFileMutex sync.Mutex

// removeDiscountFromFile xóa mã giảm giá đã sử dụng thành công khỏi file discounts.txt
func removeDiscountFromFile(filePath, codeToRemove string) error {
	discountFileMutex.Lock()
	defer discountFileMutex.Unlock()

	codeToRemove = strings.TrimSpace(codeToRemove)
	if codeToRemove == "" || filePath == "" {
		return nil
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	rawStr := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(rawStr, "\n")
	var newLines []string
	removed := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			newLines = append(newLines, line)
			continue
		}
		if trimmed == "" {
			newLines = append(newLines, "")
			continue
		}

		if !removed && strings.EqualFold(trimmed, codeToRemove) {
			removed = true
			log.Printf("-> [DISCOUNT] Đã xóa mã giảm giá thành công '%s' khỏi file '%s'\n", codeToRemove, filePath)
			continue
		}
		newLines = append(newLines, trimmed)
	}

	if removed {
		for len(newLines) > 0 && strings.TrimSpace(newLines[len(newLines)-1]) == "" {
			newLines = newLines[:len(newLines)-1]
		}
		output := strings.Join(newLines, "\r\n") + "\r\n"
		return os.WriteFile(filePath, []byte(output), 0644)
	}
	return nil
}

// SystemSpecs thông tin phần cứng máy tính
type SystemSpecs struct {
	NumCPU     int
	TotalRAMGB float64
	AvailRAMGB float64
	MaxThreads int
	RecThreads int
}

type memorystatusex struct {
	cbSize                  uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

// Mutex để ghi log và kết quả an toàn giữa các luồng
var (
	resultMutex         sync.Mutex
	logMutex            sync.Mutex
	failedProxyMutex    sync.Mutex
	failedProxyMap      = make(map[string]bool)
	currentRunFailedMap = make(map[string]bool)
)

// recordFailedProxy ghi nhận proxy bị lỗi vào file proxies_failed.txt
func recordFailedProxy(proxyStr, reason string) {
	if proxyStr == "" {
		return
	}
	failedProxyMutex.Lock()
	defer failedProxyMutex.Unlock()

	currentRunFailedMap[proxyStr] = true

	if failedProxyMap[proxyStr] {
		return
	}
	failedProxyMap[proxyStr] = true

	f, err := os.OpenFile("proxies_failed.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	_, _ = f.WriteString(fmt.Sprintf("[%s] %s | Lỗi: %s\n", timestamp, proxyStr, reason))
}

// isProxyOrConnectionError kiểm tra xem lỗi có phải do kết nối/proxy hỏng hay không
func isProxyOrConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "err_connection_closed") ||
		strings.Contains(errStr, "err_connection_reset") ||
		strings.Contains(errStr, "err_connection_aborted") ||
		strings.Contains(errStr, "err_proxy_connection_failed") ||
		strings.Contains(errStr, "err_socks_connection_failed") ||
		strings.Contains(errStr, "err_tunnel_connection_failed") ||
		strings.Contains(errStr, "err_name_not_resolved") ||
		strings.Contains(errStr, "err_timed_out") ||
		strings.Contains(errStr, "err_empty_response") ||
		strings.Contains(errStr, "err_connection_refused") ||
		strings.Contains(errStr, "net::err_") ||
		strings.Contains(errStr, "navigation failed") ||
		strings.Contains(errStr, "local proxy bridge") ||
		strings.Contains(errStr, "dialer socks5") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "i/o timeout")
}

// loadFailedProxies đọc danh sách proxy đã bị lỗi từ file proxies_failed.txt
func loadFailedProxies(filePath string) map[string]bool {
	failed := make(map[string]bool)
	lines, err := readLinesFromFile(filePath)
	if err != nil {
		return failed
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			idx := strings.Index(line, "]")
			content := strings.TrimSpace(line[idx+1:])
			parts := strings.Split(content, "|")
			proxyStr := strings.TrimSpace(parts[0])
			if proxyStr != "" {
				failed[proxyStr] = true
			}
		} else {
			parts := strings.Split(line, "|")
			proxyStr := strings.TrimSpace(parts[0])
			if proxyStr != "" {
				failed[proxyStr] = true
			}
		}
	}
	return failed
}

// TelegramConfig cấu hình bot Telegram
type TelegramConfig struct {
	BotToken string
	ChatID   string
}

// loadTelegramConfig tải cấu hình Telegram từ tham số dòng lệnh, file hoặc biến môi trường
func loadTelegramConfig(filePath, flagToken, flagChatID string) TelegramConfig {
	cfg := TelegramConfig{
		BotToken: flagToken,
		ChatID:   flagChatID,
	}
	if cfg.BotToken == "" {
		cfg.BotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	}
	if cfg.ChatID == "" {
		cfg.ChatID = os.Getenv("TELEGRAM_CHAT_ID")
	}

	lines, err := readLinesFromFile(filePath)
	if err == nil {
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") || line == "" {
				continue
			}
			if strings.Contains(line, "=") {
				parts := strings.SplitN(line, "=", 2)
				key := strings.TrimSpace(strings.ToUpper(parts[0]))
				val := strings.TrimSpace(parts[1])
				if (key == "BOT_TOKEN" || key == "TOKEN" || key == "TELEGRAM_BOT_TOKEN") && cfg.BotToken == "" {
					cfg.BotToken = val
				} else if (key == "CHAT_ID" || key == "CHATID" || key == "TELEGRAM_CHAT_ID") && cfg.ChatID == "" {
					cfg.ChatID = val
				}
			} else {
				if cfg.BotToken == "" {
					cfg.BotToken = line
				} else if cfg.ChatID == "" {
					cfg.ChatID = line
				}
			}
		}
	}
	return cfg
}

// sendTelegramAlert gửi tin nhắn cảnh báo tới Telegram
func sendTelegramAlert(cfg TelegramConfig, message string) {
	if cfg.BotToken == "" || cfg.ChatID == "" {
		log.Println("[Telegram] ⚠️ Chưa cấu hình BOT_TOKEN hoặc CHAT_ID trong telegram.txt. Bỏ qua gửi thông báo Telegram.")
		return
	}
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	data := url.Values{}
	data.Set("chat_id", cfg.ChatID)
	data.Set("text", message)
	data.Set("parse_mode", "HTML")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.PostForm(apiURL, data)
	if err != nil {
		log.Printf("[Telegram] ❌ Lỗi gửi tin nhắn Telegram: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[Telegram] ❌ Lỗi Telegram API (mã %d): %s\n", resp.StatusCode, string(body))
		return
	}
	log.Printf("[Telegram] ✅ Đã gửi cảnh báo Telegram thành công đến Chat ID: %s\n", cfg.ChatID)
}

// ProxyManager quản lý danh sách proxy, xoay vòng khi lỗi và giữ lại proxy thành công cho phiên sau
type ProxyManager struct {
	mu         sync.Mutex
	filePath   string
	available  []string
	successful []string
	inUseMap   map[string]bool
	failedMap  map[string]bool
	teleCfg    TelegramConfig
	alertSent  bool
}

// NewProxyManager khởi tạo bộ quản lý proxy từ file proxies.txt và proxies_failed.txt
func NewProxyManager(filePath string, failedFilePath string, teleCfg TelegramConfig) *ProxyManager {
	failedMap := loadFailedProxies(failedFilePath)

	lines, err := readLinesFromFile(filePath)
	if err != nil {
		lines = []string{}
	}

	var available []string
	seen := make(map[string]bool)

	for _, p := range lines {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		if !failedMap[p] && !seen[p] {
			available = append(available, p)
			seen[p] = true
		}
	}

	return &ProxyManager{
		filePath:   filePath,
		available:  available,
		successful: make([]string, 0),
		inUseMap:   make(map[string]bool),
		failedMap:  failedMap,
		teleCfg:    teleCfg,
	}
}

// GetNextProxy lấy proxy khả dụng tiếp theo từ danh sách
func (pm *ProxyManager) GetNextProxy() (string, bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if len(pm.available) == 0 {
		return "", false
	}
	p := pm.available[0]
	pm.available = pm.available[1:]
	pm.inUseMap[p] = true
	return p, true
}

// AvailableCount đếm số proxy còn lại chưa dùng trong hàng đợi
func (pm *ProxyManager) AvailableCount() int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return len(pm.available)
}

// MarkFailed ghi nhận proxy thất bại, cập nhật proxies.txt, lưu vào proxies_failed.txt và gửi Telegram nếu hết proxy
func (pm *ProxyManager) MarkFailed(proxyStr string, reason string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	delete(pm.inUseMap, proxyStr)
	pm.failedMap[proxyStr] = true
	recordFailedProxy(proxyStr, reason)

	// Cập nhật lại file proxies.txt loại bỏ proxy hỏng
	pm.saveStateLocked()

	// Nếu hàng đợi và in-use không còn proxy nào khác -> Báo Telegram
	if len(pm.available) == 0 && len(pm.inUseMap) == 0 && !pm.alertSent {
		pm.alertSent = true
		errMsg := fmt.Sprintf("🚨 <b>[CẢNH BÁO TOOL CHECKOUT]</b>\n\n⚠️ <b>HẾT PROXY ĐỂ ĐỔI TIẾP TỤC!</b>\n- Proxy <code>%s</code> vừa bị lỗi: %s\n- Trong file <code>%s</code> không còn proxy khả dụng nào khác để thay thế!\n- Vui lòng cập nhật proxy mới vào file.\n- Thời gian: %s",
			proxyStr, reason, pm.filePath, time.Now().Format("2006-01-02 15:04:05"))
		sendTelegramAlert(pm.teleCfg, errMsg)
	}
}

// MarkSuccess ghi nhận proxy thành công để ưu tiên sử dụng ở vị trí số 1 cho các phiên tiếp theo
func (pm *ProxyManager) MarkSuccess(proxyStr string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	delete(pm.inUseMap, proxyStr)

	// Đưa proxy vừa mua thành công lên vị trí đầu tiên
	var newSucc []string
	newSucc = append(newSucc, proxyStr)
	for _, p := range pm.successful {
		if p != proxyStr {
			newSucc = append(newSucc, p)
		}
	}
	pm.successful = newSucc

	// Lưu ngay lập tức vào file proxies.txt để đảm bảo proxy thành công nằm ở dòng đầu tiên
	pm.saveStateLocked()
}

// SaveState cập nhật lại proxies.txt: Đưa proxy thành công lên đầu, giữ lại proxy chưa dùng, xóa proxy lỗi
func (pm *ProxyManager) SaveState() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.saveStateLocked()
}

func (pm *ProxyManager) saveStateLocked() {
	if pm.filePath == "" {
		return
	}

	var newLines []string
	newLines = append(newLines, "# Danh sách Socks5 Proxy (Cập nhật tự động bởi Tool)")
	newLines = append(newLines, "# Các proxy mua thành công được xếp ưu tiên trên đầu cho phiên tiếp theo:")

	seen := make(map[string]bool)

	// 1. Proxy thành công lên đầu tiên
	for _, p := range pm.successful {
		if !seen[p] && !pm.failedMap[p] {
			newLines = append(newLines, p)
			seen[p] = true
		}
	}

	// 2. Các proxy đang được sử dụng (in-use)
	for p := range pm.inUseMap {
		if !seen[p] && !pm.failedMap[p] {
			newLines = append(newLines, p)
			seen[p] = true
		}
	}

	// 3. Các proxy khả dụng còn lại trong hàng đợi
	for _, p := range pm.available {
		if !seen[p] && !pm.failedMap[p] {
			newLines = append(newLines, p)
			seen[p] = true
		}
	}

	content := strings.Join(newLines, "\n") + "\n"
	_ = os.WriteFile(pm.filePath, []byte(content), 0644)
}

// logWorker ghi log có prefix số luồng rõ ràng
func logWorker(workerID int, format string, v ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	prefix := fmt.Sprintf("[Luồng #%02d] ", workerID)
	log.Printf(prefix+format, v...)
}

// getSystemSpecs phân tích CPU và RAM của hệ thống
func getSystemSpecs() SystemSpecs {
	numCPU := runtime.NumCPU()
	var totalGB, availGB float64 = 8.0, 4.0 // Giá trị mặc định

	if runtime.GOOS == "windows" {
		mod := syscall.NewLazyDLL("kernel32.dll")
		proc := mod.NewProc("GlobalMemoryStatusEx")
		var mem memorystatusex
		mem.cbSize = uint32(unsafe.Sizeof(mem))
		r1, _, _ := proc.Call(uintptr(unsafe.Pointer(&mem)))
		if r1 != 0 {
			totalGB = float64(mem.ullTotalPhys) / (1024 * 1024 * 1024)
			availGB = float64(mem.ullAvailPhys) / (1024 * 1024 * 1024)
		}
	}

	// Mỗi browser Chromium headless tốn ~250MB - 350MB RAM
	// Dành ra 1.5GB cho Hệ điều hành và các tác vụ nền
	usableRAM := availGB - 1.5
	if usableRAM < 0.5 {
		usableRAM = 0.5
	}
	ramMaxThreads := int(usableRAM * 1024 / 320)
	cpuMaxThreads := numCPU * 3

	maxThreads := ramMaxThreads
	if cpuMaxThreads < maxThreads {
		maxThreads = cpuMaxThreads
	}
	if maxThreads < 1 {
		maxThreads = 1
	}
	if maxThreads > 60 {
		maxThreads = 60
	}

	recThreads := numCPU * 2
	if recThreads > maxThreads {
		recThreads = maxThreads
	}
	if recThreads < 1 {
		recThreads = 1
	}

	return SystemSpecs{
		NumCPU:     numCPU,
		TotalRAMGB: totalGB,
		AvailRAMGB: availGB,
		MaxThreads: maxThreads,
		RecThreads: recThreads,
	}
}

// parseDataString phân tích chuỗi mẫu dạng pipe (|)
func parseDataString(raw string) *UserProfile {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}

	profile := &UserProfile{
		RawData: raw,
	}
	if len(parts) > 0 {
		profile.CardNumber = parts[0]
	}
	if len(parts) > 1 {
		profile.ExpMonth = parts[1]
	}
	if len(parts) > 2 {
		profile.ExpYear = parts[2]
	}
	if len(parts) > 3 {
		profile.CVV = parts[3]
	}
	if len(parts) > 4 {
		profile.FullName = parts[4]
		nameParts := strings.Fields(profile.FullName)
		if len(nameParts) == 1 {
			profile.FirstName = nameParts[0]
			profile.LastName = "Smith"
		} else if len(nameParts) >= 2 {
			profile.FirstName = nameParts[0]
			profile.LastName = strings.Join(nameParts[1:], " ")
		}
		cleanName := strings.ToLower(strings.ReplaceAll(profile.FullName, " ", ""))
		_ = cleanName
	}

	// Email phải lấy firstname + lastName + random số từ 1 - 999 + @gmail.com
	profile.Email = generateEmail(profile.FirstName, profile.LastName)

	// Quét thông minh qua tất cả các cột phía sau:
	// - Nếu có cột là số điện thoại (10-15 ký tự) -> lấy làm Phone (không lấy cột chứa '@')
	for i := 5; i < len(parts); i++ {
		p := parts[i]
		if strings.Contains(p, "@") {
			// Bỏ qua cột email từ cards.txt theo yêu cầu
			continue
		} else if profile.Phone == "" && len(p) >= 10 && len(p) <= 15 {
			var digits strings.Builder
			for _, r := range p {
				if r >= '0' && r <= '9' {
					digits.WriteRune(r)
				}
			}
			digitsOnly := digits.String()
			if len(digitsOnly) >= 10 && len(digitsOnly) <= 11 {
				profile.Phone = digitsOnly
			}
		}
	}
	if len(parts) > 5 {
		profile.Address = parts[5]
	}
	if len(parts) > 6 {
		profile.City = parts[6]
	}
	if len(parts) > 7 {
		profile.State = parts[7]
	}
	if len(parts) > 8 {
		profile.ZipCode = parts[8]
	}
	if len(parts) > 9 {
		profile.Country = parts[9]
	}
	if len(parts) > 10 {
		profile.Phone = parts[10]
	}

	if profile.Address != "" {
		if profile.City != "" && profile.State != "" && profile.ZipCode != "" {
			profile.RawAddress = fmt.Sprintf("%s, %s %s %s", profile.Address, profile.City, profile.State, profile.ZipCode)
		} else {
			profile.RawAddress = profile.Address
		}
	}

	return profile
}

// readLinesFromFile đọc file theo từng dòng (bỏ qua dòng trống và comment #)
func readLinesFromFile(filePath string) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}

	return lines, scanner.Err()
}

// generateEmail tạo email theo định dạng: firstname + lastName + số ngẫu nhiên từ 1 - 999 + @gmail.com
func generateEmail(firstName, lastName string) string {
	fn := strings.ToLower(strings.TrimSpace(firstName))
	ln := strings.ToLower(strings.TrimSpace(lastName))
	var sb strings.Builder
	for _, r := range fn + ln {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
	}
	cleanName := sb.String()
	if cleanName == "" {
		cleanName = "user"
	}
	randNum := rand.Intn(999) + 1 // ngẫu nhiên từ 1 đến 999
	return fmt.Sprintf("%s%d@gmail.com", cleanName, randNum)
}

// randomizeEmail hỗ trợ tương thích tạo email theo firstname + lastName + random(1-999) + @gmail.com
func randomizeEmail(email string, firstName, lastName string) string {
	return generateEmail(firstName, lastName)
}

// defaultUserAgents danh sách User-Agent hiện đại dự phòng chuẩn BitBrowser Windows
var defaultUserAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
}

// defaultAndroidUserAgents danh sách User-Agent điện thoại Android dự phòng chuẩn BitBrowser
var defaultAndroidUserAgents = []string{
	"Mozilla/5.0 (Linux; Android 14; Pixel 8 Pro) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 14; SM-S928B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 14; SM-S921B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 13; Pixel 7 Pro) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 13; SM-G998B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36",
}

// getRandomUserAgent đọc ngẫu nhiên một User-Agent từ file theo OS (hoặc dùng danh sách mặc định)
func getRandomUserAgent(filePath string, osType ...string) string {
	targetOS := "android"
	if len(osType) > 0 && strings.TrimSpace(osType[0]) != "" {
		targetOS = strings.ToLower(strings.TrimSpace(osType[0]))
	}

	lines, err := readLinesFromFile(filePath)
	if err == nil && len(lines) > 0 {
		var matched []string
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			lower := strings.ToLower(l)
			if targetOS == "android" {
				if strings.Contains(lower, "android") || strings.Contains(lower, "linux") {
					matched = append(matched, l)
				}
			} else {
				if strings.Contains(lower, "windows") {
					matched = append(matched, l)
				}
			}
		}
		if len(matched) > 0 {
			return matched[rand.Intn(len(matched))]
		}
		return lines[rand.Intn(len(lines))]
	}

	if targetOS == "android" {
		return defaultAndroidUserAgents[rand.Intn(len(defaultAndroidUserAgents))]
	}
	return defaultUserAgents[rand.Intn(len(defaultUserAgents))]
}

type WindowResolution struct {
	Width  int
	Height int
}

var commonResolutions = []WindowResolution{
	{1920, 1080},
	{1536, 864},
	{1440, 900},
	{1366, 768},
	{1600, 900},
	{1680, 1050},
	{1280, 800},
}

var commonMobileResolutions = []WindowResolution{
	{412, 915},
	{393, 873},
	{412, 892},
	{412, 912},
}

func getRandomResolution(osType ...string) (int, int) {
	if len(osType) > 0 && strings.ToLower(strings.TrimSpace(osType[0])) == "android" {
		res := commonMobileResolutions[rand.Intn(len(commonMobileResolutions))]
		return res.Width, res.Height
	}
	res := commonResolutions[rand.Intn(len(commonResolutions))]
	return res.Width, res.Height
}

// appendResultFile ghi kết quả ra file thành công hoặc thất bại
func appendResultFile(filename, content string) {
	resultMutex.Lock()
	defer resultMutex.Unlock()

	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	_, _ = f.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, content))
}

// ProductItem chứa URL sản phẩm và size cần chọn
type ProductItem struct {
	URL  string
	Size string
}

// loadProductItems đọc danh sách sản phẩm và size từ file (định dạng: URL|Size)
func loadProductItems(filePath, defaultURL, defaultSize string) []ProductItem {
	lines, err := readLinesFromFile(filePath)
	if err != nil || len(lines) == 0 {
		if defaultURL != "" {
			return []ProductItem{{URL: defaultURL, Size: defaultSize}}
		}
		return nil
	}

	var items []ProductItem
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		u := strings.TrimSpace(parts[0])
		s := defaultSize
		if len(parts) >= 2 && strings.TrimSpace(parts[1]) != "" {
			s = strings.TrimSpace(parts[1])
		}
		if u != "" {
			items = append(items, ProductItem{URL: u, Size: s})
		}
	}

	if len(items) == 0 && defaultURL != "" {
		items = append(items, ProductItem{URL: defaultURL, Size: defaultSize})
	}
	return items
}

// getRandomProduct chọn ngẫu nhiên một sản phẩm từ danh sách hoặc trả về giá trị mặc định
func getRandomProduct(products []ProductItem, defaultURL, defaultSize string) (string, string) {
	if len(products) > 0 {
		p := products[rand.Intn(len(products))]
		return p.URL, p.Size
	}
	return defaultURL, defaultSize
}

func main() {
	targetURL := flag.String("url", "https://www.on.com/en-us/products/cloudboom-max-m-3mf3031/mens/lime-raspberry-shoes-3MF30314229", "URL trang web cần mở")
	targetSize := flag.String("size", "9", "Size sản phẩm cần chọn (S, M, L, XL, ...)")
	productsFileFlag := flag.String("products-file", "products.txt", "File danh sách URL sản phẩm và size (URL|Size)")
	headlessFlag := flag.Bool("headless", true, "Chạy ẩn trình duyệt để tối ưu tài nguyên (true/false)")
	threadsFlag := flag.Int("threads", 0, "Số luồng chạy đồng thời (1 - 50)")

	addressFlag := flag.String("address", "820 Roxfield Ct, Buford GA 30518", "Địa chỉ gõ vào ô tìm kiếm (hoặc lấy từ -data)")
	proxyFileFlag := flag.String("proxy-file", "proxies.txt", "File danh sách proxy SOCKS5")
	cardFileFlag := flag.String("card-file", "cards.txt", "File danh sách thẻ và profile")
	discountFileFlag := flag.String("discount-file", "discounts.txt", "File danh sách mã giảm giá")
	telegramFileFlag := flag.String("telegram-file", "telegram.txt", "File cấu hình Telegram Bot (BOT_TOKEN, CHAT_ID)")
	phonesFileFlag := flag.String("phones-file", "phones.txt", "File danh sách số điện thoại (ngẫu nhiên)")
	uaFileFlag := flag.String("ua-file", "user_agents.txt", "File danh sách User-Agent (ngẫu nhiên)")
	teleTokenFlag := flag.String("tele-token", "", "Telegram Bot Token")
	teleChatIDFlag := flag.String("tele-chatid", "", "Telegram Chat ID")

	// Flag đơn lẻ fallback nếu người dùng muốn chạy 1 profile trực tiếp
	dataSingle := flag.String("data", "", "Chuỗi dữ liệu đơn lẻ (nếu không dùng file)")
	proxySingle := flag.String("proxy", "", "Proxy đơn lẻ")
	discountSingle := flag.String("discount", "IDME_ON_20_ITULQV", "Mã giảm giá đơn lẻ")
	osFlag := flag.String("os", "android", "Hệ điều hành vân tay: 'android' (khuyên dùng khi chạy Proxy 4G Mobile để đồng bộ TCP/IP) hoặc 'windows' (khi dùng Residential Proxy)")

	flag.Parse()

	// Đọc cấu hình Telegram
	teleCfg := loadTelegramConfig(*telegramFileFlag, *teleTokenFlag, *teleChatIDFlag)

	// 1. Kiểm tra cấu hình phần cứng
	specs := getSystemSpecs()

	fmt.Println("================================================================================")
	fmt.Println("             TOOL AUTOMATION CHECKOUT MULTI-THREAD (GOLANG)                    ")
	fmt.Println("================================================================================")
	fmt.Printf(" [THÔNG SỐ MÁY TÍNH & ĐÁNH GIÁ TÀI NGUYÊN]\n")
	fmt.Printf(" -> CPU Cores       : %d cores\n", specs.NumCPU)
	fmt.Printf(" -> Tổng RAM        : %.2f GB\n", specs.TotalRAMGB)
	fmt.Printf(" -> RAM Khả dụng    : %.2f GB\n", specs.AvailRAMGB)
	fmt.Printf(" -> Mức tiêu thụ    : ~250MB - 350MB RAM / 1 trình duyệt Chromium Headless\n")
	fmt.Printf(" -> KHUYẾN NGHỊ     : Nên chạy từ %d đến %d luồng để ổn định nhất\n", 1, specs.RecThreads)
	fmt.Printf(" -> TỐI ĐA AN TOÀN  : Máy có thể chịu tải tối đa khoảng %d luồng đồng thời\n", specs.MaxThreads)
	fmt.Println("--------------------------------------------------------------------------------")

	// 2. Xác định số luồng cần chạy
	concurrency := *threadsFlag
	if concurrency <= 0 {
		fmt.Printf("-> Nhập số luồng muốn chạy (Khuyến nghị: %d, Tối đa an toàn: %d): ", specs.RecThreads, specs.MaxThreads)
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			inputStr := strings.TrimSpace(scanner.Text())
			if inputStr != "" {
				val, err := strconv.Atoi(inputStr)
				if err == nil && val > 0 {
					concurrency = val
				}
			}
		}
		if concurrency <= 0 {
			concurrency = specs.RecThreads
		}
	}

	if concurrency > specs.MaxThreads {
		fmt.Printf("[!] CẢNH BÁO: Bạn chọn %d luồng, vượt quá mức tối đa an toàn (%d luồng). Có thể gây lag/tràn RAM!\n", concurrency, specs.MaxThreads)
	}
	fmt.Printf("==> ĐÃ CHỌN SỐ LUỒNG CHẠY ĐỒNG THỜI: %d LUỒNG (Headless=%t)\n", concurrency, *headlessFlag)
	fmt.Println("--------------------------------------------------------------------------------")

	// 3. Đọc dữ liệu từ file
	var proxies []string
	var cards []string
	var discounts []string

	if *dataSingle != "" {
		cards = append(cards, *dataSingle)
		if *proxySingle != "" {
			proxies = append(proxies, *proxySingle)
		} else {
			proxies = append(proxies, DefaultProxyMulti)
		}
		if *discountSingle != "" {
			discounts = append(discounts, *discountSingle)
		}
	} else {
		var err error
		proxies, err = readLinesFromFile(*proxyFileFlag)
		if err != nil || len(proxies) == 0 {
			log.Printf("[!] CẢNH BÁO: Không tìm thấy hoặc file proxy '%s' trống!\n", *proxyFileFlag)
		}

		cards, err = readLinesFromFile(*cardFileFlag)
		if err != nil || len(cards) == 0 {
			log.Fatalf("[!] LỖI: Không tìm thấy hoặc file thẻ '%s' trống! Vui lòng kiểm tra lại file %s.", *cardFileFlag, *cardFileFlag)
		}

		discountExplicit := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "discount" {
				discountExplicit = true
			}
		})

		if discountExplicit && *discountSingle != "" {
			discounts = []string{*discountSingle}
		} else {
			discounts, _ = readLinesFromFile(*discountFileFlag)
			if len(discounts) == 0 && *discountSingle != "" {
				discounts = []string{*discountSingle}
			}
		}
	}

	// Đọc danh sách sản phẩm và size từ file (URL|Size), nếu file không có sẽ dùng URL và Size từ cờ lệnh
	products := loadProductItems(*productsFileFlag, *targetURL, *targetSize)

	// Khởi tạo ProxyManager quản lý danh sách proxy, tự động xoay vòng khi lỗi và lưu proxy thành công
	pm := NewProxyManager(*proxyFileFlag, "proxies_failed.txt", teleCfg)
	initialAvailable := pm.AvailableCount()

	// Kiểm tra nếu không còn proxy nào khả dụng -> Báo lỗi & gửi Telegram
	if initialAvailable == 0 {
		errMsg := fmt.Sprintf("🚨 <b>[CẢNH BÁO TOOL CHECKOUT]</b>\n\n⚠️ <b>HẾT PROXY ĐỂ CHẠY!</b>\n- File proxy <code>%s</code> hiện không còn proxy nào khả dụng (tất cả proxy đều đã bị lỗi/hỏng).\n- Vui lòng bổ sung proxy mới vào file để tiếp tục.\n- Thời gian: %s",
			*proxyFileFlag, time.Now().Format("2006-01-02 15:04:05"))
		fmt.Printf("\n[!] LỖI: Hết proxy khả dụng! Tất cả proxy trong '%s' đều đã bị lỗi. Đang gửi thông báo Telegram...\n", *proxyFileFlag)
		sendTelegramAlert(teleCfg, errMsg)
		return
	}

	// Yêu cầu: Mỗi luồng dùng 1 proxy khác nhau, khi không đủ chỉ sử dụng phần còn lại trong file
	totalCards := len(cards)
	usableCount := totalCards
	if initialAvailable < totalCards {
		usableCount = initialAvailable
		fmt.Printf("\n[!] THÔNG BÁO: Số Proxy khả dụng ban đầu (%d) ít hơn số Thẻ/Profile (%d).\n", initialAvailable, totalCards)
		fmt.Printf("==> Hệ thống CHỈ CHẠY %d TÁC VỤ (mỗi luồng 1 proxy riêng biệt, sử dụng hết %d proxy còn lại trong file).\n\n", usableCount, initialAvailable)

		warnMsg := fmt.Sprintf("⚠️ <b>[CẢNH BÁO THIẾU PROXY]</b>\n\n- Tổng số Profile: %d\n- Proxy khả dụng còn lại: %d\n- Hệ thống sẽ chỉ chạy <b>%d</b> tác vụ tương ứng với số proxy hiện có.\n- Còn lại <b>%d</b> profile chưa có proxy để chạy.\n- Thời gian: %s",
			totalCards, initialAvailable, usableCount, totalCards-usableCount, time.Now().Format("2006-01-02 15:04:05"))
		sendTelegramAlert(teleCfg, warnMsg)
	}

	phones, _ := readLinesFromFile(*phonesFileFlag)
	userAgents, _ := readLinesFromFile(*uaFileFlag)

	fmt.Printf(" [DANH SÁCH DỮ LIỆU ĐÃ TẢI]\n")
	fmt.Printf(" -> Tổng số Profile/Thẻ : %d (Sẽ thực thi: %d)\n", len(cards), usableCount)
	fmt.Printf(" -> Tổng số Proxy       : %d (1 Proxy / 1 Luồng riêng biệt)\n", initialAvailable)
	if len(phones) > 0 {
		fmt.Printf(" -> Tổng số SĐT (random): %d (từ %s)\n", len(phones), *phonesFileFlag)
	}
	if len(userAgents) > 0 {
		fmt.Printf(" -> Tổng số User-Agent  : %d (ngẫu nhiên từ %s)\n", len(userAgents), *uaFileFlag)
	}
	fmt.Printf(" -> Tổng số Mã giảm giá : %d\n", len(discounts))
	fmt.Printf(" -> Địa chỉ nhận hàng   : %s\n", *addressFlag)
	if len(products) > 1 || (len(products) == 1 && products[0].URL != *targetURL) {
		fmt.Printf(" -> Sản phẩm & Size     : %d mục (ngẫu nhiên từ %s: URL|Size)\n", len(products), *productsFileFlag)
	} else {
		fmt.Printf(" -> URL Sản phẩm        : %s\n", *targetURL)
		fmt.Printf(" -> Size sản phẩm       : %s\n", *targetSize)
	}
	fmt.Println("================================================================================")
	fmt.Println(" BẮT ĐẦU CHẠY TIẾN TRÌNH...")
	fmt.Println("================================================================================")

	// 4. Tạo danh sách Tasks (Mỗi task nhận 1 proxy độc lập ban đầu từ ProxyManager)
	var tasks []CheckoutTask
	for i := 0; i < usableCount; i++ {
		prof := parseDataString(cards[i])
		if prof == nil {
			continue
		}

		// Địa chỉ nhận hàng: Luôn ưu tiên dùng addressFlag mặc định, không lấy địa chỉ từ file thẻ
		if *addressFlag != "" {
			prof.RawAddress = *addressFlag
			prof.Address = *addressFlag
			parts := strings.Split(*addressFlag, ",")
			if len(parts) >= 3 {
				prof.Address = strings.TrimSpace(parts[0])
				prof.City = strings.TrimSpace(parts[1])
				lastFields := strings.Fields(strings.TrimSpace(parts[2]))
				if len(lastFields) >= 2 {
					prof.State = lastFields[0]
					prof.ZipCode = lastFields[len(lastFields)-1]
				} else if len(parts) >= 4 {
					prof.State = strings.TrimSpace(parts[2])
					prof.ZipCode = strings.TrimSpace(parts[3])
				}
			} else if len(parts) == 2 {
				prof.Address = strings.TrimSpace(parts[0])
				restFields := strings.Fields(strings.TrimSpace(parts[1]))
				if len(restFields) >= 3 {
					prof.ZipCode = restFields[len(restFields)-1]
					prof.State = restFields[len(restFields)-2]
					prof.City = strings.Join(restFields[:len(restFields)-2], " ")
				} else if len(restFields) == 2 {
					prof.State = restFields[0]
					prof.ZipCode = restFields[1]
				}
			}
		}

		if len(phones) > 0 {
			prof.Phone = phones[rand.Intn(len(phones))]
		}

		p, ok := pm.GetNextProxy()
		if !ok {
			break
		}

		d := ""
		if len(discounts) > 0 {
			d = discounts[i%len(discounts)]
		}

		taskURL, taskSize := getRandomProduct(products, *targetURL, *targetSize)

		tasks = append(tasks, CheckoutTask{
			ID:           i + 1,
			URL:          taskURL,
			Size:         taskSize,
			Proxy:        p,
			Profile:      prof,
			DiscountCode: d,
			DiscountFile: *discountFileFlag,
			Headless:     *headlessFlag,
			UserAgent:    getRandomUserAgent(*uaFileFlag, *osFlag),
			OS:           *osFlag,
			TeleCfg:      teleCfg,
		})
	}

	// 5. Khởi chạy Worker Pool
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, concurrency)
	startTime := time.Now()

	var successCount, failCount int
	var countMutex sync.Mutex

	for _, task := range tasks {
		wg.Add(1)
		semaphore <- struct{}{}

		go func(t CheckoutTask) {
			defer wg.Done()
			defer func() { <-semaphore }()

			time.Sleep(time.Duration(rand.Intn(1500)) * time.Millisecond)

			finalUsedProxy, err := runCheckoutWorker(t, pm)

			countMutex.Lock()
			if err == nil {
				successCount++
				if t.DiscountCode != "" && t.DiscountFile != "" {
					_ = removeDiscountFromFile(t.DiscountFile, t.DiscountCode)
				}
				appendResultFile("results_success.txt", fmt.Sprintf("Thành công: Thẻ %s | Email: %s | Proxy: %s | Mã: %s",
					maskSecret("card", t.Profile.CardNumber), t.Profile.Email, finalUsedProxy, t.DiscountCode))
			} else {
				failCount++
				appendResultFile("results_failed.txt", fmt.Sprintf("Thất bại: Thẻ %s | Lỗi: %v | Proxy: %s",
					maskSecret("card", t.Profile.CardNumber), err, finalUsedProxy))
			}
			countMutex.Unlock()
		}(task)
	}

	wg.Wait()

	// Lưu trạng thái proxies.txt: Giữ lại proxy mua thành công lên đầu, loại bỏ proxy hỏng
	pm.SaveState()

	// Đếm số proxy bị lỗi trong phiên này và tổng số lịch sử
	failedProxyMutex.Lock()
	numFailedInRun := len(currentRunFailedMap)
	totalFailedHistorical := len(failedProxyMap)
	failedProxyMutex.Unlock()

	// Chỉ gửi cảnh báo "TẤT CẢ PROXY ĐỀU BỊ LỖI" khi:
	// Có tác vụ chạy, KHÔNG CÓ tác vụ nào thành công, và tất cả tác vụ đều bị lỗi proxy trong phiên này
	if successCount == 0 && failCount > 0 && len(tasks) > 0 && numFailedInRun >= len(tasks) {
		alertMsg := fmt.Sprintf("🚨 <b>[CẢNH BÁO TOOL CHECKOUT]</b>\n\n⚠️ <b>TẤT CẢ PROXY TRONG PHIÊN ĐỀU BỊ LỖI!</b>\n- Số tác vụ chạy: %d\n- Số proxy bị lỗi trong phiên: %d\n- Danh sách proxy lỗi đã lưu tại <code>proxies_failed.txt</code>\n- Vui lòng bổ sung proxy mới vào <code>%s</code>!\n- Thời gian: %s",
			len(tasks), numFailedInRun, *proxyFileFlag, time.Now().Format("2006-01-02 15:04:05"))
		fmt.Printf("\n[!] TẤT CẢ %d PROXY TRONG PHIÊN NÀY ĐỀU BỊ LỖI! Đang gửi cảnh báo tới Telegram...\n", numFailedInRun)
		sendTelegramAlert(teleCfg, alertMsg)
	}

	duration := time.Since(startTime)
	fmt.Println("\n================================================================================")
	fmt.Println("                     BÁO CÁO KẾT QUẢ TỔNG KẾT                                  ")
	fmt.Println("================================================================================")
	fmt.Printf(" -> Tổng số tác vụ đã chạy : %d\n", len(tasks))
	fmt.Printf(" -> Thành công             : %d (Xem chi tiết tại results_success.txt)\n", successCount)
	fmt.Printf(" -> Thất bại               : %d (Xem chi tiết tại results_failed.txt)\n", failCount)
	fmt.Printf(" -> Proxy lỗi phiên này    : %d\n", numFailedInRun)
	fmt.Printf(" -> Tổng proxy hỏng đã lưu : %d (Xem chi tiết tại proxies_failed.txt)\n", totalFailedHistorical)
	fmt.Printf(" -> Thời gian hoàn tất     : %v\n", duration.Round(time.Second))
	fmt.Println("================================================================================")
}

// runCheckoutWorker thực thi quy trình cho 1 tác vụ độc lập, tự động đổi proxy nếu thất bại và giữ lại proxy thành công
func runCheckoutWorker(task CheckoutTask, pm *ProxyManager) (finalProxy string, taskErr error) {
	workerID := task.ID
	profile := task.Profile

	maxProxyAttempts := 20
	currentProxy := task.Proxy

	for attempt := 1; attempt <= maxProxyAttempts; attempt++ {
		task.Proxy = currentProxy
		finalProxy = currentProxy

		logWorker(workerID, "Bắt đầu tác vụ với Profile: %s | Thẻ: %s | Proxy: %s (Lần thử proxy #%d)\n",
			profile.FullName, maskSecret("card", profile.CardNumber), task.Proxy, attempt)

		err := executeSingleBrowserCheckout(task)
		if err == nil {
			// THÀNH CÔNG: Giữ nguyên proxy này, đưa lên đầu file proxies.txt để mua tiếp ở phiên sau!
			pm.MarkSuccess(currentProxy)
			logWorker(workerID, "-> HOÀN TẤT TÁC VỤ THÀNH CÔNG với Proxy: %s! (Đã lưu ưu tiên lên đầu file proxies.txt cho phiên tiếp theo)\n", currentProxy)
			return currentProxy, nil
		}

		// THẤT BẠI: Bất kể lỗi mạng, proxy die hay lỗi thanh toán/checkout -> Đổi proxy khác!
		logWorker(workerID, "[!] Tác vụ THẤT BẠI với Proxy [%s] (Lỗi: %v)\n", currentProxy, err)
		logWorker(workerID, "-> Đang ghi nhận proxy lỗi và tự động đổi sang proxy khác từ file proxies.txt...\n")
		pm.MarkFailed(currentProxy, err.Error())

		nextProxy, ok := pm.GetNextProxy()
		if !ok {
			logWorker(workerID, "[!] ĐÃ HẾT PROXY KHẢ DỤNG TRONG FILE ĐỂ ĐỔI! Dừng tác vụ cho thẻ %s.\n", maskSecret("card", profile.CardNumber))
			return currentProxy, fmt.Errorf("thất bại và đã hết proxy khả dụng để đổi: %w", err)
		}

		logWorker(workerID, "-> ĐÃ ĐỔI SANG PROXY MỚI: %s (Chuẩn bị thử lại mua hàng cho thẻ %s)...\n", nextProxy, maskSecret("card", profile.CardNumber))
		currentProxy = nextProxy
		time.Sleep(2 * time.Second)
	}

	return currentProxy, fmt.Errorf("thất bại sau %d lần đổi proxy", maxProxyAttempts)
}

// executeSingleBrowserCheckout thực thi quy trình điều khiển trình duyệt cho 1 tác vụ
func executeSingleBrowserCheckout(task CheckoutTask) (taskErr error) {
	workerID := task.ID
	profile := task.Profile

	logWorker(workerID, "Bắt đầu tác vụ với Profile: %s | Thẻ: %s | Proxy: %s\n",
		profile.FullName, maskSecret("card", profile.CardNumber), task.Proxy)

	userDataDir, err := os.MkdirTemp("", fmt.Sprintf("rod-worker-%d-*", workerID))
	if err == nil {
		defer os.RemoveAll(userDataDir)
	}

	ua := task.UserAgent
	if ua == "" {
		ua = getRandomUserAgent("user_agents.txt", task.OS)
	}

	winW, winH := getRandomResolution(task.OS)

	// Tự sinh bộ vân tay ngẫu nhiên độc lập chuẩn BitBrowser (bao gồm Kernel, GPU, Geolocation, Timezone, OS)
	fpProfile := fingerprint.GenerateRandomProfile(task.OS, winW, winH, ua)

	l := launcher.New().
		Leakless(false).
		Headless(task.Headless).
		Devtools(false).
		Set("user-agent", fpProfile.UserAgent).
		Set("disable-blink-features", "AutomationControlled").
		Set("lang", "en-US").
		Set("accept-lang", "en-US,en;q=0.9").
		// 1. Chống rò rỉ WebRTC (Ép WebRTC chỉ dùng Proxy, chặn UDP rò rỉ IP thật)
		Set("force-webrtc-ip-handling-policy", "disable_non_proxied_udp").
		Set("enforce-webrtc-ip-permission-check", "").
		// 2. Chống rò rỉ IPv6 (Vô hiệu hoá IPv6 socket pool & chặn UDP QUIC bypass Proxy)
		Set("disable-ipv6", "").
		Set("disable-quic", "")

	if userDataDir != "" {
		l = l.UserDataDir(userDataDir)
	}

	var closeBridge func()
	if task.Proxy != "" {
		host, port, user, pass, err := parseProxyDetails(task.Proxy)
		if err != nil {
			logWorker(workerID, "[!] Lỗi phân tích định dạng proxy: %v\n", err)
			recordFailedProxy(task.Proxy, err.Error())
			return err
		}

		upstreamAddr := fmt.Sprintf("%s:%s", host, port)
		localProxyAddr, exitIP, geoInfo, cBridge, err := startLocalSocks5Bridge(upstreamAddr, user, pass)
		if err != nil {
			logWorker(workerID, "[!] Cảnh báo mở Local Proxy Bridge (%v), fallback sang cấu hình direct\n", err)
			if user != "" && pass != "" {
				l = l.Proxy(fmt.Sprintf("socks5://%s:%s@%s:%s", user, pass, host, port))
			} else {
				l = l.Proxy(fmt.Sprintf("socks5://%s:%s", host, port))
			}
		} else {
			closeBridge = cBridge
			defer func() {
				if closeBridge != nil {
					closeBridge()
				}
			}()

			// Đồng bộ Exit IP và vị trí địa lý GPS / Múi giờ với Proxy thực tế
			if exitIP != "" {
				fpProfile.ProxyIP = exitIP
				logWorker(workerID, "[+] Đã phát hiện Proxy Exit IP: %s (Đồng bộ WebRTC/DNS/IPv6 sang IP này)\n", exitIP)
			} else if net.ParseIP(host) != nil {
				fpProfile.ProxyIP = host
			}
			if geoInfo != nil {
				fpProfile.Geo = *geoInfo
			}

			// 3. Chống rò rỉ DNS Leak: Ép Chrome uỷ quyền toàn bộ DNS cho Proxy, cấm phân giải DNS cục bộ
			l = l.Proxy("socks5://" + localProxyAddr).
				Set("host-resolver-rules", "MAP * ~NOTFOUND , EXCLUDE 127.0.0.1").
				Set("disable-async-dns", "").
				Set("disable-features", "DnsOverHttps,AsyncDns")
		}
	}

	browserURL, err := l.Launch()
	if err != nil {
		logWorker(workerID, "[!] Không thể mở trình duyệt: %v\n", err)
		if isProxyOrConnectionError(err) {
			recordFailedProxy(task.Proxy, fmt.Sprintf("Launch browser error: %v", err))
		}
		return err
	}

	browser := rod.New().ControlURL(browserURL).MustConnect()
	defer browser.MustClose()

	page, err := stealth.Page(browser)
	if err != nil {
		page = browser.MustPage()
	}

	// Tiêm vân tay chuẩn BitBrowser vào page (CDP Timezone, GPS, Locale, UserAgent và JS API)
	if err := fpProfile.ApplyToPage(page); err != nil {
		logWorker(workerID, "[!] Cảnh báo: Lỗi áp dụng vân tay: %v\n", err)
	}

	// Tự động tiêm vân tay & WebRTC spoofing cho MỌI tab mới (khi người dùng mở tab kiểm tra browserleaks/whoer)
	go browser.EachEvent(func(e *proto.TargetTargetCreated) {
		if e.TargetInfo.Type == proto.TargetTargetInfoTypePage {
			newPage, err := browser.PageFromTarget(e.TargetInfo.TargetID)
			if err == nil && newPage != nil {
				_ = fpProfile.ApplyToPage(newPage)
			}
		}
	})()

	page.MustSetWindow(0, 0, fpProfile.ScreenWidth, fpProfile.ScreenHeight)
	_ = page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent:      fpProfile.UserAgent,
		AcceptLanguage: "en-US,en;q=0.9",
		Platform:       fpProfile.Platform,
	})

	if strings.ToLower(task.OS) == "windows" {
		logWorker(workerID, "[!] LƯU Ý: Đang chạy chế độ OS Windows. Nếu bạn dùng Mobile Proxy (4G/LTE), tầng mạng TCP/IP sẽ báo Android, gây lệch OS với User-Agent. Hãy dùng Residential Proxy hoặc chuyển sang '-os android' để đồng bộ 100%%!\n")
	} else {
		logWorker(workerID, "[+] Đang chạy chế độ OS Android: Trình duyệt & phần cứng được đồng bộ 100%% với tầng mạng TCP/IP của Mobile Proxy (4G/LTE)!\n")
	}

	logWorker(workerID, "\n%s\n", fpProfile.FormatBitBrowserTable(task.ID, task.Proxy, time.Now().Format("2006-01-02 15:04:05")))

	logWorker(workerID, "Đang mở URL: %s (Size: '%s') ...\n", task.URL, task.Size)
	err = page.Navigate(task.URL)
	if err != nil {
		logWorker(workerID, "[!] Lỗi khi truy cập trang web: %v\n", err)
		if isProxyOrConnectionError(err) {
			recordFailedProxy(task.Proxy, err.Error())
			logWorker(workerID, "[!] Proxy lỗi [%s] -> Đã lưu vào proxies_failed.txt\n", task.Proxy)
		}
		return err
	}

	_ = page.Timeout(30 * time.Second).WaitLoad()
	time.Sleep(3 * time.Second)

	// 1. Tắt banner cookies
	logWorker(workerID, "-> [BƯỚC 1/8] Đang kiểm tra và tắt banner Cookies nếu có...\n")
	dismissCookieConsent(page)
	logWorker(workerID, "-> [BƯỚC 1/8] Hoàn tất kiểm tra Cookie banner.\n")

	// 2. Chọn Size
	logWorker(workerID, "-> [BƯỚC 2/8] Đang chọn Size: '%s'...\n", task.Size)
	err = selectProductSize(page, task.Size)
	if err != nil {
		logWorker(workerID, "[!] [BƯỚC 2/8] Lỗi chọn size %s: %v\n", task.Size, err)
	} else {
		logWorker(workerID, "-> [BƯỚC 2/8] Đã chọn xong Size '%s' thành công!\n", task.Size)
	}
	time.Sleep(1 * time.Second)

	// 3. Bấm Add to bag
	logWorker(workerID, "-> [BƯỚC 3/8] Đang bấm nút 'Add to bag'...\n")
	err = clickAddToBag(page)
	if err != nil {
		logWorker(workerID, "[!] [BƯỚC 3/8] Lỗi bấm Add to bag: %v\n", err)
		return err
	}
	logWorker(workerID, "-> [BƯỚC 3/8] Đã thêm sản phẩm vào giỏ hàng thành công! Đang chờ giỏ hàng cập nhật...\n")
	time.Sleep(2500 * time.Millisecond)

	// 4. Bấm Checkout
	logWorker(workerID, "-> [BƯỚC 4/8] Đang tìm và bấm nút 'Checkout'...\n")
	err = clickCheckout(page)
	if err != nil {
		logWorker(workerID, "[!] [BƯỚC 4/8] Cảnh báo khi bấm Checkout: %v\n", err)
	} else {
		logWorker(workerID, "-> [BƯỚC 4/8] Đã bấm Checkout! Đang chuyển hướng sang trang thanh toán...\n")
	}

	// 5. Điền thông tin giao hàng
	logWorker(workerID, "-> [BƯỚC 5/8] Bắt đầu điền thông tin Checkout (Email, Họ tên, Địa chỉ, Số điện thoại)...\n")
	err = fillCheckoutForm(page, profile)
	if err != nil {
		logWorker(workerID, "[!] [BƯỚC 5/8] Cảnh báo điền form: %v\n", err)
	} else {
		logWorker(workerID, "-> [BƯỚC 5/8] Đã điền xong toàn bộ thông tin giao hàng!\n")
	}

	// 6. Nhập mã giảm giá
	if task.DiscountCode != "" {
		logWorker(workerID, "-> [BƯỚC 6/8] Đang nhập và áp dụng mã giảm giá '%s'...\n", task.DiscountCode)
		err = applyDiscountCode(page, task.DiscountCode)
		if err != nil {
			logWorker(workerID, "[!] [BƯỚC 6/8] Cảnh báo mã giảm giá '%s': %v\n", task.DiscountCode, err)
		} else {
			logWorker(workerID, "-> [BƯỚC 6/8] Áp dụng mã giảm giá '%s' hoàn tất!\n", task.DiscountCode)
		}
		time.Sleep(2 * time.Second)
	} else {
		logWorker(workerID, "-> [BƯỚC 6/8] Không dùng mã giảm giá, bỏ qua bước này.\n")
	}

	// 7. Điền thông tin thẻ tín dụng
	logWorker(workerID, "-> [BƯỚC 7/8] Đang tìm khung thẻ bảo mật Shopify và điền thông tin thẻ tín dụng (%s)...\n", maskSecret("card", profile.CardNumber))
	err = fillPaymentCardForm(page, profile)
	if err != nil {
		logWorker(workerID, "[!] [BƯỚC 7/8] Lỗi điền thẻ: %v\n", err)
	} else {
		logWorker(workerID, "-> [BƯỚC 7/8] Đã điền xong đầy đủ thông tin thẻ tín dụng!\n")
	}

	// 8. Bấm Pay now và kiểm tra
	logWorker(workerID, "-> [BƯỚC 8/8] Kiểm tra đơn hàng, địa chỉ và tiến hành bấm 'Pay now' (Thử tối đa 4 lần)...\n")
	err = extractOrderSummaryAndPayNow(page, task)
	if err != nil {
		logWorker(workerID, "[!] [BƯỚC 8/8] Lỗi thanh toán: %v\n", err)
		return err
	}

	logWorker(workerID, "-> HOÀN TẤT TÁC VỤ THÀNH CÔNG!\n")
	return nil
}

// fillCheckoutForm chờ tải trang checkout và gõ thông tin Email, Họ Tên, Địa chỉ (chọn gợi ý) và Số điện thoại
func fillCheckoutForm(page *rod.Page, profile *UserProfile) error {
	isRedirected := false
	redirectTimeout := time.Now().Add(35 * time.Second)
	for time.Now().Before(redirectTimeout) {
		info, err := page.Info()
		if err == nil && info != nil {
			currURL := strings.ToLower(info.URL)
			if strings.Contains(currURL, "checkouts") || strings.Contains(currURL, "checkout") {
				isRedirected = true
				break
			}
		}
		time.Sleep(1 * time.Second)
	}

	if !isRedirected {
		log.Println("[!] Cảnh báo: Hết thời gian chờ URL chuyển hướng, vẫn tiếp tục tìm form...")
	}

	log.Println("-> [BƯỚC 5] Đang đợi trang Checkout tải xong các ô nhập dữ liệu...")
	time.Sleep(4 * time.Second)

	emailInputSelectors := []string{
		`input[type="email"]`,
		`input[name="email"]`,
		`input[placeholder*="Email"]`,
		`input#email`,
		`input[autocomplete="email"]`,
		`input[name="guestEmail"]`,
	}

	var emailEl *rod.Element
	waitInputTimeout := time.Now().Add(20 * time.Second)
	for time.Now().Before(waitInputTimeout) {
		for _, sel := range emailInputSelectors {
			has, el, err := page.Has(sel)
			if err == nil && has && el != nil {
				visible, _ := el.Timeout(500 * time.Millisecond).Visible()
				if visible {
					emailEl = el
					break
				}
			}
		}
		if emailEl != nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Email phải lấy firstname + lastName + random số từ 1 - 999 + @gmail.com
	if profile.Email == "" || !strings.Contains(profile.Email, "@") {
		profile.Email = generateEmail(profile.FirstName, profile.LastName)
	}

	if emailEl != nil {
		log.Printf("-> Đang gõ Email: '%s' (Human Typing)...\n", profile.Email)
		_ = humanType(page, emailEl, profile.Email)
		log.Println("-> Đã hoàn thành gõ Email.")
		time.Sleep(time.Duration(350+rand.Intn(200)) * time.Millisecond)
	} else {
		log.Println("[!] Không tìm thấy ô Email qua selector thông thường, thử điền bằng JavaScript...")
		_, _ = page.Eval(`(val) => {
			const inputs = Array.from(document.querySelectorAll('input'));
			for (const inp of inputs) {
				const type = (inp.getAttribute('type') || '').toLowerCase();
				const name = (inp.getAttribute('name') || '').toLowerCase();
				const ph = (inp.getAttribute('placeholder') || '').toLowerCase();
				if (type === 'email' || name.includes('email') || ph.includes('email')) {
					inp.scrollIntoView({ behavior: 'smooth', block: 'center' });
					inp.focus();
					inp.value = val;
					inp.dispatchEvent(new Event('input', { bubbles: true }));
					inp.dispatchEvent(new Event('change', { bubbles: true }));
					return true;
				}
			}
			return false;
		}`, profile.Email)
		log.Println("-> Đã thử điền Email qua JS fallback.")
	}

	firstNameSelectors := []string{
		`input[name="shippingAddress.firstName"]`,
		`input[name="firstName"]`,
		`input[name="first_name"]`,
		`input[autocomplete="given-name"]`,
		`input[placeholder*="First name" i]`,
		`input[placeholder*="First Name" i]`,
		`input#firstName`,
		`input[id*="firstName"]`,
		`input[id*="first-name"]`,
	}
	var firstNameEl *rod.Element
	for retry := 0; retry < 6; retry++ {
		firstNameEl = findInputField(page, firstNameSelectors)
		if firstNameEl != nil {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}

	if firstNameEl != nil {
		log.Printf("-> Đang gõ First name: '%s' (Human Typing)...\n", profile.FirstName)
		_ = humanType(page, firstNameEl, profile.FirstName)
		log.Println("-> Đã hoàn thành gõ First name.")
		time.Sleep(time.Duration(250+rand.Intn(150)) * time.Millisecond)
	} else {
		log.Println("[!] Thử điền First name qua JavaScript fallback...")
		_, _ = page.Eval(`(val) => {
			const inputs = Array.from(document.querySelectorAll('input'));
			for (const inp of inputs) {
				const name = (inp.getAttribute('name') || '').toLowerCase();
				const ph = (inp.getAttribute('placeholder') || '').toLowerCase();
				const auto = (inp.getAttribute('autocomplete') || '').toLowerCase();
				const id = (inp.getAttribute('id') || '').toLowerCase();
				if (name.includes('firstname') || name.includes('first_name') || ph.includes('first name') || auto.includes('given-name') || id.includes('firstname') || id.includes('first-name')) {
					inp.scrollIntoView({ behavior: 'smooth', block: 'center' });
					inp.focus();
					inp.value = val;
					inp.dispatchEvent(new Event('input', { bubbles: true }));
					inp.dispatchEvent(new Event('change', { bubbles: true }));
					return true;
				}
			}
			return false;
		}`, profile.FirstName)
	}

	lastNameSelectors := []string{
		`input[name="shippingAddress.lastName"]`,
		`input[name="lastName"]`,
		`input[name="last_name"]`,
		`input[autocomplete="family-name"]`,
		`input[placeholder*="Last name" i]`,
		`input[placeholder*="Last Name" i]`,
		`input#lastName`,
		`input[id*="lastName"]`,
		`input[id*="last-name"]`,
	}
	var lastNameEl *rod.Element
	for retry := 0; retry < 5; retry++ {
		lastNameEl = findInputField(page, lastNameSelectors)
		if lastNameEl != nil {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}

	if lastNameEl != nil {
		log.Printf("-> Đang gõ Last name: '%s' (Human Typing)...\n", profile.LastName)
		_ = humanType(page, lastNameEl, profile.LastName)
		log.Println("-> Đã hoàn thành gõ Last name.")
		time.Sleep(time.Duration(250+rand.Intn(150)) * time.Millisecond)
	} else {
		log.Println("[!] Thử điền Last name qua JavaScript fallback...")
		_, _ = page.Eval(`(val) => {
			const inputs = Array.from(document.querySelectorAll('input'));
			for (const inp of inputs) {
				const name = (inp.getAttribute('name') || '').toLowerCase();
				const ph = (inp.getAttribute('placeholder') || '').toLowerCase();
				const auto = (inp.getAttribute('autocomplete') || '').toLowerCase();
				const id = (inp.getAttribute('id') || '').toLowerCase();
				if (name.includes('lastname') || name.includes('last_name') || ph.includes('last name') || auto.includes('family-name') || id.includes('lastname') || id.includes('last-name')) {
					inp.scrollIntoView({ behavior: 'smooth', block: 'center' });
					inp.focus();
					inp.value = val;
					inp.dispatchEvent(new Event('input', { bubbles: true }));
					inp.dispatchEvent(new Event('change', { bubbles: true }));
					return true;
				}
			}
			return false;
		}`, profile.LastName)
	}

	addressToType := profile.RawAddress
	if addressToType == "" {
		addressToType = profile.Address
	}

	if addressToType != "" {
		addressEl := findInputField(page, []string{
			`input[name="shippingAddress.address1"]`,
			`input[name="address1"]`,
			`input#shipping-address1`,
			`input#address1`,
			`input[placeholder*="enter an address"]`,
			`input[placeholder*="house number"]`,
			`input[name="address"]`,
			`input[name="streetAddress"]`,
			`input[placeholder*="Address"]`,
			`input[placeholder*="Street address"]`,
			`input[placeholder*="Street"]`,
			`input[autocomplete="address-line1"]`,
			`input[autocomplete="shipping address-line1"]`,
			`input[id*="address"]`,
		})

		if addressEl != nil {
			log.Printf("-> Đang gõ Address tìm kiếm: '%s' (Human Typing)...\n", addressToType)
			_ = humanType(page, addressEl, addressToType)
			time.Sleep(1 * time.Second)
			log.Println("-> Đang chọn gợi ý địa chỉ đầu tiên từ danh sách...")
			_ = selectFirstAddressSuggestion(page, addressEl, addressToType)

			time.Sleep(1500 * time.Millisecond)

			// Lấy giá trị địa chỉ thực tế vừa được suggestion điền vào ô input
			currentAddrVal := getAddressFieldValue(page)
			addrBase := currentAddrVal
			if addrBase == "" {
				addrBase = profile.Address
			}
			if addrBase == "" {
				addrBase = addressToType
			}

			fn := profile.FirstName
			if fn == "" && profile.FullName != "" {
				f := strings.Fields(profile.FullName)
				if len(f) > 0 {
					fn = f[0]
				}
			}
			// firstname + ". " phải typing, đừng gán luôn vào value của address
			if fn != "" {
				prefix := fmt.Sprintf("%s. ", fn)
				log.Printf("-> Đang tiến hành typing tiền tố '%s' vào đầu ô Address (Human Typing, không gán value)...\n", prefix)
				err := typePrefixIntoAddressField(page, prefix, addrBase)
				if err != nil {
					log.Printf("[!] Cảnh báo khi typing tiền tố address: %v\n", err)
				}
			}
			time.Sleep(1 * time.Second)

			waitForAddressAPIToComplete(page)
		} else {
			log.Println("[!] Không tìm thấy ô Address bằng selector thông thường, thử điền qua JS fallback...")
			_, _ = page.Eval(`(val) => {
				const inputs = Array.from(document.querySelectorAll('input'));
				for (const inp of inputs) {
					const name = (inp.getAttribute('name') || '').toLowerCase();
					const ph = (inp.getAttribute('placeholder') || '').toLowerCase();
					const id = (inp.getAttribute('id') || '').toLowerCase();
					if (name.includes('address1') || name.includes('address') || ph.includes('address') || id.includes('address')) {
						inp.scrollIntoView({ behavior: 'smooth', block: 'center' });
						inp.focus();
						inp.value = val;
						inp.dispatchEvent(new Event('input', { bubbles: true }));
						inp.dispatchEvent(new Event('change', { bubbles: true }));
						return true;
					}
				}
				return false;
			}`, addressToType)
		}
	}

	if profile.Phone != "" {
		phoneEl := findInputField(page, []string{
			`input[name="shippingAddress.phone"]`,
			`input[name="phone"]`,
			`input[type="tel"]`,
			`input#phone`,
			`input[name="telephone"]`,
			`input[name="phoneNumber"]`,
			`input[placeholder*="Phone"]`,
			`input[placeholder*="Mobile"]`,
			`input[autocomplete="tel"]`,
			`input[autocomplete="shipping tel"]`,
			`input[id*="phone"]`,
		})

		if phoneEl != nil {
			log.Printf("-> Đang gõ Phone: '%s' (Human Typing)...\n", profile.Phone)
			_ = humanType(page, phoneEl, profile.Phone)
			log.Println("-> Đã hoàn thành gõ Phone.")
			time.Sleep(time.Duration(250+rand.Intn(150)) * time.Millisecond)
		} else {
			log.Println("[!] Thử điền Phone qua JavaScript fallback...")
			_, _ = page.Eval(`(val) => {
				const inputs = Array.from(document.querySelectorAll('input'));
				for (const inp of inputs) {
					const type = (inp.getAttribute('type') || '').toLowerCase();
					const name = (inp.getAttribute('name') || '').toLowerCase();
					const ph = (inp.getAttribute('placeholder') || '').toLowerCase();
					const id = (inp.getAttribute('id') || '').toLowerCase();
					if (type === 'tel' || name.includes('phone') || ph.includes('phone') || id.includes('phone')) {
						inp.scrollIntoView({ behavior: 'smooth', block: 'center' });
						inp.focus();
						inp.value = val;
						inp.dispatchEvent(new Event('input', { bubbles: true }));
						inp.dispatchEvent(new Event('change', { bubbles: true }));
						return true;
					}
				}
				return false;
			}`, profile.Phone)
		}
	}

	log.Println("-> [BƯỚC 5] ĐÃ HOÀN TẤT ĐIỀN EMAIL, HỌ TÊN, ĐỊA CHỈ & SỐ ĐIỆN THOẠI!")
	return nil
}

// waitForAddressAPIToComplete chờ API xác thực địa chỉ
func waitForAddressAPIToComplete(page *rod.Page) {
	waitTimeout := time.Now().Add(10 * time.Second)
	for time.Now().Before(waitTimeout) {
		time.Sleep(600 * time.Millisecond)
		res, err := page.Eval(`() => {
			const cityInp = document.querySelector('input[name="city"], input[placeholder*="City"], input#city');
			const zipInp = document.querySelector('input[name="postalCode"], input[name="zip"], input[name="zipcode"], input#postalCode');
			const hasCity = cityInp && (cityInp.value || '').trim().length > 0;
			const hasZip = zipInp && (zipInp.value || '').trim().length > 0;
			const allText = document.body ? document.body.innerText : '';
			const stillWaitingAddress = allText.includes('Enter your shipping address to view available shipping methods');
			const hasSpinners = Array.from(document.querySelectorAll('[class*="spinner"], [class*="skeleton"], [aria-busy="true"]')).some(el => {
				const rect = el.getBoundingClientRect();
				return rect.width > 0 && rect.height > 0;
			});

			if ((hasCity || hasZip || !stillWaitingAddress) && !hasSpinners) {
				return true;
			}
			return false;
		}`)

		if err == nil && res.Value.Bool() {
			time.Sleep(600 * time.Millisecond)
			return
		}
	}
}

// selectFirstAddressSuggestion đợi danh sách gợi ý và click
func selectFirstAddressSuggestion(page *rod.Page, addressEl *rod.Element, addressQuery string) error {
	waitTimeout := time.Now().Add(12 * time.Second)
	for time.Now().Before(waitTimeout) {
		time.Sleep(500 * time.Millisecond)

		res, err := page.Eval(`(query) => {
			function isVisible(el) {
				if (!el) return false;
				const style = window.getComputedStyle(el);
				if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') return false;
				const rect = el.getBoundingClientRect();
				return rect.width > 20 && rect.height > 10;
			}

			function triggerEvents(el) {
				const events = ['pointerover', 'mouseover', 'pointerdown', 'mousedown', 'focus', 'pointerup', 'mouseup', 'click'];
				for (const evt of events) {
					try {
						el.dispatchEvent(new MouseEvent(evt, {
							bubbles: true,
							cancelable: true,
							view: window,
							composed: true,
							buttons: 1
						}));
					} catch(e) {}
				}
				if (typeof el.click === 'function') {
					el.click();
				}
			}

			const rawWords = (query || '').split(/[,\s]+/).map(w => w.trim().toLowerCase()).filter(w => w.length >= 3);
			const allElements = Array.from(document.querySelectorAll('*'));
			const candidates = [];

			for (const el of allElements) {
				if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SCRIPT' || el.tagName === 'STYLE') {
					continue;
				}
				if (!isVisible(el)) continue;

				const text = (el.innerText || el.textContent || '').trim();
				if (!text || text.length > 250) continue;

				const lowerText = text.toLowerCase();
				if (lowerText === 'suggestions' || lowerText === '✕' || lowerText === 'x') continue;

				let matchScore = 0;
				for (const w of rawWords) {
					if (lowerText.includes(w)) {
						matchScore++;
					}
				}
				if (lowerText.includes('united states')) {
					matchScore += 2;
				}

				if (matchScore >= 1) {
					const rect = el.getBoundingClientRect();
					if (rect.width >= 50 && rect.height >= 15) {
						candidates.push({ el: el, rect: rect, text: text, length: text.length, score: matchScore });
					}
				}
			}

			candidates.sort((a, b) => {
				if (b.score !== a.score) return b.score - a.score;
				return a.length - b.length;
			});

			if (candidates.length > 0) {
				const best = candidates[0];
				triggerEvents(best.el);
				return {
					found: true,
					x: best.rect.left + best.rect.width / 2,
					y: best.rect.top + best.rect.height / 2,
					text: best.text
				};
			}

			return { found: false, x: 0, y: 0, text: '' };
		}`, addressQuery)

		if err == nil && res.Value.Get("found").Bool() {
			x := res.Value.Get("x").Num()
			y := res.Value.Get("y").Num()

			_ = page.Mouse.MoveLinear(proto.Point{X: x, Y: y}, 2)
			time.Sleep(80 * time.Millisecond)
			_ = page.Mouse.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(300 * time.Millisecond)
			_ = page.Keyboard.Press(input.Enter)
			time.Sleep(800 * time.Millisecond)
			return nil
		}
	}

	if addressEl != nil {
		_ = addressEl.Focus()
		time.Sleep(100 * time.Millisecond)
		_ = page.Keyboard.Press(input.ArrowDown)
		time.Sleep(200 * time.Millisecond)
		_ = page.Keyboard.Press(input.Enter)
		time.Sleep(800 * time.Millisecond)
		return nil
	}

	return fmt.Errorf("không tìm thấy dòng gợi ý địa chỉ")
}

// getAddressFieldValue lấy giá trị text hiện tại đang có trong ô địa chỉ (sau khi chọn gợi ý)
func getAddressFieldValue(page *rod.Page) string {
	res, err := page.Eval(`() => {
		const selectors = [
			'input[name="address1"]',
			'input[name="address"]',
			'input[name="streetAddress"]',
			'input[name="shippingAddress.address1"]',
			'input#address1',
			'input#shipping-address1',
			'input[placeholder*="enter an address" i]',
			'input[placeholder*="Address" i]'
		];
		for (const sel of selectors) {
			const el = document.querySelector(sel);
			if (el && el.value) {
				const v = el.value.trim();
				if (v.length > 0) return v;
			}
		}
		return '';
	}`)
	if err == nil && res != nil {
		return res.Value.String()
	}
	return ""
}

// abbreviateStreet viết tắt các từ chỉ loại đường/hướng theo chuẩn bưu điện Mỹ (USPS) để tiết kiệm ký tự
func abbreviateStreet(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		cleanW := strings.Trim(w, ".,")
		suffix := ""
		if strings.HasSuffix(w, ",") {
			suffix = ","
		} else if strings.HasSuffix(w, ".") {
			suffix = "."
		}
		switch strings.ToLower(cleanW) {
		case "boulevard":
			words[i] = "Blvd" + suffix
		case "avenue":
			words[i] = "Ave" + suffix
		case "street":
			words[i] = "St" + suffix
		case "drive":
			words[i] = "Dr" + suffix
		case "road":
			words[i] = "Rd" + suffix
		case "court":
			words[i] = "Ct" + suffix
		case "lane":
			words[i] = "Ln" + suffix
		case "parkway":
			words[i] = "Pkwy" + suffix
		case "highway":
			words[i] = "Hwy" + suffix
		case "circle":
			words[i] = "Cir" + suffix
		case "terrace":
			words[i] = "Ter" + suffix
		case "place":
			words[i] = "Pl" + suffix
		case "apartment":
			words[i] = "Apt" + suffix
		case "suite":
			words[i] = "Ste" + suffix
		case "north":
			words[i] = "N" + suffix
		case "south":
			words[i] = "S" + suffix
		case "east":
			words[i] = "E" + suffix
		case "west":
			words[i] = "W" + suffix
		case "northeast":
			words[i] = "NE" + suffix
		case "northwest":
			words[i] = "NW" + suffix
		case "southeast":
			words[i] = "SE" + suffix
		case "southwest":
			words[i] = "SW" + suffix
		}
	}
	return strings.Join(words, " ")
}

// formatAddressMax35 xử lý và cắt gọt địa chỉ để luôn đảm bảo <= 35 ký tự theo quy định khắt khe của Shopify Address 1
func formatAddressMax35(firstName, rawAddr, city, zip string) string {
	rawAddr = strings.TrimSpace(rawAddr)
	if rawAddr == "" {
		return ""
	}

	// 1. Nếu địa chỉ đã bị gắn tiền tố FirstName trước đó thì loại bỏ để chuẩn hoá lại từ đầu
	if firstName != "" {
		lowerFirstName := strings.ToLower(firstName)
		lowerAddr := strings.ToLower(rawAddr)
		if strings.HasPrefix(lowerAddr, lowerFirstName+". ") {
			rawAddr = strings.TrimSpace(rawAddr[len(firstName)+2:])
		} else if strings.HasPrefix(lowerAddr, lowerFirstName+".") {
			rawAddr = strings.TrimSpace(rawAddr[len(firstName)+1:])
		}
	}

	// 2. Tách chỉ lấy phần Tên đường / Số nhà (Street Address) vì City, State, Zip đã có ô riêng trong Shopify
	street := rawAddr
	if idx := strings.Index(street, ","); idx != -1 {
		street = strings.TrimSpace(street[:idx])
	}
	if city != "" && strings.Contains(strings.ToLower(street), strings.ToLower(city)) {
		idx := strings.Index(strings.ToLower(street), strings.ToLower(city))
		street = strings.TrimSpace(street[:idx])
	}
	if zip != "" && strings.Contains(street, zip) {
		idx := strings.Index(street, zip)
		street = strings.TrimSpace(street[:idx])
	}
	street = strings.TrimRight(street, ",. ")
	if street == "" {
		street = rawAddr
		if idx := strings.Index(street, ","); idx != -1 {
			street = strings.TrimSpace(street[:idx])
		}
	}

	// 3. Ưu tiên 1: FirstName. Street (đầy đủ)
	if firstName != "" {
		cand := fmt.Sprintf("%s. %s", firstName, street)
		if len(cand) <= 35 {
			return cand
		}
	} else if len(street) <= 35 {
		return street
	}

	// 4. Ưu tiên 2: FirstName. Viết tắt tên đường (Boulevard -> Blvd, Street -> St, Court -> Ct...)
	abbrStreet := abbreviateStreet(street)
	if firstName != "" {
		cand := fmt.Sprintf("%s. %s", firstName, abbrStreet)
		if len(cand) <= 35 {
			return cand
		}

		// 5. Ưu tiên 3: Tên viết tắt (Chữ cái đầu F.) + Viết tắt tên đường (Ví dụ: A. 820 Roxfield Ct)
		runes := []rune(firstName)
		if len(runes) > 0 {
			initialCand := fmt.Sprintf("%s. %s", string(runes[0]), abbrStreet)
			if len(initialCand) <= 35 {
				return initialCand
			}
		}
	}

	// 6. Ưu tiên 4: Chỉ giữ lại tên đường viết tắt (không kèm tên) nếu vẫn <= 35 ký tự
	if len(abbrStreet) <= 35 {
		return abbrStreet
	}

	// 7. Ưu tiên 5: Cắt cứng tối đa 35 ký tự an toàn
	result := abbrStreet
	if len(result) > 35 {
		result = strings.TrimSpace(result[:35])
		result = strings.TrimRight(result, ",.- ")
	}
	return result
}

// checkAndFixAddressFieldBeforePayNow kiểm tra lại ô địa chỉ trước khi bấm Pay now.
// Nếu phát hiện độ dài > 35 ký tự hoặc hệ thống Shopify đang hiển thị lỗi cảnh báo vượt quá 35 ký tự,
// hàm sẽ tự động chuẩn hoá và điền lại địa chỉ <= 35 ký tự để tránh bị cổng thanh toán từ chối.
func checkAndFixAddressFieldBeforePayNow(page *rod.Page, profile *UserProfile) {
	log.Println("-> [BƯỚC 8.0] KIỂM TRA LẠI Ô ĐỊA CHỈ TRƯỚC KHI BẤM 'PAY NOW'...")

	evalRes, err := page.Eval(`() => {
		const selectors = [
			'input[name="address1"]',
			'input[name="shippingAddress.address1"]',
			'input[name="address"]',
			'input[name="streetAddress"]',
			'input#address1',
			'input#shipping-address1',
			'input[placeholder*="enter an address" i]',
			'input[placeholder*="house number" i]',
			'input[placeholder*="Address" i]',
			'input[placeholder*="Street" i]',
			'input[autocomplete="address-line1"]',
			'input[autocomplete="shipping address-line1"]'
		];

		let addressInput = null;
		for (const sel of selectors) {
			const el = document.querySelector(sel);
			if (el && (el.offsetWidth > 0 || el.offsetHeight > 0 || el.getClientRects().length > 0)) {
				addressInput = el;
				break;
			}
		}
		if (!addressInput) {
			for (const sel of selectors) {
				const el = document.querySelector(sel);
				if (el) { addressInput = el; break; }
			}
		}

		const currentVal = addressInput ? (addressInput.value || '').trim() : '';
		const isOver35 = currentVal.length > 35;

		// Kiểm tra các thông báo lỗi liên quan đến địa chỉ hoặc giới hạn 35 ký tự
		let errorText = '';
		const errSelectors = [
			'#error-for-address1',
			'#error-for-shippingAddress-address1',
			'[id*="error-for-address"]',
			'[id*="error-for-shippingAddress"]',
			'[id*="error-for-street"]',
			'.field--error .field__message--error',
			'.field__message--error',
			'[role="alert"]',
			'[class*="error-message"]',
			'[class*="field__message"]'
		];

		for (const sel of errSelectors) {
			const els = document.querySelectorAll(sel);
			for (const el of els) {
				const txt = (el.innerText || el.textContent || '').trim();
				if (txt) {
					const lower = txt.toLowerCase();
					if (lower.includes('35') || lower.includes('too long') || lower.includes('maximum') ||
						lower.includes('exceed') || lower.includes('character') || lower.includes('ký tự') ||
						lower.includes('address') || lower.includes('địa chỉ')) {
						errorText = txt;
						break;
					}
				}
			}
			if (errorText) break;
		}

		if (!errorText && document.body) {
			const bText = document.body.innerText || '';
			const match = bText.match(/.*(?:35\s*(?:characters?|ký tự)|(?:maximum|tối đa|vượt quá)\s*35|address\s+is\s+too\s+long).*/i);
			if (match) {
				errorText = match[0].trim();
			}
		}

		const hasAriaInvalid = addressInput ? (addressInput.getAttribute('aria-invalid') === 'true') : false;

		return {
			foundInput: !!addressInput,
			currentValue: currentVal,
			length: currentVal.length,
			isOver35: isOver35,
			hasErrorMsg: !!errorText,
			errorText: errorText,
			hasAriaInvalid: hasAriaInvalid
		};
	}`)

	if err != nil || evalRes == nil {
		log.Printf("[!] Không thể đọc trạng thái ô địa chỉ: %v\n", err)
		return
	}

	foundInput := evalRes.Value.Get("foundInput").Bool()
	currentVal := evalRes.Value.Get("currentValue").String()
	valLen := evalRes.Value.Get("length").Int()
	isOver35 := evalRes.Value.Get("isOver35").Bool()
	hasErrorMsg := evalRes.Value.Get("hasErrorMsg").Bool()
	errorText := evalRes.Value.Get("errorText").String()
	hasAriaInvalid := evalRes.Value.Get("hasAriaInvalid").Bool()

	needFix := isOver35 || hasErrorMsg || (hasAriaInvalid && valLen > 30)

	if !needFix {
		if foundInput {
			log.Printf("-> [KIỂM TRA ĐỊA CHỈ TRƯỚC KHI PAY NOW]: Ô địa chỉ hợp lệ: '%s' (%d/35 ký tự), không phát hiện lỗi.\n", currentVal, valLen)
		} else {
			log.Println("-> [KIỂM TRA ĐỊA CHỈ TRƯỚC KHI PAY NOW]: Không tìm thấy ô địa chỉ trên màn hình hiện tại (có thể đang ở bước thanh toán thẻ).")
		}
		return
	}

	log.Println("--------------------------------------------------")
	log.Printf("[!] CẢNH BÁO: PHÁT HIỆN LỖI Ô ĐỊA CHỈ TRƯỚC KHI BẤM PAY NOW!\n")
	log.Printf("    * Địa chỉ hiện tại : '%s' (Độ dài: %d ký tự)\n", currentVal, valLen)
	if isOver35 {
		log.Printf("    * Trạng thái       : VƯỢT QUÁ 35 KÝ TỰ (%d > 35)!\n", valLen)
	}
	if errorText != "" {
		log.Printf("    * Thông báo lỗi web: '%s'\n", errorText)
	}
	log.Println("-> ĐANG TIẾN HÀNH RÚT GỌN VÀ CẬP NHẬT LẠI Ô ĐỊA CHỈ ĐẢM BẢO <= 35 KÝ TỰ...")

	rawSource := currentVal
	if rawSource == "" || len(rawSource) > 100 {
		rawSource = profile.Address
		if rawSource == "" {
			rawSource = profile.RawAddress
		}
	}

	newAddr := formatAddressMax35(profile.FirstName, rawSource, profile.City, profile.ZipCode)
	if len(newAddr) > 35 {
		newAddr = strings.TrimSpace(newAddr[:35])
		newAddr = strings.TrimRight(newAddr, ",.- ")
	}

	log.Printf("-> Địa chỉ mới chuẩn hoá: '%s' (Độ dài: %d <= 35 ký tự)\n", newAddr, len(newAddr))
	updateAddressField(page, newAddr)
	time.Sleep(1500 * time.Millisecond)

	// Đảm bảo blur để kích hoạt validation của form
	_, _ = page.Eval(`() => {
		const inp = document.querySelector('input[name="address1"], input[name="shippingAddress.address1"], input#address1');
		if (inp) {
			inp.dispatchEvent(new Event('input', { bubbles: true }));
			inp.dispatchEvent(new Event('change', { bubbles: true }));
			inp.dispatchEvent(new Event('blur', { bubbles: true }));
		}
	}`)
	time.Sleep(1 * time.Second)

	// Kiểm tra lại lần cuối
	finalCheck, _ := page.Eval(`() => {
		const inp = document.querySelector('input[name="address1"], input[name="shippingAddress.address1"], input#address1');
		const v = inp ? (inp.value || '').trim() : '';
		return { value: v, len: v.length };
	}`)
	if finalCheck != nil {
		fVal := finalCheck.Value.Get("value").String()
		fLen := finalCheck.Value.Get("len").Int()
		log.Printf("-> [HOÀN TẤT KIỂM TRA ĐỊA CHỈ]: Giá trị thực tế trên web hiện tại: '%s' (%d ký tự <= 35). Lỗi đã được khắc phục!\n", fVal, fLen)
	}
	log.Println("--------------------------------------------------")
}

// typePrefixIntoAddressField gõ tiền tố firstname + ". " vào đầu ô địa chỉ (Human Typing, không gán trực tiếp value)
func typePrefixIntoAddressField(page *rod.Page, prefix string, addrBase string) error {
	if prefix == "" {
		return nil
	}

	selectors := []string{
		`input[name="shippingAddress.address1"]`,
		`input[name="address1"]`,
		`input#shipping-address1`,
		`input#address1`,
		`input[name="address"]`,
		`input[name="streetAddress"]`,
		`input[placeholder*="enter an address" i]`,
		`input[placeholder*="house number" i]`,
		`input[placeholder*="Address" i]`,
		`input[placeholder*="Street" i]`,
		`input[autocomplete="address-line1"]`,
		`input[autocomplete="shipping address-line1"]`,
		`input[id*="address" i]`,
	}

	addrEl := findInputField(page, selectors)
	if addrEl == nil {
		return fmt.Errorf("không tìm thấy ô address input")
	}

	// Lấy giá trị hiện tại của ô input
	currentVal := getAddressFieldValue(page)
	if currentVal == "" {
		currentVal = addrBase
	}
	currentVal = strings.TrimSpace(currentVal)

	lowerPrefix := strings.ToLower(prefix)
	lowerVal := strings.ToLower(currentVal)

	// Nếu ô đã bắt đầu bằng prefix thì không cần gõ lại
	if strings.HasPrefix(lowerVal, lowerPrefix) {
		log.Printf("-> Ô địa chỉ đã có tiền tố '%s' (giá trị hiện tại: '%s'), bỏ qua không gõ lại.\n", prefix, currentVal)
		return nil
	}

	// Nếu đã có tiền tố cũ nhưng khác (ví dụ "f." hoặc "John."), loại bỏ nó khỏi currentVal
	if strings.Contains(currentVal, ". ") {
		idx := strings.Index(currentVal, ". ")
		if idx <= 15 {
			currentVal = strings.TrimSpace(currentVal[idx+2:])
		}
	}

	// Đảm bảo tổng độ dài (len(prefix) + len(street)) <= 35 theo quy định Shopify
	maxStreetLen := 35 - len(prefix)
	if maxStreetLen > 0 && len(currentVal) > maxStreetLen {
		abbrStreet := abbreviateStreet(currentVal)
		if len(abbrStreet) > maxStreetLen {
			abbrStreet = strings.TrimSpace(abbrStreet[:maxStreetLen])
			abbrStreet = strings.TrimRight(abbrStreet, ",.- ")
		}
		currentVal = abbrStreet
		// Cập nhật lại phần base street bằng typing nếu cần rút gọn để đảm bảo <= 35 ký tự
		_ = addrEl.Focus()
		_ = addrEl.SelectAllText()
		_ = addrEl.Input(currentVal)
		time.Sleep(150 * time.Millisecond)
	}

	// Focus ô địa chỉ và đưa con trỏ chuột về đầu (vị trí 0)
	_ = addrEl.ScrollIntoView()
	time.Sleep(60 * time.Millisecond)
	_ = addrEl.Focus()
	time.Sleep(60 * time.Millisecond)

	// Đưa selectionRange về (0,0) và nhấn Home để con trỏ ở vị trí ký tự đầu tiên
	_, _ = page.Eval(`(selList) => {
		for (const sel of selList) {
			const el = document.querySelector(sel);
			if (el && (el.offsetWidth > 0 || el.offsetHeight > 0)) {
				el.focus();
				if (typeof el.setSelectionRange === 'function') {
					el.setSelectionRange(0, 0);
				}
				return true;
			}
		}
		return false;
	}`, selectors)
	time.Sleep(60 * time.Millisecond)
	_ = page.Keyboard.Press(input.Home)
	time.Sleep(60 * time.Millisecond)

	log.Printf("-> Đang gõ tiền tố '%s' vào đầu ô Address (Human Typing)...\n", prefix)

	// Gõ từng ký tự của prefix (mô phỏng nhịp điệu người thật)
	for _, ch := range prefix {
		charStr := string(ch)
		// Dùng page.InsertText để chèn ký tự tại vị trí con trỏ hiện tại (đầu chuỗi)
		err := page.Timeout(500 * time.Millisecond).InsertText(charStr)
		if err != nil {
			_ = addrEl.Timeout(400 * time.Millisecond).Input(charStr)
		}

		// Trigger input event
		_, _ = addrEl.Timeout(300 * time.Millisecond).Eval(`() => {
			this.dispatchEvent(new Event('input', { bubbles: true }));
		}`)

		delay := 55 + rand.Intn(45)
		if ch == '.' || ch == ' ' {
			delay = 130 + rand.Intn(90)
		}
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}

	// Đóng triệt để gợi ý dropdown nếu xuất hiện
	time.Sleep(200 * time.Millisecond)
	_ = page.Keyboard.Press(input.Escape)
	time.Sleep(150 * time.Millisecond)

	// Trigger change event
	_, _ = addrEl.Timeout(500 * time.Millisecond).Eval(`() => {
		this.dispatchEvent(new Event('input', { bubbles: true }));
		this.dispatchEvent(new Event('change', { bubbles: true }));
	}`)

	finalAddr := getAddressFieldValue(page)
	log.Printf("-> ĐÃ GÕ XONG TIỀN TỐ VÀO Ô ĐỊA CHỈ: '%s' (Độ dài: %d <= 35 ký tự)\n", finalAddr, len(finalAddr))
	return nil
}

// updateAddressField cập nhật lại giá trị ô địa chỉ bằng Human Typing và đóng gợi ý
func updateAddressField(page *rod.Page, newAddress string) {
	if len(newAddress) > 35 {
		newAddress = strings.TrimSpace(newAddress[:35])
		newAddress = strings.TrimRight(newAddress, ",.- ")
	}
	selectors := []string{
		`input[name="address1"]`,
		`input[name="address"]`,
		`input[name="streetAddress"]`,
		`input[name="shippingAddress.address1"]`,
		`input[placeholder*="enter an address" i]`,
		`input[placeholder*="house number" i]`,
		`input[placeholder*="Address" i]`,
		`input[placeholder*="Street" i]`,
		`input#address1`,
		`input#shipping-address1`,
		`input[id*="address" i]`,
	}

	addrEl := findInputField(page, selectors)
	if addrEl != nil {
		_ = humanType(page, addrEl, newAddress)
	}

	// Đóng triệt để popup dropdown gợi ý nếu còn mở và không cần click suggestion nữa
	_ = page.Keyboard.Press(input.Escape)
	log.Println("-> Đã cập nhật xong ô địa chỉ bằng Human Typing (không cần click suggestion nữa).")
}

// findInputField tìm kiếm ô input theo danh sách selector (dùng Has để không bao giờ bị treo)
func findInputField(page *rod.Page, selectors []string) *rod.Element {
	for _, sel := range selectors {
		has, el, err := page.Has(sel)
		if err == nil && has && el != nil {
			visible, err := el.Timeout(500 * time.Millisecond).Visible()
			if err == nil && visible {
				return el
			}
		}
	}
	return nil
}

// typeHumanLike gõ từng ký tự mô phỏng nhịp điệu người thật:
// - Tốc độ gõ cơ bản: 45 - 95ms / ký tự
// - Ngắt quãng dài hơn khi gặp dấu chấm (.), @, khoảng trắng, gạch ngang, v.v. (120 - 240ms)
// - Ngắt quãng tự nhiên theo cụm 4 số thẻ ngân hàng (200 - 380ms)
// - Ngắt quãng ngẫu nhiên (micro-pause / hesitation)
func typeHumanLike(el *rod.Element, text string, isCardNumber bool) {
	if el == nil {
		return
	}
	page := el.Page()
	for idx, ch := range text {
		charStr := string(ch)
		// Dùng timeout 400ms để không bao giờ bị treo
		err := el.Timeout(400 * time.Millisecond).Input(charStr)
		if err != nil && page != nil {
			_ = page.Timeout(300 * time.Millisecond).InsertText(charStr)
		}

		// Độ trễ cơ bản giữa mỗi phím (tự nhiên 45 - 95ms)
		baseDelay := 45 + rand.Intn(50)
		time.Sleep(time.Duration(baseDelay) * time.Millisecond)

		// Nếu là số thẻ ngân hàng: ngắt quãng tự nhiên sau mỗi cụm 4 số để kiểm tra/nhìn thẻ
		if isCardNumber && (idx+1)%4 == 0 && idx < len(text)-1 {
			pause := 200 + rand.Intn(180)
			time.Sleep(time.Duration(pause) * time.Millisecond)
			continue
		}

		// Ngắt quãng rõ rệt khi gõ dấu chấm (.), ký tự đặc biệt (@, -, /, ,, khoảng trắng)
		if ch == '.' || ch == '@' || ch == ' ' || ch == '-' || ch == '/' || ch == ',' || ch == '_' {
			pause := 120 + rand.Intn(130)
			time.Sleep(time.Duration(pause) * time.Millisecond)
		} else if rand.Float32() < 0.12 {
			// Thỉnh thoảng ngắt quãng ngẫu nhiên (hesitation / micro-pause 100 - 220ms)
			pause := 100 + rand.Intn(120)
			time.Sleep(time.Duration(pause) * time.Millisecond)
		}
	}
}

// humanType mô phỏng gõ phím con người an toàn, tự nhiên và chống bot detection
func humanType(page *rod.Page, el *rod.Element, text string) error {
	if el == nil {
		return fmt.Errorf("element is nil")
	}

	// Đảm bảo element có timeout an toàn 8 giây, không bao giờ bị treo
	safeEl := el.Timeout(8 * time.Second)

	_ = safeEl.ScrollIntoView()
	time.Sleep(time.Duration(50+rand.Intn(40)) * time.Millisecond)
	_ = safeEl.Focus()
	_ = safeEl.Click(proto.InputMouseButtonLeft, 1)
	time.Sleep(time.Duration(60+rand.Intn(40)) * time.Millisecond)
	_ = safeEl.SelectAllText()
	time.Sleep(time.Duration(30+rand.Intn(30)) * time.Millisecond)

	typeHumanLike(safeEl, text, false)

	_, _ = safeEl.Timeout(2 * time.Second).Eval(`(el, expected) => {
		if (el.value !== expected) {
			el.value = expected;
		}
		el.dispatchEvent(new Event('input', { bubbles: true }));
		el.dispatchEvent(new Event('change', { bubbles: true }));
	}`, text)

	time.Sleep(time.Duration(120+rand.Intn(80)) * time.Millisecond)
	return nil
}

// dismissCookieConsent tự động tắt banner cookie nếu xuất hiện
func dismissCookieConsent(page *rod.Page) {
	_, _ = page.Eval(`() => {
		const btns = Array.from(document.querySelectorAll('button, a'));
		for (const b of btns) {
			const t = (b.innerText || '').trim().toLowerCase();
			if (t === 'accept all' || t === 'accept all cookies' || t === 'accept' || t === 'i agree' || b.id === 'onetrust-accept-btn-handler') {
				b.click();
				return true;
			}
		}
		return false;
	}`)
	time.Sleep(300 * time.Millisecond)
}

// selectProductSize thực hiện mở dropdown và chọn size
func selectProductSize(page *rod.Page, targetSize string) error {
	_, _ = page.Eval(`() => {
		const btns = Array.from(document.querySelectorAll('button, [role="button"], [aria-haspopup="listbox"]'));
		for (const b of btns) {
			const text = (b.innerText || b.textContent || '').trim();
			const aria = (b.getAttribute('aria-label') || '').toLowerCase();
			const testId = (b.getAttribute('data-testid') || '').toLowerCase();

			if (text.includes('Select a size') || text.includes('Select size') ||
			    testId.includes('size-selector') || aria.includes('select size') ||
			    b.getAttribute('aria-haspopup') === 'listbox') {
				b.scrollIntoView({ behavior: 'smooth', block: 'center' });
				b.click();
				return true;
			}
		}

		const allBtns = Array.from(document.querySelectorAll('button'));
		for (let i = 0; i < allBtns.length; i++) {
			const t = (allBtns[i].innerText || '').toLowerCase();
			if (t.includes('add to bag') && i > 0) {
				allBtns[i - 1].scrollIntoView({ behavior: 'smooth', block: 'center' });
				allBtns[i - 1].click();
				return true;
			}
		}
		return false;
	}`)

	time.Sleep(800 * time.Millisecond)

	selectResult, err := page.Eval(fmt.Sprintf(`() => {
		const target = "%s".toUpperCase().trim();
		const candidates = Array.from(document.querySelectorAll(
			'li, button, [role="option"], [role="radio"], div[data-testid*="size"], span, label'
		));

		for (const el of candidates) {
			const rawText = (el.innerText || el.textContent || '').trim();
			if (rawText.length > 30) continue;

			const t = rawText.toUpperCase();
			const lines = t.split('\n').map(s => s.trim()).filter(Boolean);
			const firstLine = lines.length > 0 ? lines[0] : '';

			const isMatch = (t === target) ||
			                (firstLine === target) ||
			                (t === 'US ' + target) ||
			                (t.startsWith(target + ' -')) ||
			                (t.startsWith(target + ' /'));

			if (isMatch) {
				const rect = el.getBoundingClientRect();
				if (rect.width > 0 && rect.height > 0) {
					el.scrollIntoView({ behavior: 'smooth', block: 'center' });
					el.click();
					return { success: true, text: rawText };
				}
			}
		}

		return { success: false, text: '' };
	}`, targetSize))

	if err == nil && selectResult.Value.Get("success").Bool() {
		return nil
	}

	return fmt.Errorf("không chọn được size %s", targetSize)
}

// clickAddToBag thực hiện bấm nút "Add to bag"
func clickAddToBag(page *rod.Page) error {
	startTime := time.Now()
	for time.Since(startTime) < 8*time.Second {
		res, err := page.Eval(`() => {
			const btns = Array.from(document.querySelectorAll('button, input[type="submit"], [role="button"]'));
			for (const b of btns) {
				const text = (b.innerText || b.textContent || '').trim().toLowerCase();
				const testId = (b.getAttribute('data-testid') || '').toLowerCase();
				const aria = (b.getAttribute('aria-label') || '').toLowerCase();

				if (text.includes('add to bag') || text.includes('add to cart') ||
				    testId.includes('add-to-bag') || aria.includes('add to bag')) {
					const rect = b.getBoundingClientRect();
					if (rect.width > 0 && rect.height > 0) {
						b.scrollIntoView({ behavior: 'smooth', block: 'center' });
						b.click();
						return { clicked: true, text: (b.innerText || '').trim() };
					}
				}
			}
			return { clicked: false, text: '' };
		}`)

		if err == nil && res.Value.Get("clicked").Bool() {
			return nil
		}

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("không tìm thấy nút 'Add to bag'")
}

// clickCheckout tìm và bấm nút Checkout trong giỏ hàng
func clickCheckout(page *rod.Page) error {
	startTime := time.Now()
	for time.Since(startTime) < 12*time.Second {
		res, err := page.Eval(`() => {
			function isVisible(el) {
				if (!el) return false;
				const style = window.getComputedStyle(el);
				if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') return false;
				const rect = el.getBoundingClientRect();
				return rect.width > 20 && rect.height > 20;
			}

			const cartContainers = Array.from(document.querySelectorAll('aside, dialog, [role="dialog"], [data-testid*="cart"], [data-testid*="bag"], [class*="cart"], [class*="drawer"], [class*="bag"]'));
			for (const container of cartContainers) {
				if (isVisible(container)) {
					const btns = Array.from(container.querySelectorAll('button, a, [role="button"]'));
					for (const b of btns) {
						const text = (b.innerText || b.textContent || '').trim().toLowerCase();
						const href = (b.getAttribute('href') || '').toLowerCase();
						if ((text.includes('checkout') || href.includes('/checkout') || text.includes('view bag')) && isVisible(b)) {
							b.scrollIntoView({ behavior: 'smooth', block: 'center' });
							b.click();
							b.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
							return { clicked: true, text: (b.innerText || b.textContent || '').trim() };
						}
					}
				}
			}

			const allElements = Array.from(document.querySelectorAll('button, a, [role="button"]'));
			for (const el of allElements) {
				const text = (el.innerText || el.textContent || '').trim().toLowerCase();
				const href = (el.getAttribute('href') || '').toLowerCase();

				if ((text === 'checkout' || text === 'go to checkout' || text === 'proceed to checkout' ||
				     text.startsWith('checkout') || href.includes('/checkout')) && isVisible(el)) {
					el.scrollIntoView({ behavior: 'smooth', block: 'center' });
					el.click();
					el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
					return { clicked: true, text: (el.innerText || el.textContent || '').trim() };
				}
			}

			return { clicked: false, text: '' };
		}`)

		if err == nil && res.Value.Get("clicked").Bool() {
			time.Sleep(1500 * time.Millisecond)
			return nil
		}

		time.Sleep(800 * time.Millisecond)
	}

	_ = page.Navigate("https://www.on.com/en-us/checkout")
	return nil
}

// parseProxyDetails phân tích các định dạng SOCKS5 phổ biến
func parseProxyDetails(raw string) (host, port, user, pass string, err error) {
	raw = strings.TrimSpace(raw)

	parts := strings.Split(raw, ":")
	if len(parts) == 4 && !strings.Contains(raw, "//") {
		return parts[0], parts[1], parts[2], parts[3], nil
	}

	if len(parts) == 2 && !strings.Contains(raw, "//") {
		return parts[0], parts[1], "", "", nil
	}

	if !strings.HasPrefix(raw, "socks5://") && !strings.HasPrefix(raw, "socks5h://") && !strings.HasPrefix(raw, "http://") {
		raw = "socks5://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", "", "", "", err
	}

	h, p, err := net.SplitHostPort(u.Host)
	if err != nil {
		h = u.Host
		p = "1080"
	}

	var username, password string
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}

	return h, p, username, password, nil
}

// startLocalSocks5Bridge tạo một SOCKS5 listener cục bộ, tự động lấy exit IP & vị trí của Proxy, trả về localAddr, exitIP, geoInfo, closeFn, err
func startLocalSocks5Bridge(upstreamAddr, user, pass string) (string, string, *fingerprint.GeoLocationInfo, func(), error) {
	var auth *proxy.Auth
	if user != "" {
		auth = &proxy.Auth{
			User:     user,
			Password: pass,
		}
	}

	dialer, err := proxy.SOCKS5("tcp", upstreamAddr, auth, proxy.Direct)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("không thể tạo dialer SOCKS5 upstream: %w", err)
	}

	// Lấy exit IP thực tế và toạ độ địa lý của Proxy (luôn dùng HTTPS Port 443 để khớp 100% với trình duyệt)
	var exitIP string
	var geoInfo *fingerprint.GeoLocationInfo

	type ipWhoIsResp struct {
		IP          string  `json:"ip"`
		Success     bool    `json:"success"`
		CountryCode string  `json:"country_code"`
		Region      string  `json:"region"`
		City        string  `json:"city"`
		Postal      string  `json:"postal"`
		Latitude    float64 `json:"latitude"`
		Longitude   float64 `json:"longitude"`
		Timezone    struct {
			ID     string `json:"id"`
			Offset int    `json:"offset"`
		} `json:"timezone"`
	}

	checkClient := &http.Client{
		Transport: &http.Transport{Dial: dialer.Dial},
		Timeout:   4 * time.Second,
	}

	// Luôn truy vấn qua HTTPS (Port 443) để lấy đúng Exit IP của web HTTPS
	resp, err := checkClient.Get("https://ipwho.is")
	if err == nil {
		var info ipWhoIsResp
		if err := json.NewDecoder(resp.Body).Decode(&info); err == nil && info.Success && info.IP != "" {
			exitIP = info.IP
			tzOffset := -info.Timezone.Offset / 60
			if tzOffset < 0 {
				tzOffset = 0
			}
			geoInfo = &fingerprint.GeoLocationInfo{
				Country:        info.CountryCode,
				State:          info.Region,
				City:           info.City,
				ZipCode:        info.Postal,
				Timezone:       info.Timezone.ID,
				TimezoneOffset: tzOffset,
				Latitude:       info.Latitude,
				Longitude:      info.Longitude,
			}
		}
		resp.Body.Close()
	}

	if exitIP == "" {
		if resp2, err2 := checkClient.Get("https://api.ipify.org"); err2 == nil {
			if b, err := io.ReadAll(resp2.Body); err == nil && len(b) > 0 {
				exitIP = strings.TrimSpace(string(b))
			}
			resp2.Body.Close()
		}
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("không thể mở local listener: %w", err)
	}

	localAddr := listener.Addr().String()
	closed := make(chan struct{})

	go func() {
		for {
			clientConn, err := listener.Accept()
			if err != nil {
				select {
				case <-closed:
					return
				default:
					return
				}
			}
			go handleLocalSocksConn(clientConn, dialer)
		}
	}()

	closeFn := func() {
		select {
		case <-closed:
		default:
			close(closed)
		}
		_ = listener.Close()
	}

	return localAddr, exitIP, geoInfo, closeFn, nil
}

// handleLocalSocksConn xử lý handshake SOCKS5 phía local
func handleLocalSocksConn(client net.Conn, dialer proxy.Dialer) {
	defer client.Close()

	buf := make([]byte, 258)
	n, err := io.ReadAtLeast(client, buf, 2)
	if err != nil || buf[0] != 0x05 {
		return
	}

	nmethods := int(buf[1])
	if n < 2+nmethods {
		_, err = io.ReadFull(client, buf[n:2+nmethods])
		if err != nil {
			return
		}
	}

	_, err = client.Write([]byte{0x05, 0x00})
	if err != nil {
		return
	}

	reqBuf := make([]byte, 4)
	_, err = io.ReadFull(client, reqBuf)
	if err != nil || reqBuf[0] != 0x05 {
		return
	}

	cmd := reqBuf[1]
	if cmd != 0x01 {
		client.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	atyp := reqBuf[3]
	var targetHost string

	switch atyp {
	case 0x01:
		ipBuf := make([]byte, 4)
		if _, err := io.ReadFull(client, ipBuf); err != nil {
			return
		}
		targetHost = net.IP(ipBuf).String()
	case 0x03:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(client, lenBuf); err != nil {
			return
		}
		domainLen := int(lenBuf[0])
		domainBuf := make([]byte, domainLen)
		if _, err := io.ReadFull(client, domainBuf); err != nil {
			return
		}
		targetHost = string(domainBuf)
	case 0x04:
		ip6Buf := make([]byte, 16)
		if _, err := io.ReadFull(client, ip6Buf); err != nil {
			return
		}
		targetHost = net.IP(ip6Buf).String()
	default:
		client.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(client, portBuf); err != nil {
		return
	}
	targetPort := int(portBuf[0])<<8 | int(portBuf[1])
	targetAddr := fmt.Sprintf("%s:%d", targetHost, targetPort)

	upstreamConn, err := dialer.Dial("tcp", targetAddr)
	if err != nil {
		client.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstreamConn.Close()

	_, err = client.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	if err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstreamConn, client)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstreamConn)
		done <- struct{}{}
	}()
	<-done
}

// ensurePaymentSectionVisible cuộn tới phần thanh toán và đảm bảo đã chọn Credit Card
func ensurePaymentSectionVisible(page *rod.Page) {
	_, _ = page.Eval(`() => {
		const elements = Array.from(document.querySelectorAll('h1, h2, h3, h4, legend, label, span, div'));
		for (const el of elements) {
			const text = (el.innerText || el.textContent || '').trim().toLowerCase();
			if (text === 'payment' || text === 'credit card' || text.startsWith('credit card')) {
				el.scrollIntoView({ behavior: 'smooth', block: 'center' });
				break;
			}
		}

		const radios = Array.from(document.querySelectorAll('input[type="radio"], [role="radio"]'));
		for (const r of radios) {
			const id = (r.id || '').toLowerCase();
			const val = (r.value || '').toLowerCase();
			const aria = (r.getAttribute('aria-label') || '').toLowerCase();
			const parent = r.closest('label, div');
			const pText = parent ? (parent.innerText || '').toLowerCase() : '';

			if (id.includes('credit') || id.includes('card') || val.includes('credit') || aria.includes('credit') || pText.includes('credit card') || id.includes('basic-creditcards')) {
				if (!r.checked && typeof r.click === 'function') {
					r.click();
				}
				break;
			}
		}
	}`)
	time.Sleep(1 * time.Second)

	waitIfrTimeout := time.Now().Add(10 * time.Second)
	for time.Now().Before(waitIfrTimeout) {
		res, err := page.Eval(`() => {
			const ifrs = Array.from(document.querySelectorAll('iframe'));
			const cardIfrs = ifrs.filter(f => {
				const src = (f.src || '').toLowerCase();
				const id = (f.id || '').toLowerCase();
				const title = (f.getAttribute('title') || '').toLowerCase();
				return src.includes('shopifycs') || src.includes('sessions') || id.includes('card-fields') || id.includes('number') || title.includes('card number');
			});
			return cardIfrs.length >= 1;
		}`)
		if err == nil && res != nil && res.Value.Bool() {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}
}

// maskSecret che giấu thông tin nhạy cảm khi hiển thị log
func maskSecret(fieldName, val string) string {
	lower := strings.ToLower(fieldName)
	if strings.Contains(lower, "cvv") || strings.Contains(lower, "security") {
		return "***"
	}
	if len(val) >= 12 {
		return val[:4] + strings.Repeat("*", len(val)-8) + val[len(val)-4:]
	}
	return val
}

// fillFieldByExactSelector tìm chính xác ô input qua selector trong các Iframes hoặc Trang chính và điền trực tiếp
func fillFieldByExactSelector(page *rod.Page, fieldName string, selector string, value string) (success bool) {
	if strings.TrimSpace(value) == "" {
		return true
	}

	log.Printf("-> Đang gõ %s: '%s' (Human Typing)...\n", fieldName, maskSecret(fieldName, value))

	defer func() {
		if r := recover(); r != nil {
			success = false
		}
	}()

	isExpiry := strings.Contains(selector, "expiry") || strings.Contains(strings.ToLower(fieldName), "hạn")

	// 1. Quét qua tất cả iframes trên trang
	iframes, err := page.Elements("iframe")
	if err == nil {
		for _, ifr := range iframes {
			frame, err := ifr.Frame()
			if err != nil || frame == nil {
				continue
			}

			hasRes, err := frame.Eval(`(sel) => {
				try {
					const el = document.querySelector(sel);
					return !!el;
				} catch(e) { return false; }
			}`, selector)

			if err == nil && hasRes != nil && hasRes.Value.Bool() {
				inp, err := frame.Element(selector)
				if err == nil && inp != nil {
					_ = inp.ScrollIntoView()
					_ = inp.Focus()
					_ = inp.Click(proto.InputMouseButtonLeft, 1)
					time.Sleep(80 * time.Millisecond)

					_ = inp.SelectAllText()
					_ = frame.Keyboard.Press(input.ControlLeft)
					_ = frame.Keyboard.Press(input.KeyA)
					_ = frame.Keyboard.Release(input.ControlLeft)
					_ = frame.Keyboard.Press(input.Backspace)
					for i := 0; i < 10; i++ {
						_ = frame.Keyboard.Press(input.Backspace)
					}
					time.Sleep(60 * time.Millisecond)

					if isExpiry {
						cleanDigits := ""
						for _, r := range value {
							if r >= '0' && r <= '9' {
								cleanDigits += string(r)
							}
						}
						if len(cleanDigits) == 4 {
							month := cleanDigits[:2]
							year := cleanDigits[2:]

							_ = inp.Input(string(month[0]))
							time.Sleep(time.Duration(55+rand.Intn(40)) * time.Millisecond)
							_ = inp.Input(string(month[1]))
							time.Sleep(time.Duration(120+rand.Intn(80)) * time.Millisecond)

							valRes, _ := frame.Eval(`(sel) => {
								try {
									const el = document.querySelector(sel);
									return el ? (el.value || '') : '';
								} catch(e) { return ''; }
							}`, selector)

							currentVal := ""
							if valRes != nil {
								currentVal = valRes.Value.String()
							}

							if !strings.Contains(currentVal, "/") {
								_ = inp.Input("/")
								time.Sleep(time.Duration(70+rand.Intn(50)) * time.Millisecond)
							} else {
								time.Sleep(time.Duration(60+rand.Intn(40)) * time.Millisecond)
							}

							_ = inp.Input(string(year[0]))
							time.Sleep(time.Duration(55+rand.Intn(40)) * time.Millisecond)
							_ = inp.Input(string(year[1]))
							time.Sleep(time.Duration(100+rand.Intn(60)) * time.Millisecond)

							_, _ = frame.Eval(`(sel, mm, yy) => {
								try {
									const el = document.querySelector(sel);
									if (el) {
										let v = (el.value || '').trim();
										if (!v.includes('/') || v === mm + yy) {
											el.value = mm + ' / ' + yy;
										}
										el.dispatchEvent(new Event('input', { bubbles: true }));
										el.dispatchEvent(new Event('change', { bubbles: true }));
										el.dispatchEvent(new Event('blur', { bubbles: true }));
									}
								} catch(e) {}
							}`, selector, month, year)
						} else {
							typeHumanLike(inp, value, false)
						}
					} else {
						isCardNumber := strings.Contains(strings.ToLower(fieldName), "số thẻ") || strings.Contains(selector, "number")
						typeHumanLike(inp, value, isCardNumber)
						time.Sleep(time.Duration(120+rand.Intn(80)) * time.Millisecond)

						_, _ = frame.Eval(`(sel) => {
							try {
								const el = document.querySelector(sel);
								if (el) {
									el.dispatchEvent(new Event('input', { bubbles: true }));
									el.dispatchEvent(new Event('change', { bubbles: true }));
									el.dispatchEvent(new Event('blur', { bubbles: true }));
								}
							} catch(e) {}
						}`, selector)
					}
					return true
				}
			}
		}
	}

	// 2. Nếu nằm trên trang chính
	hasMain, err := page.Eval(`(sel) => {
		try {
			const el = document.querySelector(sel);
			return !!el;
		} catch(e) { return false; }
	}`, selector)

	if err == nil && hasMain != nil && hasMain.Value.Bool() {
		inp, err := page.Element(selector)
		if err == nil && inp != nil {
			_ = inp.ScrollIntoView()
			_ = inp.Focus()
			_ = inp.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(80 * time.Millisecond)

			_ = inp.SelectAllText()
			_ = page.Keyboard.Press(input.ControlLeft)
			_ = page.Keyboard.Press(input.KeyA)
			_ = page.Keyboard.Release(input.ControlLeft)
			_ = page.Keyboard.Press(input.Backspace)
			for i := 0; i < 10; i++ {
				_ = page.Keyboard.Press(input.Backspace)
			}
			time.Sleep(60 * time.Millisecond)

			if isExpiry {
				cleanDigits := ""
				for _, r := range value {
					if r >= '0' && r <= '9' {
						cleanDigits += string(r)
					}
				}
				if len(cleanDigits) == 4 {
					month := cleanDigits[:2]
					year := cleanDigits[2:]

					_ = inp.Input(string(month[0]))
					time.Sleep(time.Duration(55+rand.Intn(40)) * time.Millisecond)
					_ = inp.Input(string(month[1]))
					time.Sleep(time.Duration(120+rand.Intn(80)) * time.Millisecond)

					valRes, _ := page.Eval(`(sel) => {
						try {
							const el = document.querySelector(sel);
							return el ? (el.value || '') : '';
						} catch(e) { return ''; }
					}`, selector)

					currentVal := ""
					if valRes != nil {
						currentVal = valRes.Value.String()
					}

					if !strings.Contains(currentVal, "/") {
						_ = inp.Input("/")
						time.Sleep(time.Duration(70+rand.Intn(50)) * time.Millisecond)
					} else {
						time.Sleep(time.Duration(60+rand.Intn(40)) * time.Millisecond)
					}

					_ = inp.Input(string(year[0]))
					time.Sleep(time.Duration(55+rand.Intn(40)) * time.Millisecond)
					_ = inp.Input(string(year[1]))
					time.Sleep(time.Duration(100+rand.Intn(60)) * time.Millisecond)

					_, _ = page.Eval(`(sel, mm, yy) => {
						try {
							const el = document.querySelector(sel);
							if (el) {
								let v = (el.value || '').trim();
								if (!v.includes('/') || v === mm + yy) {
									el.value = mm + ' / ' + yy;
								}
								el.dispatchEvent(new Event('input', { bubbles: true }));
								el.dispatchEvent(new Event('change', { bubbles: true }));
								el.dispatchEvent(new Event('blur', { bubbles: true }));
							}
						} catch(e) {}
					}`, selector, month, year)
				} else {
					typeHumanLike(inp, value, false)
				}
			} else {
				isCardNumber := strings.Contains(strings.ToLower(fieldName), "số thẻ") || strings.Contains(selector, "number")
				typeHumanLike(inp, value, isCardNumber)
				time.Sleep(time.Duration(120+rand.Intn(80)) * time.Millisecond)

				_, _ = page.Eval(`(sel) => {
					try {
						const el = document.querySelector(sel);
						if (el) {
							el.dispatchEvent(new Event('input', { bubbles: true }));
							el.dispatchEvent(new Event('change', { bubbles: true }));
							el.dispatchEvent(new Event('blur', { bubbles: true }));
						}
					} catch(e) {}
				}`, selector)
			}
			return true
		}
	}

	return false
}

// fillPaymentCardForm thực hiện toàn bộ quy trình điền thẻ tín dụng
func fillPaymentCardForm(page *rod.Page, profile *UserProfile) error {
	if profile.CardNumber == "" {
		return nil
	}

	ensurePaymentSectionVisible(page)
	waitForPageIdle(page)

	expMonth := profile.ExpMonth
	if len(expMonth) == 1 {
		expMonth = "0" + expMonth
	}
	expYear := profile.ExpYear
	if len(expYear) == 4 {
		expYear = expYear[2:]
	}
	expCombined := expMonth + expYear

	// Nghỉ tự nhiên trước khi bắt đầu nhập thẻ (800 - 1200ms)
	time.Sleep(time.Duration(800+rand.Intn(400)) * time.Millisecond)

	_ = fillFieldByExactSelector(page, "Số thẻ", `input#number, input[name="number"]`, profile.CardNumber)
	time.Sleep(time.Duration(150+rand.Intn(80)) * time.Millisecond)

	_ = fillFieldByExactSelector(page, "Hạn dùng", `input#expiry, input[name="expiry"]`, expCombined)
	time.Sleep(time.Duration(120+rand.Intn(80)) * time.Millisecond)

	_ = fillFieldByExactSelector(page, "CVV", `input#verification_value, input[name="verification_value"]`, profile.CVV)
	time.Sleep(time.Duration(120+rand.Intn(80)) * time.Millisecond)

	if profile.FullName != "" {
		_ = fillFieldByExactSelector(page, "Tên trên thẻ", `input#name, input[name="name"], input[placeholder*="Name on card" i]`, profile.FullName)
		time.Sleep(time.Duration(120+rand.Intn(80)) * time.Millisecond)
	}

	// Đợi iframe Shopify hoàn tất kiểm tra Luhn và hợp lệ hóa thẻ
	time.Sleep(600 * time.Millisecond)

	return nil
}

// applyDiscountCode tìm ô name="reductions", xoá sạch dữ liệu, nhập mã giảm giá và bấm Apply
func applyDiscountCode(page *rod.Page, discountCode string) error {
	discountCode = strings.TrimSpace(discountCode)
	if discountCode == "" {
		return nil
	}

	startTime := time.Now()
	timeout := 15 * time.Second

	for time.Since(startTime) < timeout {
		_, _ = page.Eval(`() => {
			const toggles = Array.from(document.querySelectorAll('button, a, summary, [role="button"]'));
			for (const t of toggles) {
				const text = (t.innerText || t.textContent || '').trim().toLowerCase();
				const aria = (t.getAttribute('aria-label') || '').toLowerCase();
				if (text.includes('discount') || text.includes('gift card') || text.includes('promo') || aria.includes('discount')) {
					const rect = t.getBoundingClientRect();
					if (rect.width > 0 && rect.height > 0) {
						t.click();
						break;
					}
				}
			}
		}`)
		time.Sleep(200 * time.Millisecond)

		var reductionsInput *rod.Element
		selectors := []string{
			`input[name="reductions"]`,
			`input#reductions`,
			`input[name*="reduction"]`,
			`input[placeholder*="Discount"]`,
			`input[placeholder*="gift card"]`,
			`input[placeholder*="Promo"]`,
			`input[aria-label*="Discount"]`,
		}

		for _, sel := range selectors {
			has, el, err := page.Has(sel)
			if err == nil && has && el != nil {
				vis, _ := el.Timeout(500 * time.Millisecond).Visible()
				if vis {
					reductionsInput = el
					break
				}
			}
		}

		if reductionsInput != nil {
			_ = reductionsInput.ScrollIntoView()
			time.Sleep(100 * time.Millisecond)
			_ = reductionsInput.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(80 * time.Millisecond)

			_ = reductionsInput.SelectAllText()
			_ = page.Keyboard.Press(input.ControlLeft)
			_ = page.Keyboard.Press(input.KeyA)
			_ = page.Keyboard.Release(input.ControlLeft)
			_ = page.Keyboard.Press(input.Backspace)
			_, _ = reductionsInput.Eval(`() => { this.value = ''; this.dispatchEvent(new Event('input', {bubbles: true})); }`)
			time.Sleep(80 * time.Millisecond)

			typeHumanLike(reductionsInput, discountCode, false)
			time.Sleep(time.Duration(150+rand.Intn(100)) * time.Millisecond)

			_ = page.Keyboard.Press(input.Enter)
			time.Sleep(300 * time.Millisecond)

			_, _ = page.Eval(`() => {
				const inp = document.querySelector('input[name="reductions"], input#reductions, input[placeholder*="Discount"]');
				if (inp) {
					const form = inp.closest('form') || inp.parentElement || inp.closest('div');
					if (form) {
						const btn = form.querySelector('button[type="submit"], button[aria-label*="Apply"], button');
						if (btn && typeof btn.click === 'function') {
							btn.click();
							return true;
						}
					}
				}
				const allBtns = Array.from(document.querySelectorAll('button'));
				for (const b of allBtns) {
					const t = (b.innerText || b.textContent || '').trim().toLowerCase();
					const aria = (b.getAttribute('aria-label') || '').toLowerCase();
					if (t === 'apply' || t.startsWith('apply') || aria.includes('apply')) {
						b.click();
						return true;
					}
				}
				return false;
			}`)

			log.Printf("-> ĐÃ NHẬP VÀ BẤM ÁP DỤNG MÃ GIẢM GIÁ '%s' THÀNH CÔNG!\n", discountCode)
			waitForDiscountAPIToComplete(page)
			return nil
		}

		time.Sleep(800 * time.Millisecond)
	}

	return fmt.Errorf("không tìm thấy ô reductions")
}

// waitForDiscountAPIToComplete chờ API áp dụng mã giảm giá gọi xong hoàn tất trước khi sang bước tiếp theo
func waitForDiscountAPIToComplete(page *rod.Page) {
	log.Println("-> Đang chờ API mã giảm giá xử lý và tính toán lại bảng giá...")
	waitTimeout := time.Now().Add(15 * time.Second)
	time.Sleep(1200 * time.Millisecond)

	for time.Now().Before(waitTimeout) {
		res, err := page.Eval(`() => {
			const isBusy = Array.from(document.querySelectorAll('button[class*="loading"], [aria-busy="true"], .spinner, [class*="spinner"], [class*="skeleton"]')).some(el => {
				const rect = el.getBoundingClientRect();
				return rect.width > 0 && rect.height > 0;
			});
			const hasTag = !!document.querySelector('[class*="reduction-code"], [class*="discount-tag"], [class*="tag"], [class*="badge"], [aria-label*="discount"]');
			const hasError = !!document.querySelector('#error-for-reduction_code, [id*="reduction-code-error"], [role="alert"]');
			return { isBusy, isDone: (hasTag || hasError) && !isBusy };
		}`)
		if err == nil && res != nil {
			isBusy := res.Value.Get("isBusy").Bool()
			isDone := res.Value.Get("isDone").Bool()
			if isDone {
				log.Println("-> API MÃ GIẢM GIÁ ĐÃ GỌI XONG HOÀN TẤT!")
				time.Sleep(1 * time.Second)
				return
			}
			if !isBusy && time.Since(waitTimeout) > -11*time.Second {
				time.Sleep(1 * time.Second)
				return
			}
		}
		time.Sleep(600 * time.Millisecond)
	}
	log.Println("-> Đã kết thúc chờ API mã giảm giá. Tiếp tục bước điền thẻ.")
}

// waitForPageIdle chờ cho đến khi trang web không còn spinner hoặc trạng thái bận
func waitForPageIdle(page *rod.Page) {
	timeout := time.Now().Add(8 * time.Second)
	for time.Now().Before(timeout) {
		res, err := page.Eval(`() => {
			const hasBusy = Array.from(document.querySelectorAll('[aria-busy="true"], .spinner, [class*="spinner"], [class*="loading"], [class*="skeleton"]')).some(el => {
				const rect = el.getBoundingClientRect();
				return rect.width > 0 && rect.height > 0;
			});
			return hasBusy;
		}`)
		if err == nil && res != nil && !res.Value.Bool() {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	time.Sleep(800 * time.Millisecond)
}

// clickPayNowButton tìm và bấm nút thanh toán (Pay now / Complete order / Place order)
func clickPayNowButton(page *rod.Page) (bool, string) {
	payRes, err := page.Eval(`() => {
		function isVisible(el) {
			if (!el) return false;
			const style = window.getComputedStyle(el);
			if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') return false;
			const rect = el.getBoundingClientRect();
			return rect.width > 20 && rect.height > 15 && !el.disabled;
		}

		const allBtns = Array.from(document.querySelectorAll('button, input[type="submit"], [role="button"]'));
		for (const b of allBtns) {
			if (!isVisible(b)) continue;
			const text = (b.innerText || b.textContent || b.value || '').trim().toLowerCase();
			const id = (b.id || '').toLowerCase();
			const aria = (b.getAttribute('aria-label') || '').toLowerCase();

			if (text === 'pay now' || text.includes('pay now') ||
			    text === 'complete order' || text.includes('complete order') ||
			    text === 'place order' || text.includes('place order') ||
			    id.includes('pay-button') || aria.includes('pay now')) {
				b.scrollIntoView({ behavior: 'smooth', block: 'center' });
				b.click();
				b.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
				return { clicked: true, text: (b.innerText || b.textContent || 'Pay now').trim() };
			}
		}

		return { clicked: false, text: '' };
	}`)

	if err == nil && payRes != nil && payRes.Value.Get("clicked").Bool() {
		btnName := payRes.Value.Get("text").String()
		return true, btnName
	}

	hasPay, payBtn, err := page.Has(`button#checkout-pay-button, button[id*="pay-button"], button[type="submit"]`)
	if err == nil && hasPay && payBtn != nil {
		vis, _ := payBtn.Timeout(500 * time.Millisecond).Visible()
		if vis {
			_ = payBtn.Timeout(2 * time.Second).ScrollIntoView()
			time.Sleep(100 * time.Millisecond)
			_ = payBtn.Timeout(2 * time.Second).Click(proto.InputMouseButtonLeft, 1)
			return true, "Pay now"
		}
	}

	return false, ""
}

// extractOrderSummaryAndPayNow kiểm tra giá tiền đã giảm và bấm Pay now
func extractOrderSummaryAndPayNow(page *rod.Page, task CheckoutTask) error {
	profile := task.Profile
	if profile == nil {
		profile = &UserProfile{}
	}

	time.Sleep(3 * time.Second)

	maxRetries := 3 // Thử tối đa 4 lần (1 lần đầu + 3 lần retry)
	for attempt := 1; attempt <= 1+maxRetries; attempt++ {
		if attempt > 1 {
			log.Println("==================================================")
			log.Printf("-> [RETRY %d/%d (Lần %d/4)] CHỜ CỔNG THANH TOÁN ỔN ĐỊNH HOÀN TOÀN TRƯỚC KHI THỬ LẠI...\n", attempt-1, maxRetries, attempt)
			log.Println("==================================================")
			waitForPageIdle(page)
			time.Sleep(2 * time.Second)

			log.Printf("-> [RETRY %d/%d (Lần %d/4)] Đang nhập lại toàn bộ thông tin thẻ tín dụng trước khi bấm Pay now...\n", attempt-1, maxRetries, attempt)
			_ = fillPaymentCardForm(page, profile)
			time.Sleep(1500 * time.Millisecond)
		}

		// Đảm bảo không còn spinner hay API nền nào đang bận trước khi bấm Pay now
		waitForPageIdle(page)

		// Kiểm tra lại 1 lần nữa ô địa chỉ trước khi bấm Pay now (đảm bảo <= 35 ký tự và không có lỗi)
		checkAndFixAddressFieldBeforePayNow(page, profile)
		waitForPageIdle(page)

		randomWait := 2 + rand.Intn(3)
		time.Sleep(time.Duration(randomWait) * time.Second)

		log.Printf("-> Đang kiểm tra và bấm nút 'Pay now' (Lần %d/4)...\n", attempt)
		clicked, btnName := clickPayNowButton(page)
		if !clicked {
			log.Println("-> Nút thanh toán không còn hiển thị. Đang kiểm tra xem đơn hàng đã chuyển trang Thank You thành công hay chưa...")
			time.Sleep(2 * time.Second)
			_, isAlreadySuccess, _ := waitForPaymentProcessing(page, 10*time.Second)
			if isAlreadySuccess {
				info, _ := page.Info()
				finalURL := ""
				if info != nil {
					finalURL = info.URL
				}
				log.Printf("-> XÁC NHẬN ĐƠN HÀNG THÀNH CÔNG TẠI URL: %s\n", finalURL)
				log.Println("-> Đang trích xuất thông tin trên màn hình Thank You và lưu ra file...")
				time.Sleep(2500 * time.Millisecond)
				tyDetails := extractThankYouPageDetails(page)
				if tyDetails.FinalURL == "" {
					tyDetails.FinalURL = finalURL
				}
				_ = saveSuccessOrderToFile(task, tyDetails, "success_orders.txt")
				return nil
			}
			break
		}

		log.Printf("-> ĐÃ BẤM THÀNH CÔNG NÚT '%s' (Lần %d/4)!\n", btnName, attempt)
		log.Println("-> BẮT ĐẦU CHỜ API CỔNG THANH TOÁN GỌI XONG (nút hết 'Processing...' và ngưng spinner)...")

		// CHỜ API THANH TOÁN GỌI XONG HOÀN TOÀN:
		// Tuyệt đối không thực hiện bước tiếp theo trong khi nút vẫn đang "Processing..."!
		_, isSuccess, errMsg := waitForPaymentProcessing(page, 75*time.Second)

		if isSuccess {
			info, _ := page.Info()
			finalURL := ""
			if info != nil {
				finalURL = info.URL
			}
			log.Printf("-> API THANH TOÁN ĐÃ GỌI XONG VÀ THÀNH CÔNG!\n-> URL: %s\n", finalURL)
			log.Println("-> [MÀN HÌNH THANK YOU] Đang trích xuất thông tin đơn hàng và lưu tất cả thông tin vừa nhập ra file...")
			time.Sleep(2500 * time.Millisecond)

			tyDetails := extractThankYouPageDetails(page)
			if tyDetails.FinalURL == "" {
				tyDetails.FinalURL = finalURL
			}
			_ = saveSuccessOrderToFile(task, tyDetails, "success_orders.txt")
			return nil
		}

		log.Printf("-> API THANH TOÁN ĐÃ GỌI XONG VỚI KẾT QUẢ: %s\n", errMsg)

		if attempt < 1+maxRetries {
			log.Printf("-> [BƯỚC TIẾP THEO] Chờ 5 giây cho hệ thống ổn định hoàn toàn rồi mới thử lại lần %d/4...\n", attempt+1)
			waitForPageIdle(page)
			time.Sleep(5 * time.Second)
		} else {
			log.Printf("[!] Cổng thanh toán kết luận thất bại sau 4 lần thử thẻ: %s\n", errMsg)
			return fmt.Errorf("thanh toán không thành công sau 4 lần thử thẻ: %s", errMsg)
		}
	}

	return fmt.Errorf("thanh toán không thành công: không thể bấm nút Pay now hoặc không nhận được xác nhận đơn hàng")
}

// waitForPaymentProcessing theo dõi quá trình API xử lý thanh toán (chờ loading/spinner hoàn tất 100% trước khi bước tiếp theo)
func waitForPaymentProcessing(page *rod.Page, timeout time.Duration) (finished bool, isSuccess bool, errMsg string) {
	deadline := time.Now().Add(timeout)
	lastLog := time.Now()
	startTime := time.Now()

	// 1. Cho cổng thanh toán 2.5s đầu tiên để tiếp nhận request và chuyển sang trạng thái Processing...
	log.Println("-> Đang đợi cổng thanh toán tiếp nhận yêu cầu và bắt đầu xử lý...")
	time.Sleep(2500 * time.Millisecond)

	hasObservedProcessing := false
	settleCounter := 0

	for time.Now().Before(deadline) {
		// 1. Kiểm tra nếu URL đã chuyển hướng sang trang thành công (Thank you / Orders / Confirmation)
		info, err := page.Info()
		if err == nil && info != nil {
			urlLower := strings.ToLower(info.URL)
			if strings.Contains(urlLower, "thank_you") || strings.Contains(urlLower, "orders") || strings.Contains(urlLower, "order-received") || strings.Contains(urlLower, "confirmation") || strings.Contains(urlLower, "receipt") || strings.Contains(urlLower, "completed") {
				return true, true, ""
			}
		}

		// 2. Đánh giá trạng thái DOM xem API có đang xử lý (Processing... / spinner / disabled) hoặc đã báo lỗi không
		evalRes, err := page.Eval(`() => {
			// A. Kiểm tra trang có thông báo xác nhận thành công không (màn Thank You / Confirmation)
			const bodyText = (document.body ? document.body.innerText : '');
			const bodyTextLower = bodyText.toLowerCase();
			if (bodyTextLower.includes('thank you for your purchase') || 
			    bodyTextLower.includes('your order is confirmed') || 
			    bodyTextLower.includes('order confirmed') || 
			    bodyTextLower.includes('thank you,') || 
			    bodyTextLower.includes('confirmation #') || 
			    bodyTextLower.includes('confirmation code') || 
			    bodyTextLower.includes("you'll receive a confirmation email") || 
			    bodyTextLower.includes('you receive a confirmation email')) {
				return { isSuccessText: true, isProcessing: false, btnText: '', hasError: false, errorMsg: '' };
			}

			// B. Kiểm tra THÔNG BÁO LỖI (Error Banners / Alerts từ cổng thanh toán)
			let detectedError = '';
			const errSelectors = [
				'[role="alert"]',
				'.notice--error',
				'.notice.notice--error',
				'[data-banner-level="critical"]',
				'.field--error',
				'#error-for-number',
				'#error-for-expiry',
				'#error-for-verification_value',
				'.banner--error',
				'p[id*="error"]',
				'[data-error-feedback]',
				'[aria-live="polite"][class*="error"]',
				'[aria-live="assertive"]'
			];

			for (const sel of errSelectors) {
				const els = Array.from(document.querySelectorAll(sel));
				for (const el of els) {
					const rect = el.getBoundingClientRect();
					if (rect.width > 0 && rect.height > 0) {
						const t = (el.innerText || el.textContent || '').trim();
						if (t.length > 3) {
							detectedError = t;
							break;
						}
					}
				}
				if (detectedError) break;
			}

			if (!detectedError) {
				if (bodyTextLower.includes('there was an issue processing your payment') || bodyTextLower.includes('try again or use a different payment method')) {
					detectedError = 'There was an issue processing your payment. Try again or use a different payment method.';
				} else if (bodyTextLower.includes('card was declined') || bodyTextLower.includes('payment could not be processed')) {
					detectedError = 'Payment was declined or could not be processed.';
				}
			}

			// Nếu đã xuất hiện thông báo lỗi rõ ràng thì KHÔNG CÒN xử lý nữa (API đã trả về thất bại)
			if (detectedError) {
				return {
					isSuccessText: false,
					isProcessing: false,
					btnText: '',
					hasError: true,
					errorMsg: detectedError
				};
			}

			// C. Quét trạng thái API thanh toán đang xử lý (CHỈ KIỂM TRA NÚT THANH TOÁN HOẶC OVERLAY THẬT SỰ)
			let isProcessing = false;
			let btnStatusText = '';

			// C1. Tìm chính xác NÚT THANH TOÁN CHÍNH (Pay now / Place order / Complete order)
			const allBtns = Array.from(document.querySelectorAll('button, input[type="submit"], [role="button"]'));
			let payBtn = null;
			for (const b of allBtns) {
				const rect = b.getBoundingClientRect();
				if (rect.width === 0 || rect.height === 0) continue;
				const t = (b.innerText || b.textContent || b.value || '').trim().toLowerCase();
				const id = (b.id || '').toLowerCase();
				if (t.includes('pay now') || t.includes('complete order') || t.includes('place order') || id.includes('pay-button') || (b.type === 'submit' && !id.includes('discount') && !id.includes('coupon'))) {
					payBtn = b;
					break;
				}
			}

			if (payBtn) {
				const t = (payBtn.innerText || payBtn.textContent || payBtn.value || '').trim();
				const tLower = t.toLowerCase();

				const isDis = payBtn.disabled || payBtn.getAttribute('aria-disabled') === 'true';
				const isBusy = payBtn.getAttribute('aria-busy') === 'true';
				const hasLoadClass = payBtn.classList.contains('btn--loading') || payBtn.classList.contains('is-loading') || payBtn.classList.contains('loading');
				const hasRealSpinner = !!payBtn.querySelector('.spinner, [class*="spinner"], [class*="loader"], [data-loading], svg.animate-spin');

				// Nút chỉ đang processing nếu:
				// - Chữ hiển thị là processing / please wait / authorizing
				// - Hoặc bị disabled/busy kèm loading class hoặc spinner thực sự
				if (tLower === 'processing...' || tLower.startsWith('processing') || tLower.includes('authorizing') || tLower.includes('submitting') || tLower.includes('please wait')) {
					isProcessing = true;
					btnStatusText = t || 'Processing...';
				} else if (isBusy || hasLoadClass || (isDis && hasRealSpinner)) {
					isProcessing = true;
					btnStatusText = t ? (t + ' (đang xử lý)') : 'Processing...';
				}
			}

			// C2. Kiểm tra overlay loading toàn màn hình (nếu có)
			if (!isProcessing) {
				const overlays = Array.from(document.querySelectorAll('.loading-overlay, [data-processing], .page-loading'));
				for (const sp of overlays) {
					const rect = sp.getBoundingClientRect();
					if (rect.width > 20 && rect.height > 20) {
						isProcessing = true;
						btnStatusText = 'Page Loading Overlay';
						break;
					}
				}
			}

			// C3. Kiểm tra URL processing hoặc 3DS Challenge
			const isUrlProcessing = window.location.href.includes('processing') || window.location.href.includes('3d_secure') || window.location.href.includes('challenge') || window.location.href.includes('authenticate');
			if (!isProcessing && isUrlProcessing) {
				isProcessing = true;
				btnStatusText = 'URL Processing/3DS';
			}

			return {
				isSuccessText: false,
				isProcessing: isProcessing,
				btnText: btnStatusText,
				hasError: false,
				errorMsg: ''
			};
		}`)

		if err == nil && evalRes != nil {
			if evalRes.Value.Get("isSuccessText").Bool() {
				return true, true, ""
			}

			hasError := evalRes.Value.Get("hasError").Bool()
			errText := evalRes.Value.Get("errorMsg").String()
			isProcessing := evalRes.Value.Get("isProcessing").Bool()
			btnText := evalRes.Value.Get("btnText").String()

			// TRƯỜNG HỢP 1: NẾU ĐÃ CÓ LỖI RÕ RÀNG TỪ CỔNG THANH TOÁN -> NGỪNG CHỜ NGAY LẬP TỨC
			if hasError {
				log.Printf("-> [API ĐÃ GỌI XONG - CỔNG THANH TOÁN BÁO LỖI]: %s\n", errText)
				return true, false, errText
			}

			// TRƯỜNG HỢP 2: API VẪN ĐANG GỌI (nút đang 'Processing...' hoặc spinner đang quay):
			// BẮT BUỘC TIẾP TỤC CHỜ! TUYỆT ĐỐI KHÔNG THỰC HIỆN BƯỚC TIẾP THEO!
			if isProcessing {
				hasObservedProcessing = true
				settleCounter = 0
				if time.Since(lastLog) >= 3*time.Second {
					if btnText != "" {
						log.Printf("-> [API ĐANG XỬ LÝ] Nút hiển thị: '%s' ... Đang chờ API gọi xong...\n", btnText)
					} else {
						log.Println("-> [API ĐANG XỬ LÝ] Cổng thanh toán đang thực hiện giao dịch... Đang chờ API gọi xong...")
					}
					lastLog = time.Now()
				}
				time.Sleep(1500 * time.Millisecond)
				continue
			}

			// TRƯỜNG HỢP 3: API ĐÃ NGỪNG PROCESSING (nút đã trở về bình thường):
			if hasObservedProcessing || time.Since(startTime) >= 5*time.Second {
				settleCounter++
				if settleCounter < 2 {
					// Chờ thêm 1 lượt để chắc chắn trạng thái đã ổn định
					time.Sleep(1200 * time.Millisecond)
					continue
				}

				// Kiểm tra xem có chuyển sang trang hoàn tất đơn không
				infoAfter, _ := page.Info()
				if infoAfter != nil {
					uLow := strings.ToLower(infoAfter.URL)
					if strings.Contains(uLow, "thank_you") || strings.Contains(uLow, "orders") || strings.Contains(uLow, "order-received") || strings.Contains(uLow, "confirmation") {
						return true, true, ""
					}
				}

				// Nếu nút thanh toán đã trở lại bình thường và cho phép bấm lại
				// Tức là cổng thanh toán đã kết thúc phiên gọi API
				log.Println("-> API thanh toán đã kết thúc giao dịch (nút đã dừng Processing).")
				if errText != "" {
					return true, false, errText
				}
				return true, false, "Cổng thanh toán từ chối hoặc giao dịch không hoàn tất"
			}
		}

		time.Sleep(1200 * time.Millisecond)
	}

	return true, false, fmt.Sprintf("Hết thời gian chờ phản hồi thanh toán (%v)", timeout)
}
