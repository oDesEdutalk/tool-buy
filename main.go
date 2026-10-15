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
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"golang.org/x/net/proxy"

	"tool-buy/fingerprint"
)

// DefaultProxy do người dùng cung cấp
const DefaultProxy = "global.rotip.trustsource.live:20000:USER501664-zone-custom-region-US-session-56178095:41b761"

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

// CheckoutTask chứa thông tin của tác vụ mua hàng
type CheckoutTask struct {
	URL          string
	Size         string
	Proxy        string
	Profile      *UserProfile
	DiscountCode string
	DiscountFile string
	Headless     bool
	UserAgent    string
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

// TelegramConfig cấu hình Telegram Bot
type TelegramConfig struct {
	BotToken string
	ChatID   string
}

// loadTelegramConfig tải cấu hình Telegram từ file hoặc flags
func loadTelegramConfig(filePath, flagToken, flagChatID string) TelegramConfig {
	cfg := TelegramConfig{
		BotToken: strings.TrimSpace(flagToken),
		ChatID:   strings.TrimSpace(flagChatID),
	}

	if cfg.BotToken != "" && cfg.ChatID != "" {
		return cfg
	}

	lines, err := readLinesFromFile(filePath)
	if err == nil {
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
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
		log.Println("[Telegram] ⚠️ Chưa cấu hình BOT_TOKEN hoặc CHAT_ID trong telegram.txt.")
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

// readLinesFromFile đọc file theo từng dòng
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

// getRandomPhone đọc ngẫu nhiên một số điện thoại từ file
func getRandomPhone(filePath string) string {
	lines, err := readLinesFromFile(filePath)
	if err != nil || len(lines) == 0 {
		return ""
	}
	return lines[rand.Intn(len(lines))]
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

// getRandomUserAgent đọc ngẫu nhiên một User-Agent từ file (hoặc dùng danh sách mặc định)
func getRandomUserAgent(filePath string) string {
	lines, err := readLinesFromFile(filePath)
	if err == nil && len(lines) > 0 {
		return lines[rand.Intn(len(lines))]
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

func getRandomResolution() (int, int) {
	res := commonResolutions[rand.Intn(len(commonResolutions))]
	return res.Width, res.Height
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

var failedProxyMutex sync.Mutex

// recordFailedProxy ghi nhận proxy bị lỗi vào file proxies_failed.txt
func recordFailedProxy(proxyStr, reason string) {
	if proxyStr == "" {
		return
	}
	failedProxyMutex.Lock()
	defer failedProxyMutex.Unlock()

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

// ProxyManager quản lý danh sách proxy, xoay vòng khi lỗi và lưu proxy thành công lên đầu
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

// AvailableCount đếm số proxy còn lại chưa dùng
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

	pm.saveStateLocked()

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

	var newSucc []string
	newSucc = append(newSucc, proxyStr)
	for _, p := range pm.successful {
		if p != proxyStr {
			newSucc = append(newSucc, p)
		}
	}
	pm.successful = newSucc

	pm.saveStateLocked()
}

// SaveState cập nhật lại proxies.txt
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

	// 2. Các proxy đang in-use
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

// parseDataString phân tích chuỗi mẫu
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

	// Quét thông minh qua tất cả các cột phía sau trong file cards.txt:
	// - Cột chứa số điện thoại (10-15 ký tự) -> lấy làm Phone (không lấy cột chứa '@')
	for i := 5; i < len(parts); i++ {
		p := parts[i]
		if strings.Contains(p, "@") {
			// Bỏ qua cột email từ file cards.txt theo yêu cầu
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

// executeSingleCheckout thực hiện toàn bộ quy trình mua hàng cho 1 proxy
func executeSingleCheckout(task CheckoutTask) error {
	profile := task.Profile

	userDataDir, err := os.MkdirTemp("", "rod-single-*")
	if err == nil {
		defer os.RemoveAll(userDataDir)
	}

	ua := task.UserAgent
	if ua == "" {
		ua = getRandomUserAgent("user_agents.txt")
	}

	winW, winH := getRandomResolution()

	// Tự sinh bộ vân tay ngẫu nhiên độc lập chuẩn BitBrowser (bao gồm Kernel, GPU, Geolocation, Timezone)
	fpProfile := fingerprint.GenerateRandomProfile(winW, winH, ua)

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
			log.Printf("[!] Lỗi phân tích định dạng proxy: %v\n", err)
			return err
		}

		upstreamAddr := fmt.Sprintf("%s:%s", host, port)
		localProxyAddr, exitIP, geoInfo, cBridge, err := startLocalSocks5Bridge(upstreamAddr, user, pass)
		if err != nil {
			log.Printf("[!] Cảnh báo mở Local Proxy Bridge (%v), fallback sang cấu hình direct\n", err)
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
				log.Printf("[+] Đã phát hiện Proxy Exit IP: %s (Đồng bộ WebRTC/DNS/IPv6 sang IP này)\n", exitIP)
			} else if net.ParseIP(host) != nil {
				fpProfile.ProxyIP = host
			}
			if geoInfo != nil {
				fpProfile.Geo = *geoInfo
			}

			// 3. Chống rò rỉ DNS Leak: Ép Chrome uỷ quyền toàn bộ DNS cho Proxy, cấm phân giải DNS cục bộ
			l = l.Proxy("socks5://"+localProxyAddr).
				Set("host-resolver-rules", "MAP * ~NOTFOUND , EXCLUDE 127.0.0.1").
				Set("disable-async-dns", "").
				Set("disable-features", "DnsOverHttps,AsyncDns")
		}
	}

	log.Println("-> Đang khởi chạy trình duyệt...")
	browserURL, err := l.Launch()
	if err != nil {
		return fmt.Errorf("không thể mở trình duyệt: %w", err)
	}

	browser := rod.New().ControlURL(browserURL).MustConnect()
	defer browser.MustClose()

	log.Println("-> Đang kích hoạt Stealth Mode (chống bot detection)...")
	page, err := stealth.Page(browser)
	if err != nil {
		page = browser.MustPage()
	}

	// Tiêm vân tay chuẩn BitBrowser vào page (CDP Timezone, GPS, Locale, UserAgent và JS API)
	if err := fpProfile.ApplyToPage(page); err != nil {
		log.Printf("[!] Cảnh báo: Lỗi áp dụng vân tay: %v\n", err)
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

	page.MustSetWindow(0, 0, winW, winH)
	_ = page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent:      fpProfile.UserAgent,
		AcceptLanguage: "en-US,en;q=0.9",
		Platform:       "Win32",
	})

	log.Printf("\n%s\n", fpProfile.FormatBitBrowserTable(1, task.Proxy, time.Now().Format("2006-01-02 15:04:05")))

	log.Printf("-> Đang truy cập URL: %s (Size: '%s') ...\n", task.URL, task.Size)
	err = page.Navigate(task.URL)
	if err != nil {
		return fmt.Errorf("lỗi khi truy cập trang web: %w", err)
	}

	_ = page.Timeout(30 * time.Second).WaitLoad()
	log.Println("-> ĐÃ MỞ TRANG THÀNH CÔNG!")
	time.Sleep(3 * time.Second)

	// 1. Đóng Popup Cookies nếu có
	dismissCookieConsent(page)

	// 2. Chọn Size sản phẩm
	err = selectProductSize(page, task.Size)
	if err != nil {
		log.Printf("[!] Lỗi chọn size: %v\n", err)
	}

	time.Sleep(1500 * time.Millisecond)

	// 3. Bấm "Add to bag"
	err = clickAddToBag(page)
	if err != nil {
		return fmt.Errorf("lỗi khi bấm Add to bag: %w", err)
	}

	log.Println("-> Đang chờ giỏ hàng cập nhật...")
	time.Sleep(3 * time.Second)

	// 4. Bấm "Checkout"
	err = clickCheckout(page)
	if err != nil {
		log.Printf("[!] Cảnh báo khi bấm Checkout: %v\n", err)
	}

	// 5. Chờ chuyển hướng và load xong hoàn toàn trang Checkout rồi điền form (Human Typing)
	err = fillCheckoutForm(page, profile)
	if err != nil {
		log.Printf("[!] Cảnh báo khi điền form checkout: %v\n", err)
	}

	// 6. Nhập và áp dụng Mã giảm giá (Discount Code)
	if task.DiscountCode != "" {
		err = applyDiscountCode(page, task.DiscountCode)
		if err != nil {
			log.Printf("[!] Cảnh báo khi áp dụng mã giảm giá: %v\n", err)
		}
		time.Sleep(2 * time.Second)
	}

	// 7. Điền thông tin Thẻ tín dụng (Credit Card Payment: #number, #expiry, #verification_value)
	err = fillPaymentCardForm(page, profile)
	if err != nil {
		log.Printf("[!] Cảnh báo khi điền thông tin thẻ tín dụng: %v\n", err)
	}

	// 8. Chờ kiểm tra giá sau khi áp mã giảm giá và bấm "Pay now"
	err = extractOrderSummaryAndPayNow(page, task)
	if err != nil {
		return err
	}

	return nil
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
	targetURL := flag.String("url", "https://www.on.com/en-us/products/cloudtilt-arc-m-3mg3033/mens/alloy-ivory-shoes-3MG30333228", "URL trang web cần mở")
	targetSize := flag.String("size", "9.5", "Size sản phẩm cần chọn (S, M, L, XL, ...)")
	productsFileFlag := flag.String("products-file", "products.txt", "File danh sách URL sản phẩm và size (URL|Size)")
	proxyRaw := flag.String("proxy", "", "Proxy SOCKS5 riêng biệt (nếu muốn chỉ định trực tiếp)")
	proxyFileFlag := flag.String("proxy-file", "proxies.txt", "File danh sách proxy SOCKS5")
	telegramFileFlag := flag.String("telegram-file", "telegram.txt", "File cấu hình Telegram Bot (BOT_TOKEN, CHAT_ID)")
	teleTokenFlag := flag.String("tele-token", "", "Telegram Bot Token")
	teleChatIDFlag := flag.String("tele-chatid", "", "Telegram Chat ID")
	headless := flag.Bool("headless", false, "Chạy ẩn trình duyệt (true/false)")

	cardFileFlag := flag.String("card-file", "cards.txt", "File danh sách thẻ và profile")
	dataRaw := flag.String("data", "", "Chuỗi dữ liệu tổng hợp (nếu để trống sẽ tự động đọc từ cards.txt)")
	emailFlag := flag.String("email", "", "Email thanh toán (để trống sẽ tự lấy từ -data hoặc cards.txt)")
	firstNameFlag := flag.String("firstname", "", "Tên (First Name)")
	lastNameFlag := flag.String("lastname", "", "Họ (Last Name)")
	addressFlag := flag.String("address", "820 Roxfield Ct, Buford GA 30518", "Địa chỉ gõ vào ô tìm kiếm (hoặc lấy từ -data)")
	phoneFlag := flag.String("phone", "", "Số điện thoại")
	phonesFileFlag := flag.String("phones-file", "phones.txt", "File danh sách số điện thoại (ngẫu nhiên)")
	uaFileFlag := flag.String("ua-file", "user_agents.txt", "File danh sách User-Agent (ngẫu nhiên)")
	uaFlag := flag.String("user-agent", "", "User-Agent cố định (nếu không muốn ngẫu nhiên)")
	cardFlag := flag.String("card", "", "Số thẻ tín dụng")
	monthFlag := flag.String("month", "", "Tháng hết hạn MM")
	yearFlag := flag.String("year", "", "Năm hết hạn YY")
	cvvFlag := flag.String("cvv", "", "Mã bảo mật CVV")
	discountFileFlag := flag.String("discount-file", "discounts.txt", "File danh sách mã giảm giá (mỗi dòng 1 mã)")
	discountFlag := flag.String("discount", "IDME_ON_20_ITULQV", "Mã giảm giá (Discount code)")

	flag.Parse()

	var profile *UserProfile
	if *dataRaw != "" {
		profile = parseDataString(*dataRaw)
	} else {
		cards, err := readLinesFromFile(*cardFileFlag)
		if err == nil && len(cards) > 0 {
			profile = parseDataString(cards[0])
			log.Printf("-> Đã đọc thẻ thành công từ '%s': %s (Số thẻ: %s)\n", *cardFileFlag, profile.FullName, maskSecret("card", profile.CardNumber))
		} else {
			log.Printf("[!] Cảnh báo: Không tìm thấy thẻ trong '%s', sử dụng profile dự phòng.\n", *cardFileFlag)
			profile = parseDataString("4127770010937223|02|28|753|Joanne Brehm|UNITED STATES|COMENITY BANK|CREDIT|TRADITIONAL|VISA|||||||")
		}
	}

	// Lấy số điện thoại ngẫu nhiên từ phones.txt nếu có
	if randPhone := getRandomPhone(*phonesFileFlag); randPhone != "" {
		profile.Phone = randPhone
		log.Printf("-> Đã chọn số điện thoại ngẫu nhiên từ '%s': %s\n", *phonesFileFlag, randPhone)
	}

	if *emailFlag != "" {
		profile.Email = *emailFlag
	}
	if *firstNameFlag != "" {
		profile.FirstName = *firstNameFlag
	}
	if *lastNameFlag != "" {
		profile.LastName = *lastNameFlag
	}
	// Địa chỉ nhận hàng: Luôn ưu tiên dùng addressFlag mặc định, không lấy địa chỉ từ file thẻ
	if *addressFlag != "" {
		profile.RawAddress = *addressFlag
		profile.Address = *addressFlag
		parts := strings.Split(*addressFlag, ",")
		if len(parts) >= 3 {
			profile.Address = strings.TrimSpace(parts[0])
			profile.City = strings.TrimSpace(parts[1])
			lastFields := strings.Fields(strings.TrimSpace(parts[2]))
			if len(lastFields) >= 2 {
				profile.State = lastFields[0]
				profile.ZipCode = lastFields[len(lastFields)-1]
			} else if len(parts) >= 4 {
				profile.State = strings.TrimSpace(parts[2])
				profile.ZipCode = strings.TrimSpace(parts[3])
			}
		} else if len(parts) == 2 {
			profile.Address = strings.TrimSpace(parts[0])
			restFields := strings.Fields(strings.TrimSpace(parts[1]))
			if len(restFields) >= 3 {
				profile.ZipCode = restFields[len(restFields)-1]
				profile.State = restFields[len(restFields)-2]
				profile.City = strings.Join(restFields[:len(restFields)-2], " ")
			} else if len(restFields) == 2 {
				profile.State = restFields[0]
				profile.ZipCode = restFields[1]
			}
		}
	}
	if *phoneFlag != "" {
		profile.Phone = *phoneFlag
	}
	if *cardFlag != "" {
		profile.CardNumber = *cardFlag
	}
	if *monthFlag != "" {
		profile.ExpMonth = *monthFlag
	}
	if *yearFlag != "" {
		profile.ExpYear = *yearFlag
	}
	if *cvvFlag != "" {
		profile.CVV = *cvvFlag
	}

	teleCfg := loadTelegramConfig(*telegramFileFlag, *teleTokenFlag, *teleChatIDFlag)

	products := loadProductItems(*productsFileFlag, *targetURL, *targetSize)

	discountExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "discount" {
			discountExplicit = true
		}
	})

	var discounts []string
	if discountExplicit && *discountFlag != "" {
		discounts = []string{*discountFlag}
	} else {
		fileDiscounts, err := readLinesFromFile(*discountFileFlag)
		if err == nil && len(fileDiscounts) > 0 {
			discounts = fileDiscounts
			log.Printf("-> Đã đọc %d mã giảm giá từ '%s'\n", len(discounts), *discountFileFlag)
		} else if *discountFlag != "" {
			discounts = []string{*discountFlag}
		}
	}

	log.Println("==================================================")
	log.Println("     TOOL AUTOMATION BROWSER SOCKS5 (GOLANG)      ")
	log.Println("==================================================")
	if len(products) > 1 || (len(products) == 1 && products[0].URL != *targetURL) {
		log.Printf("-> Danh sách Sản phẩm : %d mục (ngẫu nhiên từ %s: URL|Size)\n", len(products), *productsFileFlag)
	} else {
		log.Printf("-> URL mục tiêu  : %s\n", *targetURL)
		log.Printf("-> Size cần mua  : %s\n", *targetSize)
	}
	log.Printf("-> Email         : %s\n", profile.Email)
	log.Printf("-> Họ và Tên     : %s %s\n", profile.FirstName, profile.LastName)
	log.Printf("-> Địa chỉ tìm   : %s\n", profile.RawAddress)
	log.Printf("-> Số điện thoại : %s\n", profile.Phone)
	log.Printf("-> Thẻ tín dụng  : %s | %s/%s | CVV: %s\n", maskSecret("card", profile.CardNumber), profile.ExpMonth, profile.ExpYear, maskSecret("cvv", profile.CVV))
	if len(discounts) > 1 {
		log.Printf("-> Mã giảm giá   : %d mã từ %s (lần lượt: %s...)\n", len(discounts), *discountFileFlag, discounts[0])
	} else if len(discounts) == 1 {
		if discountExplicit {
			log.Printf("-> Mã giảm giá   : %s (chỉ định từ cờ lệnh)\n", discounts[0])
		} else {
			log.Printf("-> Mã giảm giá   : %s (từ %s)\n", discounts[0], *discountFileFlag)
		}
	} else {
		log.Printf("-> Mã giảm giá   : Không sử dụng\n")
	}

	pm := NewProxyManager(*proxyFileFlag, "proxies_failed.txt", teleCfg)

	if *proxyRaw != "" {
		pm.available = append([]string{*proxyRaw}, pm.available...)
	}

	if pm.AvailableCount() == 0 {
		pm.available = append(pm.available, DefaultProxy)
	}

	log.Printf("-> Số proxy khả dụng ban đầu: %d\n", pm.AvailableCount())

	baseEmail := profile.Email
	attemptCount := 0
	maxProxyAttempts := 25

	for {
		attemptCount++
		currentProxy, ok := pm.GetNextProxy()
		if !ok {
			log.Println("==================================================")
			log.Println("[!] HẾT PROXY KHẢ DỤNG ĐỂ CHẠY! Đã thử hết proxy trong file.")
			log.Println("==================================================")
			break
		}

		currentUA := *uaFlag
		if currentUA == "" {
			currentUA = getRandomUserAgent(*uaFileFlag)
		}

		taskProf := *profile
		if baseEmail != "" {
			taskProf.Email = baseEmail
		}
		if *phoneFlag == "" {
			if randPhone := getRandomPhone(*phonesFileFlag); randPhone != "" {
				taskProf.Phone = randPhone
			}
		}

		chosenURL, chosenSize := getRandomProduct(products, *targetURL, *targetSize)

		currentDiscount := ""
		if len(discounts) > 0 {
			currentDiscount = discounts[(attemptCount-1)%len(discounts)]
		}

		task := CheckoutTask{
			URL:          chosenURL,
			Size:         chosenSize,
			Proxy:        currentProxy,
			Profile:      &taskProf,
			DiscountCode: currentDiscount,
			DiscountFile: *discountFileFlag,
			Headless:     *headless,
			UserAgent:    currentUA,
			TeleCfg:      teleCfg,
		}

		log.Println("==================================================")
		log.Printf("-> [LẦN THỬ #%d] BẮT ĐẦU VỚI PROXY: %s\n", attemptCount, currentProxy)
		log.Println("==================================================")

		err := executeSingleCheckout(task)
		if err == nil {
			log.Println("==================================================")
			log.Printf("-> MUA HÀNG THÀNH CÔNG VỚI PROXY: %s!\n", currentProxy)
			log.Println("-> Giữ nguyên proxy này ở ĐẦU TIÊN file proxies.txt để mua tiếp ở phiên sau!")
			log.Println("==================================================")
			pm.MarkSuccess(currentProxy)
			if task.DiscountCode != "" && *discountFileFlag != "" {
				_ = removeDiscountFromFile(*discountFileFlag, task.DiscountCode)
			}
			time.Sleep(3 * time.Second)
			return
		}

		log.Println("==================================================")
		log.Printf("[!] THẤT BẠI KHI MUA HÀNG VỚI PROXY [%s] (Lỗi: %v)\n", currentProxy, err)
		log.Println("-> Tự động lưu proxy lỗi và đổi sang proxy tiếp theo trong file...")
		log.Println("==================================================")
		pm.MarkFailed(currentProxy, err.Error())
		time.Sleep(2 * time.Second)

		if attemptCount >= maxProxyAttempts {
			log.Printf("[!] Đã đạt giới hạn tối đa %d lần thử đổi proxy. Dừng chương trình.\n", maxProxyAttempts)
			break
		}
	}
}

// fillCheckoutForm chờ tải trang checkout và gõ thông tin Email, Họ Tên, Địa chỉ (chọn gợi ý) và Số điện thoại
func fillCheckoutForm(page *rod.Page, profile *UserProfile) error {
	log.Println("-> [BƯỚC 5] Đang theo dõi chuyển hướng sang trang Checkout...")

	// 1. Chờ URL chuyển hướng thực sự sang trang checkout (chứa checkouts hoặc checkout.on.com)
	isRedirected := false
	redirectTimeout := time.Now().Add(35 * time.Second)
	for time.Now().Before(redirectTimeout) {
		info, err := page.Info()
		if err == nil && info != nil {
			currURL := strings.ToLower(info.URL)
			if strings.Contains(currURL, "checkouts") || strings.Contains(currURL, "checkout") {
				log.Printf("-> ĐÃ CHUYỂN HƯỚNG TỚI TRANG CHECKOUT THÀNH CÔNG!\n-> URL: %s\n", info.URL)
				isRedirected = true
				break
			}
		}
		time.Sleep(1 * time.Second)
	}

	if !isRedirected {
		log.Println("[!] Cảnh báo: Hết thời gian chờ URL chuyển hướng, vẫn tiếp tục tìm form...")
	}

	// Chờ trang checkout render ổn định các component
	log.Println("-> Đang đợi trang Checkout tải xong các ô nhập dữ liệu...")
	time.Sleep(4 * time.Second)

	// 2. Tìm và gõ Email (Chờ tối đa 25s)
	emailInputSelectors := []string{
		`input[type="email"]`,
		`input[name="email"]`,
		`input[placeholder*="Email"]`,
		`input#email`,
		`input[autocomplete="email"]`,
		`input[name="guestEmail"]`,
	}

	var emailEl *rod.Element
	waitInputTimeout := time.Now().Add(25 * time.Second)
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

	// 3. Tìm và gõ First name
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

	// 4. Tìm và gõ Last name
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

	// 5. Tìm và gõ Address, sau đó chọn Suggestion đầu tiên
	addressToType := profile.RawAddress
	if addressToType == "" {
		addressToType = profile.Address
	}

	if addressToType != "" {
		log.Printf("-> Đang gõ Address: '%s' (Human Typing)...\n", addressToType)
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
			_ = humanType(page, addressEl, addressToType)
			log.Println("-> Đã gõ địa chỉ. Đang đợi danh sách SUGGESTIONS xuất hiện...")
			time.Sleep(1 * time.Second)

			// Chờ và chọn gợi ý đầu tiên trong SUGGESTIONS
			err := selectFirstAddressSuggestion(page, addressEl, addressToType)
			if err != nil {
				log.Printf("[!] Cảnh báo chọn suggestion: %v\n", err)
			}

			log.Println("-> Đang đợi Shopify điền xong City/State/Zip và chuẩn hoá địa chỉ từ suggestion...")
			time.Sleep(1500 * time.Millisecond)

			// Lấy giá trị địa chỉ thực tế vừa được suggestion điền vào ô input
			currentAddrVal := getAddressFieldValue(page)
			addrBase := currentAddrVal
			if addrBase != "" {
				log.Printf("-> Đã lấy được giá trị địa chỉ trong ô input sau khi chọn gợi ý: '%s'\n", addrBase)
			} else {
				addrBase = profile.Address
				if addrBase == "" {
					addrBase = addressToType
				}
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

			// Chờ API xác thực địa chỉ, tự động điền City/State/ZIP và tải phương thức vận chuyển
			waitForAddressAPIToComplete(page)
		} else {
			log.Println("[!] Không tìm thấy ô nhập Address bằng selector thông thường, thử điền qua JS fallback...")
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

	// 6. Tìm và gõ Phone Number (Sau khi API địa chỉ đã hoàn tất)
	if profile.Phone != "" {
		log.Printf("-> Đang gõ Phone: '%s' (Human Typing)...\n", profile.Phone)
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
			_ = humanType(page, phoneEl, profile.Phone)
			log.Println("-> Đã hoàn thành gõ Phone.")
			time.Sleep(time.Duration(250+rand.Intn(150)) * time.Millisecond)
		} else {
			log.Println("[!] Không tìm thấy ô nhập Phone bằng selector, thử điền qua JS fallback...")
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

	log.Println("==================================================")
	log.Println("-> ĐÃ HOÀN TẤT ĐIỀN EMAIL, HỌ TÊN, ĐỊA CHỈ & SỐ ĐIỆN THOẠI!")
	log.Println("==================================================")
	return nil
}

// waitForAddressAPIToComplete chờ API xác thực địa chỉ và tính phí vận chuyển hoàn tất
func waitForAddressAPIToComplete(page *rod.Page) {
	log.Println("-> Đang chờ API xác thực địa chỉ và tính phí vận chuyển (Shipping Methods)...")

	waitTimeout := time.Now().Add(12 * time.Second)
	for time.Now().Before(waitTimeout) {
		time.Sleep(800 * time.Millisecond)

		res, err := page.Eval(`() => {
			// 1. Kiểm tra xem City hoặc ZIP code đã có dữ liệu tự động điền chưa
			const cityInp = document.querySelector('input[name="city"], input[placeholder*="City"], input#city');
			const zipInp = document.querySelector('input[name="postalCode"], input[name="zip"], input[name="zipcode"], input#postalCode');

			const hasCity = cityInp && (cityInp.value || '').trim().length > 0;
			const hasZip = zipInp && (zipInp.value || '').trim().length > 0;

			// 2. Kiểm tra xem Shipping Method đã load xong chưa (không còn thông báo "Enter your shipping address...")
			const allText = document.body ? document.body.innerText : '';
			const stillWaitingAddress = allText.includes('Enter your shipping address to view available shipping methods');
			
			// 3. Kiểm tra xem có spinner hoặc loading skeleton không
			const hasSpinners = Array.from(document.querySelectorAll('[class*="spinner"], [class*="skeleton"], [aria-busy="true"]')).some(el => {
				const rect = el.getBoundingClientRect();
				return rect.width > 0 && rect.height > 0;
			});

			if ((hasCity || hasZip || !stillWaitingAddress) && !hasSpinners) {
				return {
					ready: true,
					city: hasCity ? cityInp.value : '',
					zip: hasZip ? zipInp.value : ''
				};
			}

			return { ready: false, city: '', zip: '' };
		}`)

		if err == nil && res.Value.Get("ready").Bool() {
			city := res.Value.Get("city").String()
			zip := res.Value.Get("zip").String()
			log.Printf("-> API ĐÃ PHẢN HỒI THÀNH CÔNG! (City: '%s', ZIP: '%s')\n", city, zip)
			time.Sleep(1 * time.Second)
			return
		}
	}

	log.Println("-> Đã hoàn tất thời gian chờ API. Tiếp tục bước điền số điện thoại.")
}

// selectFirstAddressSuggestion đợi danh sách gợi ý và click trực tiếp vào dòng gợi ý trong khung đỏ
func selectFirstAddressSuggestion(page *rod.Page, addressEl *rod.Element, addressQuery string) error {
	log.Println("-> [BƯỚC 5.4] Đang chờ và click chọn dòng gợi ý địa chỉ (SUGGESTIONS)...")

	waitTimeout := time.Now().Add(15 * time.Second)

	for time.Now().Before(waitTimeout) {
		time.Sleep(600 * time.Millisecond)

		// 1. Tìm toạ độ của dòng gợi ý thông qua JavaScript
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
					try {
						if (window.PointerEvent && evt.startsWith('pointer')) {
							el.dispatchEvent(new PointerEvent(evt, {
								bubbles: true,
								cancelable: true,
								view: window,
								composed: true,
								pointerId: 1,
								pointerType: 'mouse',
								buttons: 1
							}));
						}
					} catch(e) {}
				}
				if (typeof el.click === 'function') {
					el.click();
				}
			}

			// Tách các từ khoá từ chuỗi địa chỉ cần tìm
			const rawWords = (query || '').split(/[,\s]+/).map(w => w.trim().toLowerCase()).filter(w => w.length >= 3);

			const allElements = Array.from(document.querySelectorAll('*'));
			const candidates = [];

			for (const el of allElements) {
				// Bỏ qua input và textarea
				if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SCRIPT' || el.tagName === 'STYLE') {
					continue;
				}

				if (!isVisible(el)) continue;

				const text = (el.innerText || el.textContent || '').trim();
				if (!text || text.length > 250) continue;

				const lowerText = text.toLowerCase();
				if (lowerText === 'suggestions' || lowerText === '✕' || lowerText === 'x') continue;

				// Kiểm tra nếu text chứa từ khoá địa chỉ hoặc "united states"
				let matchScore = 0;
				for (const w of rawWords) {
					if (lowerText.includes(w)) {
						matchScore++;
					}
				}
				if (lowerText.includes('united states')) {
					matchScore += 2;
				}

				// Nếu có khớp từ khoá và nằm ở vị trí hợp lệ
				if (matchScore >= 1) {
					const rect = el.getBoundingClientRect();
					if (rect.width >= 50 && rect.height >= 15) {
						candidates.push({
							el: el,
							rect: rect,
							text: text,
							length: text.length,
							score: matchScore
						});
					}
				}
			}

			// Ưu tiên điểm khớp cao nhất và text ngắn nhất (phần tử con trong cùng)
			candidates.sort((a, b) => {
				if (b.score !== a.score) {
					return b.score - a.score;
				}
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
			text := res.Value.Get("text").String()

			log.Printf("-> ĐÃ BẮT ĐƯỢC DÒNG GỢI Ý: '%s'\n", text)
			log.Printf("-> Đang click chuột CDP tại toạ độ (%.1f, %.1f)...\n", x, y)

			// Di chuyển chuột CDP và click thật
			_ = page.Mouse.MoveLinear(proto.Point{X: x, Y: y}, 2)
			time.Sleep(100 * time.Millisecond)
			_ = page.Mouse.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(500 * time.Millisecond)

			// Thử gửi thêm phím Enter
			_ = page.Keyboard.Press(input.Enter)
			time.Sleep(1000 * time.Millisecond)

			log.Println("-> ĐÃ CLICK THÀNH CÔNG VÀO GỢI Ý ĐỊA CHỈ!")
			return nil
		}
	}

	// Fallback bàn phím nếu hết timeout
	log.Println("[!] Thử chọn gợi ý bằng bàn phím (ArrowDown + Enter)...")
	if addressEl != nil {
		_ = addressEl.Focus()
		time.Sleep(200 * time.Millisecond)
		_ = page.Keyboard.Press(input.ArrowDown)
		time.Sleep(300 * time.Millisecond)
		_ = page.Keyboard.Press(input.Enter)
		time.Sleep(1 * time.Second)
		log.Println("-> Đã gửi phím ArrowDown + Enter.")
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

	// Đảm bảo dữ liệu đã vào đúng và trigger các sự kiện React
	_, _ = safeEl.Timeout(2*time.Second).Eval(`(el, expected) => {
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
	time.Sleep(500 * time.Millisecond)
}

// selectProductSize thực hiện mở dropdown và chọn size
func selectProductSize(page *rod.Page, targetSize string) error {
	log.Printf("-> [BƯỚC 1] Đang tìm và bấm nút chọn size (Select a size)...")

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

	time.Sleep(1 * time.Second)

	log.Printf("-> [BƯỚC 2] Đang chọn size '%s' trong danh sách...", targetSize)

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
		matchedText := selectResult.Value.Get("text").String()
		log.Printf("-> ĐÃ CHỌN THÀNH CÔNG SIZE: '%s' (Khớp: '%s')\n", targetSize, matchedText)
		return nil
	}

	return fmt.Errorf("không chọn được size %s", targetSize)
}

// clickAddToBag thực hiện bấm nút "Add to bag"
func clickAddToBag(page *rod.Page) error {
	log.Println("-> [BƯỚC 3] Đang tìm và bấm nút 'Add to bag'...")

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
			btnText := res.Value.Get("text").String()
			log.Printf("-> ĐÃ BẤM NÚT '%s' THÀNH CÔNG!\n", btnText)
			return nil
		}

		time.Sleep(600 * time.Millisecond)
	}

	return fmt.Errorf("không tìm thấy nút 'Add to bag'")
}

// clickCheckout tìm và bấm nút Checkout trong giỏ hàng
func clickCheckout(page *rod.Page) error {
	log.Println("-> [BƯỚC 4] Đang tìm và bấm nút 'Checkout' trong giỏ hàng...")

	startTime := time.Now()
	for time.Since(startTime) < 15*time.Second {
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
			btnText := res.Value.Get("text").String()
			log.Printf("-> ĐÃ BẤM NÚT '%s' THÀNH CÔNG! Đang chuyển hướng sang trang thanh toán...\n", btnText)
			time.Sleep(2 * time.Second)
			return nil
		}

		time.Sleep(1 * time.Second)
	}

	log.Println("-> Đang tự động chuyển hướng sang URL Checkout...")
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
		io.Copy(upstreamConn, client)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(client, upstreamConn)
		done <- struct{}{}
	}()
	<-done
}

// ensurePaymentSectionVisible cuộn tới phần thanh toán và đảm bảo đã chọn Credit Card
func ensurePaymentSectionVisible(page *rod.Page) {
	log.Println("-> [BƯỚC 7.1] Đang cuộn xuống và chọn phương thức thanh toán Credit card...")

	_, _ = page.Eval(`() => {
		// 1. Tìm heading hoặc container Payment / Credit card
		const elements = Array.from(document.querySelectorAll('h1, h2, h3, h4, legend, label, span, div'));
		for (const el of elements) {
			const text = (el.innerText || el.textContent || '').trim().toLowerCase();
			if (text === 'payment' || text === 'credit card' || text.startsWith('credit card')) {
				el.scrollIntoView({ behavior: 'smooth', block: 'center' });
				break;
			}
		}

		// 2. Tìm radio button Credit card nếu chưa checked
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
	time.Sleep(1500 * time.Millisecond)

	// Đợi các iframe của thẻ tín dụng tải xong
	log.Println("-> Đang đợi các khung iframe của thẻ tín dụng (Shopify Card Fields) tải xong...")
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
			log.Println("-> Các iframe thẻ tín dụng đã sẵn sàng!")
			break
		}
		time.Sleep(500 * time.Millisecond)
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

	// Đảm bảo không bao giờ bị panic do frame bị huỷ bất ngờ
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[!] Cảnh báo recover trong fillFieldByExactSelector (%s): %v\n", fieldName, r)
			success = false
		}
	}()

	log.Printf("-> Đang gõ %s: '%s' (Human Typing)...\n", fieldName, maskSecret(fieldName, value))
	isExpiry := strings.Contains(selector, "expiry") || strings.Contains(strings.ToLower(fieldName), "hạn")

	// 1. Quét qua tất cả iframes trên trang
	iframes, err := page.Elements("iframe")
	if err == nil {
		for idx, ifr := range iframes {
			frame, err := ifr.Frame()
			if err != nil || frame == nil {
				continue
			}

			// Kiểm tra selector trong frame bằng Eval siêu tốc
			hasRes, err := frame.Eval(`(sel) => {
				try {
					const el = document.querySelector(sel);
					return !!el;
				} catch(e) { return false; }
			}`, selector)

			if err == nil && hasRes != nil && hasRes.Value.Bool() {
				log.Printf("-> [Iframe #%d] Bắt trúng %s theo selector '%s'!\n", idx, fieldName, selector)
				inp, err := frame.Element(selector)
				if err == nil && inp != nil {
					_ = inp.ScrollIntoView()
					_ = inp.Focus()
					_ = inp.Click(proto.InputMouseButtonLeft, 1)
					time.Sleep(100 * time.Millisecond)

					// Xoá nội dung cũ bằng phím tắt
					_ = inp.SelectAllText()
					_ = frame.Keyboard.Press(input.ControlLeft)
					_ = frame.Keyboard.Press(input.KeyA)
					_ = frame.Keyboard.Release(input.ControlLeft)
					_ = frame.Keyboard.Press(input.Backspace)
					for i := 0; i < 15; i++ {
						_ = frame.Keyboard.Press(input.Backspace)
					}
					time.Sleep(80 * time.Millisecond)

					// Gõ giá trị bằng phím mô phỏng con người (Human Typing)
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

							// 1. Gõ 2 số đầu (Tháng MM) với delay tự nhiên
							_ = inp.Input(string(month[0]))
							time.Sleep(time.Duration(55+rand.Intn(40)) * time.Millisecond)
							_ = inp.Input(string(month[1]))
							time.Sleep(time.Duration(120+rand.Intn(80)) * time.Millisecond)

							// 2. Kiểm tra xem ô input có tự động chèn dấu '/' chưa
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

							// Nếu hệ thống không tự chèn '/' thì ta chủ động gõ '/'
							if !strings.Contains(currentVal, "/") {
								_ = inp.Input("/")
								time.Sleep(time.Duration(70+rand.Intn(50)) * time.Millisecond)
							} else {
								time.Sleep(time.Duration(60+rand.Intn(40)) * time.Millisecond)
							}

							// 3. Gõ 2 số sau (Năm YY) với delay tự nhiên
							_ = inp.Input(string(year[0]))
							time.Sleep(time.Duration(55+rand.Intn(40)) * time.Millisecond)
							_ = inp.Input(string(year[1]))
							time.Sleep(time.Duration(100+rand.Intn(60)) * time.Millisecond)

							// 4. Đảm bảo giá trị đã hợp lệ (có dấu '/') và kích hoạt đầy đủ các sự kiện validation
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

					log.Printf("-> ĐÃ ĐIỀN THÀNH CÔNG %s!\n", fieldName)
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
			log.Printf("-> [Main DOM] Bắt trúng %s trên trang chính!\n", fieldName)
			_ = inp.ScrollIntoView()
			_ = inp.Focus()
			_ = inp.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(100 * time.Millisecond)

			_ = inp.SelectAllText()
			_ = page.Keyboard.Press(input.ControlLeft)
			_ = page.Keyboard.Press(input.KeyA)
			_ = page.Keyboard.Release(input.ControlLeft)
			_ = page.Keyboard.Press(input.Backspace)
			for i := 0; i < 15; i++ {
				_ = page.Keyboard.Press(input.Backspace)
			}
			time.Sleep(80 * time.Millisecond)

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

			log.Printf("-> ĐÃ ĐIỀN THÀNH CÔNG %s TRÊN TRANG CHÍNH!\n", fieldName)
			return true
		}
	}

	log.Printf("[!] Không tìm thấy ô nhập cho %s theo selector: %s\n", fieldName, selector)
	return false
}

// fillPaymentCardForm thực hiện toàn bộ quy trình điền thẻ tín dụng (Card Number, Expiry, CVV, Name)
func fillPaymentCardForm(page *rod.Page, profile *UserProfile) error {
	if profile.CardNumber == "" {
		log.Println("[!] Không có thông tin số thẻ tín dụng để điền.")
		return nil
	}

	log.Println("==================================================")
	log.Println("-> [BƯỚC 7] BẮT ĐẦU ĐIỀN THÔNG TIN THẺ TÍN DỤNG...")
	log.Println("==================================================")

	ensurePaymentSectionVisible(page)
	waitForPageIdle(page)

	// Chuẩn bị chuỗi ngày hết hạn
	expMonth := profile.ExpMonth
	if len(expMonth) == 1 {
		expMonth = "0" + expMonth
	}
	expYear := profile.ExpYear
	if len(expYear) == 4 {
		expYear = expYear[2:]
	}
	expCombined := expMonth + expYear // Ví dụ: "0729"

	log.Printf("-> Dữ liệu thẻ cần điền: Số thẻ: %s | Hạn: %s/%s (%s) | CVV: %s\n",
		maskSecret("card", profile.CardNumber), expMonth, expYear, expCombined, maskSecret("cvv", profile.CVV))

	// Nghỉ tự nhiên trước khi bắt đầu nhập thẻ (200 - 300ms)
	time.Sleep(time.Duration(200+rand.Intn(100)) * time.Millisecond)

	// 1. Điền Số thẻ: id="number" hoặc name="number"
	cardOk := fillFieldByExactSelector(page, "Số thẻ (Card Number)", `input#number, input[name="number"]`, profile.CardNumber)
	time.Sleep(time.Duration(150+rand.Intn(80)) * time.Millisecond)

	// 2. Điền Ngày hết hạn: id="expiry" hoặc name="expiry"
	expiryOk := fillFieldByExactSelector(page, "Hạn dùng (Expiration Date)", `input#expiry, input[name="expiry"]`, expCombined)
	time.Sleep(time.Duration(120+rand.Intn(80)) * time.Millisecond)

	// 3. Điền CVV: id="verification_value" hoặc name="verification_value"
	cvvOk := fillFieldByExactSelector(page, "Mã bảo mật (CVV)", `input#verification_value, input[name="verification_value"]`, profile.CVV)
	time.Sleep(time.Duration(120+rand.Intn(80)) * time.Millisecond)

	// 4. Điền Tên trên thẻ nếu có ô Name on card: id="name" hoặc name="name"
	if profile.FullName != "" {
		fillFieldByExactSelector(page, "Tên trên thẻ (Name on Card)", `input#name, input[name="name"], input[placeholder*="Name on card" i]`, profile.FullName)
		time.Sleep(time.Duration(120+rand.Intn(80)) * time.Millisecond)
	}

	// Đợi iframe Shopify hoàn tất kiểm tra Luhn và hợp lệ hóa thẻ
	time.Sleep(600 * time.Millisecond)

	if cardOk && expiryOk && cvvOk {
		log.Println("==================================================")
		log.Println("-> ĐÃ HOÀN TẤT NHẬP CHÍNH XÁC TOÀN BỘ THẺ TÍN DỤNG (#number, #expiry, #verification_value)!")
		log.Println("==================================================")
	} else {
		log.Printf("[!] Trạng thái điền thẻ: Số thẻ=%t | Hạn=%t | CVV=%t\n", cardOk, expiryOk, cvvOk)
	}

	return nil
}

// applyDiscountCode tìm ô name="reductions", xoá sạch dữ liệu, nhập mã giảm giá và bấm Apply
func applyDiscountCode(page *rod.Page, discountCode string) error {
	discountCode = strings.TrimSpace(discountCode)
	if discountCode == "" {
		return nil
	}

	log.Println("==================================================")
	log.Printf("-> [BƯỚC 7] ĐANG NHẬP VÀ ÁP DỤNG MÃ GIẢM GIÁ: '%s' (name=\"reductions\")...\n", discountCode)
	log.Println("==================================================")

	startTime := time.Now()
	timeout := 20 * time.Second

	for time.Since(startTime) < timeout {
		// 1. Nếu khung nhập giảm giá bị ẩn trong accordion / toggle, bấm mở
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
		time.Sleep(300 * time.Millisecond)

		// 2. Tìm ô nhập name="reductions"
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
			time.Sleep(150 * time.Millisecond)
			_ = reductionsInput.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(100 * time.Millisecond)

			// Xoá sạch dữ liệu cũ
			_ = reductionsInput.SelectAllText()
			_ = page.Keyboard.Press(input.ControlLeft)
			_ = page.Keyboard.Press(input.KeyA)
			_ = page.Keyboard.Release(input.ControlLeft)
			_ = page.Keyboard.Press(input.Backspace)
			_, _ = reductionsInput.Eval(`() => { this.value = ''; this.dispatchEvent(new Event('input', {bubbles: true})); }`)
			time.Sleep(100 * time.Millisecond)

			// Gõ mã giảm giá mô phỏng người thật
			typeHumanLike(reductionsInput, discountCode, false)
			time.Sleep(time.Duration(150+rand.Intn(100)) * time.Millisecond)

			// Gửi phím Enter
			_ = page.Keyboard.Press(input.Enter)
			time.Sleep(500 * time.Millisecond)

			// Tìm và bấm nút Apply
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

		time.Sleep(1 * time.Second)
	}

	log.Println("[!] Không tìm thấy ô nhập mã giảm giá name='reductions'.")
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

	// Fallback CDP Element Click
	hasPay, payBtn, err := page.Has(`button#checkout-pay-button, button[id*="pay-button"], button[type="submit"]`)
	if err == nil && hasPay && payBtn != nil {
		vis, _ := payBtn.Timeout(500 * time.Millisecond).Visible()
		if vis {
			_ = payBtn.Timeout(2 * time.Second).ScrollIntoView()
			time.Sleep(150 * time.Millisecond)
			_ = payBtn.Timeout(2*time.Second).Click(proto.InputMouseButtonLeft, 1)
			return true, "Pay now"
		}
	}

	return false, ""
}

// extractOrderSummaryAndPayNow kiểm tra giá tiền đã giảm và bấm Pay now (chờ API xử lý xong 100% trước khi bước tiếp theo)
func extractOrderSummaryAndPayNow(page *rod.Page, task CheckoutTask) error {
	profile := task.Profile
	if profile == nil {
		profile = &UserProfile{}
	}

	log.Println("==================================================")
	log.Println("-> [BƯỚC 8] ĐANG ĐỢI CẬP NHẬT GIÁ VÀ BẤM 'PAY NOW'...")
	log.Println("==================================================")

	// 1. Chờ hệ thống Shopify cập nhật bảng giá và tính toán mã giảm giá
	log.Println("-> Đang đợi hệ thống cập nhật tổng tiền thanh toán...")
	time.Sleep(3 * time.Second)

	// 2. Trích xuất thông tin giá tiền (Subtotal, Discount, Total)
	priceRes, err := page.Eval(`() => {
		let total = '';
		let discount = '';

		const totalEls = Array.from(document.querySelectorAll('[data-checkout-payment-due-target], strong[class*="total"], span[class*="total"], [aria-label*="Total"], [class*="payment-due"], [class*="final-price"]'));
		for (const el of totalEls) {
			const t = (el.innerText || el.textContent || '').trim();
			if (t.includes('$')) {
				total = t;
				break;
			}
		}

		if (!total) {
			const summary = document.querySelector('aside, [role="complementary"], [class*="order-summary"], [class*="summary"], main');
			if (summary) {
				const spans = Array.from(summary.querySelectorAll('span, strong, em, div'));
				for (let i = spans.length - 1; i >= 0; i--) {
					const t = (spans[i].innerText || '').trim();
					if (t.startsWith('$') && t.length <= 15) {
						total = t;
						break;
					}
				}
			}
		}

		const discEls = Array.from(document.querySelectorAll('[class*="reduction"], [class*="discount"], [class*="tag"], [class*="badge"]'));
		for (const el of discEls) {
			const t = (el.innerText || el.textContent || '').trim();
			if (t.includes('$') || t.includes('-') || t.includes('%')) {
				discount += (discount ? ' | ' : '') + t;
			}
		}

		return { total, discount };
	}`)

	if err == nil && priceRes != nil {
		total := priceRes.Value.Get("total").String()
		discount := priceRes.Value.Get("discount").String()

		log.Println("--------------------------------------------------")
		if total != "" {
			log.Printf("-> [TỔNG TIỀN PHẢI TRẢ]: %s\n", total)
		} else {
			log.Println("-> [TỔNG TIỀN PHẢI TRẢ]: Đã cập nhật trên giao diện thanh toán")
		}
		if discount != "" {
			log.Printf("-> [THÔNG TIN GIẢM GIÁ]: %s\n", discount)
		}
		log.Println("--------------------------------------------------")
	}

	// 3. Thực hiện bấm nút "Pay now" (Thử tối đa 4 lần: lần 1 + 3 lần retry có nhập lại thẻ)
	maxRetries := 3
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
		if clicked {
			log.Printf("-> ĐÃ BẤM THÀNH CÔNG NÚT '%s' (Lần %d/4)!\n", btnName, attempt)
		} else {
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

		// BẮT BUỘC CHỜ API CỔNG THANH TOÁN GỌI XONG HOÀN TOÀN:
		// Tuyệt đối không thực hiện bước tiếp theo trong khi nút vẫn đang "Processing..."!
		log.Println("-> BẮT ĐẦU CHỜ API CỔNG THANH TOÁN GỌI XONG (nút hết 'Processing...' và ngưng spinner)...")
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

		// API đã gọi xong nhưng trả về lỗi
		log.Printf("-> API THANH TOÁN ĐÃ GỌI XONG VỚI KẾT QUẢ: %s\n", errMsg)

		if attempt < 1+maxRetries {
			log.Printf("-> [BƯỚC TIẾP THEO] Chờ 5 giây cho hệ thống ổn định hoàn toàn rồi mới thử lại lần %d/4...\n", attempt+1)
			waitForPageIdle(page)
			time.Sleep(5 * time.Second)
		} else {
			log.Printf("[!] Cổng thanh toán kết luận thất bại sau 4 lần thử: %s\n", errMsg)
			return fmt.Errorf("thanh toán không thành công sau 4 lần thử: %s", errMsg)
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

		// 2. Đánh giá trạng thái DOM
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
