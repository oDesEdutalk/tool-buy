package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"golang.org/x/net/proxy"
)

// DefaultProxy do người dùng cung cấp

const DefaultProxy = "global.rotip.trustsource.live:20000:USER501664-zone-custom-region-US-session-10840789:41b761"

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
}

// parseDataString phân tích chuỗi mẫu dạng:
// "4160467122625048|07|29|082|Elizabeth Bass|7920 N 107Th #9|Milwaukee|WI|53224|US|4143243575|lizjay2415@gmail.com"
func parseDataString(raw string) *UserProfile {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}

	profile := &UserProfile{}
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
			profile.LastName = ""
		} else if len(nameParts) >= 2 {
			profile.FirstName = nameParts[0]
			profile.LastName = strings.Join(nameParts[1:], " ")
		}
		// Tạo email từ FullName theo định dạng yêu cầu: Elizabeth Bass -> ElizabethBass@gmail.com
		cleanName := strings.ReplaceAll(profile.FullName, " ", "")
		profile.Email = fmt.Sprintf("%s@gmail.com", cleanName)
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

	// Tạo địa chỉ đầy đủ để tìm kiếm gợi ý
	if profile.Address != "" {
		if profile.City != "" && profile.State != "" && profile.ZipCode != "" {
			profile.RawAddress = fmt.Sprintf("%s, %s %s %s", profile.Address, profile.City, profile.State, profile.ZipCode)
		} else {
			profile.RawAddress = profile.Address
		}
	}

	return profile
}

func main() {
	targetURL := flag.String("url", "https://www.on.com/en-us/products/5-court-shorts-m-1mg1019/mens/black-apparel-1MG10190553", "URL trang web cần mở")
	targetSize := flag.String("size", "S", "Size sản phẩm cần chọn (S, M, L, XL, ...)")
	proxyRaw := flag.String("proxy", DefaultProxy, "Proxy SOCKS5 (định dạng host:port:user:pass hoặc socks5://user:pass@host:port)")
	headless := flag.Bool("headless", false, "Chạy ẩn trình duyệt (true/false)")

	// Tham số chuỗi dữ liệu tổng hợp
	dataRaw := flag.String("data", "4160467122625048|07|29|082|Elizabeth Bass|7920 N 107Th #9|Milwaukee|WI|53224|US|4143243575|lizjay2415@gmail.com", "Chuỗi dữ liệu tổng hợp")

	// Các cờ riêng lẻ (nếu cần ghi đè)
	emailFlag := flag.String("email", "", "Email thanh toán (để trống sẽ tự lấy từ -data)")
	firstNameFlag := flag.String("firstname", "", "Tên (First Name)")
	lastNameFlag := flag.String("lastname", "", "Họ (Last Name)")
	addressFlag := flag.String("address", "820 Roxfield Ct, Buford GA 30518", "Địa chỉ gõ vào ô tìm kiếm (hoặc lấy từ -data)")
	phoneFlag := flag.String("phone", "", "Số điện thoại")
	cardFlag := flag.String("card", "", "Số thẻ tín dụng")
	monthFlag := flag.String("month", "", "Tháng hết hạn MM")
	yearFlag := flag.String("year", "", "Năm hết hạn YY")
	cvvFlag := flag.String("cvv", "", "Mã bảo mật CVV")
	discountFlag := flag.String("discount", "IDME_ON_20_JOLDOZ", "Mã giảm giá (Discount code)")

	flag.Parse()

	// Phân tích chuỗi profile
	profile := parseDataString(*dataRaw)
	if profile == nil {
		profile = &UserProfile{}
	}

	// Ghi đè nếu có flag chỉ định
	if *emailFlag != "" {
		profile.Email = *emailFlag
	}
	if *firstNameFlag != "" {
		profile.FirstName = *firstNameFlag
	}
	if *lastNameFlag != "" {
		profile.LastName = *lastNameFlag
	}
	if *addressFlag != "" {
		profile.RawAddress = *addressFlag
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

	log.Println("==================================================")
	log.Println("     TOOL AUTOMATION BROWSER SOCKS5 (GOLANG)      ")
	log.Println("==================================================")
	log.Printf("-> URL mục tiêu  : %s\n", *targetURL)
	log.Printf("-> Size cần mua  : %s\n", *targetSize)
	log.Printf("-> Email         : %s\n", profile.Email)
	log.Printf("-> Họ và Tên     : %s %s\n", profile.FirstName, profile.LastName)
	log.Printf("-> Địa chỉ tìm   : %s\n", profile.RawAddress)
	log.Printf("-> Số điện thoại : %s\n", profile.Phone)
	log.Printf("-> Thẻ tín dụng  : %s | %s/%s | CVV: %s\n", profile.CardNumber, profile.ExpMonth, profile.ExpYear, profile.CVV)
	log.Printf("-> Mã giảm giá   : %s\n", *discountFlag)

	l := launcher.New().
		Headless(*headless).
		Devtools(false)

	// Xử lý Proxy
	if *proxyRaw != "" {
		host, port, user, pass, err := parseProxyDetails(*proxyRaw)
		if err != nil {
			log.Fatalf("[!] Lỗi phân tích định dạng proxy: %v", err)
		}

		upstreamAddr := fmt.Sprintf("%s:%s", host, port)
		log.Printf("-> Upstream Proxy: %s\n", upstreamAddr)
		if user != "" {
			log.Printf("-> Proxy Auth User: %s\n", user)
		}

		if user != "" && pass != "" {
			localProxyAddr, err := startLocalSocks5Bridge(upstreamAddr, user, pass)
			if err != nil {
				log.Fatalf("[!] Không thể khởi tạo Local Proxy Bridge: %v", err)
			}
			log.Printf("-> Đã thiết lập Local Bridge: socks5://%s\n", localProxyAddr)
			l = l.Proxy("socks5://" + localProxyAddr)
		} else {
			l = l.Proxy(fmt.Sprintf("socks5://%s:%s", host, port))
		}
	} else {
		log.Println("-> Không sử dụng proxy (kết nối trực tiếp).")
	}

	log.Println("-> Đang khởi chạy trình duyệt...")
	browserURL, err := l.Launch()
	if err != nil {
		log.Fatalf("[!] Không thể mở trình duyệt: %v", err)
	}

	browser := rod.New().ControlURL(browserURL).MustConnect()
	defer browser.MustClose()

	log.Println("-> Đang kích hoạt Stealth Mode (chống bot detection)...")
	page, err := stealth.Page(browser)
	if err != nil {
		log.Printf("[!] Cảnh báo: Không thể bật stealth mode, dùng page mặc định: %v", err)
		page = browser.MustPage()
	}

	// Đặt kích thước màn hình
	page.MustSetWindow(0, 0, 1366, 900)

	log.Printf("-> Đang truy cập: %s ...\n", *targetURL)
	err = page.Navigate(*targetURL)
	if err != nil {
		log.Fatalf("[!] Lỗi khi truy cập trang web: %v", err)
	}

	page.MustWaitLoad()
	log.Println("-> ĐÃ MỞ TRANG THÀNH CÔNG!")
	time.Sleep(3 * time.Second)

	// 1. Đóng Popup Cookies nếu có
	dismissCookieConsent(page)

	// 2. Chọn Size sản phẩm
	err = selectProductSize(page, *targetSize)
	if err != nil {
		log.Printf("[!] Lỗi chọn size: %v", err)
	}

	time.Sleep(1500 * time.Millisecond)

	// 3. Bấm "Add to bag"
	err = clickAddToBag(page)
	if err != nil {
		log.Fatalf("[!] Lỗi khi bấm Add to bag: %v", err)
	}

	log.Println("-> Đang chờ giỏ hàng cập nhật...")
	time.Sleep(3 * time.Second)

	// 4. Bấm "Checkout"
	err = clickCheckout(page)
	if err != nil {
		log.Printf("[!] Cảnh báo khi bấm Checkout: %v", err)
	}

	// 5. Chờ chuyển hướng và load xong hoàn toàn trang Checkout rồi điền form (Human Typing)
	err = fillCheckoutForm(page, profile)
	if err != nil {
		log.Printf("[!] Cảnh báo khi điền form checkout: %v", err)
	}

	// 6. Nhập và áp dụng Mã giảm giá (Discount Code) trước để tránh reload làm mất thông tin thẻ
	if *discountFlag != "" {
		err = applyDiscountCode(page, *discountFlag)
		if err != nil {
			log.Printf("[!] Cảnh báo khi áp dụng mã giảm giá: %v", err)
		}
		time.Sleep(2 * time.Second)
	}

	// 7. Điền thông tin Thẻ tín dụng (Credit Card Payment: #number, #expiry, #verification_value)
	err = fillPaymentCardForm(page, profile)
	if err != nil {
		log.Printf("[!] Cảnh báo khi điền thông tin thẻ tín dụng: %v", err)
	}

	// 8. Chờ kiểm tra giá sau khi áp mã giảm giá và bấm "Pay now" (có nhập lại thẻ nếu thử lại)
	err = extractOrderSummaryAndPayNow(page, profile)
	if err != nil {
		log.Printf("[!] Cảnh báo khi thanh toán: %v", err)
	}

	log.Println("==================================================")
	log.Println("-> HOÀN TẤT TOÀN BỘ QUY TRÌNH MUA HÀNG VÀ THANH TOÁN!")
	log.Println("-> Đã thực hiện thanh toán xong. Kết thúc phiên làm việc sau 5 giây...")
	log.Println("==================================================")

	time.Sleep(5 * time.Second)
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
			el, err := page.Element(sel)
			if err == nil && el != nil {
				visible, _ := el.Visible()
				if visible {
					emailEl = el
					break
				}
			}
		}

		if emailEl != nil {
			break
		}
		time.Sleep(1 * time.Second)
	}

	if emailEl != nil {
		log.Printf("-> Đang gõ Email: '%s' (Human Typing)...\n", profile.Email)
		_ = humanType(page, emailEl, profile.Email)
		time.Sleep(800 * time.Millisecond)
	} else {
		log.Println("[!] Thử tìm ô Email qua JavaScript...")
		_, _ = page.Eval(fmt.Sprintf(`(val) => {
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
		}`, profile.Email))
	}

	// 3. Tìm và gõ First name
	log.Printf("-> Đang gõ First name: '%s' (Human Typing)...\n", profile.FirstName)
	firstNameEl := findInputField(page, []string{
		`input[name="firstName"]`,
		`input[name="first_name"]`,
		`input[name="shippingAddress.firstName"]`,
		`input[placeholder*="First name"]`,
		`input[placeholder*="First Name"]`,
		`input[autocomplete="given-name"]`,
		`input#firstName`,
		`input[id*="firstName"]`,
		`input[id*="first-name"]`,
	})
	if firstNameEl != nil {
		_ = humanType(page, firstNameEl, profile.FirstName)
		time.Sleep(600 * time.Millisecond)
	}

	// 4. Tìm và gõ Last name
	log.Printf("-> Đang gõ Last name: '%s' (Human Typing)...\n", profile.LastName)
	lastNameEl := findInputField(page, []string{
		`input[name="lastName"]`,
		`input[name="last_name"]`,
		`input[name="shippingAddress.lastName"]`,
		`input[placeholder*="Last name"]`,
		`input[placeholder*="Last Name"]`,
		`input[autocomplete="family-name"]`,
		`input#lastName`,
		`input[id*="lastName"]`,
		`input[id*="last-name"]`,
	})
	if lastNameEl != nil {
		_ = humanType(page, lastNameEl, profile.LastName)
		time.Sleep(600 * time.Millisecond)
	}

	// 5. Tìm và gõ Address, sau đó chọn Suggestion đầu tiên
	addressToType := profile.RawAddress
	if addressToType == "" {
		addressToType = profile.Address
	}

	if addressToType != "" {
		log.Printf("-> Đang gõ Address: '%s' (Human Typing)...\n", addressToType)
		addressEl := findInputField(page, []string{
			`input[placeholder*="enter an address"]`,
			`input[placeholder*="house number"]`,
			`input[name="address1"]`,
			`input[name="address"]`,
			`input[name="streetAddress"]`,
			`input[name="shippingAddress.address1"]`,
			`input[placeholder*="Address"]`,
			`input[placeholder*="Street address"]`,
			`input[placeholder*="Street"]`,
			`input[autocomplete="address-line1"]`,
			`input[autocomplete="shipping address-line1"]`,
			`input#address1`,
			`input#shipping-address1`,
			`input[id*="address"]`,
		})

		if addressEl != nil {
			_ = humanType(page, addressEl, addressToType)
			log.Println("-> Đã gõ địa chỉ. Đang đợi danh sách SUGGESTIONS xuất hiện...")
			time.Sleep(1 * time.Second)

			// Chờ và chọn gợi ý đầu tiên trong SUGGESTIONS
			err := selectFirstAddressSuggestion(page, addressEl, addressToType)
			if err != nil {
				log.Printf("[!] Cảnh báo chọn suggestion: %v", err)
			}

			// Chờ API xác thực địa chỉ, tự động điền City/State/ZIP và tải phương thức vận chuyển
			waitForAddressAPIToComplete(page)
		} else {
			log.Println("[!] Không tìm thấy ô nhập Address")
		}
	}

	// 6. Tìm và gõ Phone Number (Sau khi API địa chỉ đã hoàn tất)
	if profile.Phone != "" {
		log.Printf("-> Đang gõ Phone: '%s' (Human Typing)...\n", profile.Phone)
		phoneEl := findInputField(page, []string{
			`input[type="tel"]`,
			`input[name="phone"]`,
			`input[name="telephone"]`,
			`input[name="phoneNumber"]`,
			`input[name="shippingAddress.phone"]`,
			`input[placeholder*="Phone"]`,
			`input[placeholder*="Mobile"]`,
			`input[autocomplete="tel"]`,
			`input[autocomplete="shipping tel"]`,
			`input#phone`,
			`input[id*="phone"]`,
		})

		if phoneEl != nil {
			_ = humanType(page, phoneEl, profile.Phone)
			time.Sleep(800 * time.Millisecond)
		} else {
			log.Println("[!] Không tìm thấy ô nhập Phone")
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

// findInputField tìm kiếm ô input theo danh sách selector
func findInputField(page *rod.Page, selectors []string) *rod.Element {
	for _, sel := range selectors {
		el, err := page.Element(sel)
		if err == nil && el != nil {
			visible, _ := el.Visible()
			if visible {
				return el
			}
		}
	}
	return nil
}

// humanType mô phỏng gõ phím con người an toàn
func humanType(page *rod.Page, el *rod.Element, text string) error {
	if el == nil {
		return fmt.Errorf("element is nil")
	}

	_ = el.ScrollIntoView()
	time.Sleep(100 * time.Millisecond)
	_ = el.Click(proto.InputMouseButtonLeft, 1)
	time.Sleep(100 * time.Millisecond)

	_ = el.SelectAllText()

	for _, ch := range text {
		_ = el.Input(string(ch))
		delay := 25 + rand.Intn(35)
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}

	// Đảm bảo dữ liệu đã vào đúng và trigger các sự kiện React
	_, _ = el.Eval(`(el, expected) => {
		if (el.value !== expected) {
			el.value = expected;
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
		}
	}`, text)

	time.Sleep(150 * time.Millisecond)
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

// startLocalSocks5Bridge tạo một SOCKS5 listener cục bộ (local)
func startLocalSocks5Bridge(upstreamAddr, user, pass string) (string, error) {
	var auth *proxy.Auth
	if user != "" {
		auth = &proxy.Auth{
			User:     user,
			Password: pass,
		}
	}

	dialer, err := proxy.SOCKS5("tcp", upstreamAddr, auth, proxy.Direct)
	if err != nil {
		return "", fmt.Errorf("không thể tạo dialer SOCKS5 upstream: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("không thể mở local listener: %w", err)
	}

	localAddr := listener.Addr().String()

	go func() {
		for {
			clientConn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleLocalSocksConn(clientConn, dialer)
		}
	}()

	return localAddr, nil
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
func fillFieldByExactSelector(page *rod.Page, fieldName string, selector string, value string) bool {
	if strings.TrimSpace(value) == "" {
		return true
	}

	log.Printf("-> Đang tìm và điền %s (%s): '%s'...\n", fieldName, selector, maskSecret(fieldName, value))
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
				const el = document.querySelector(sel);
				return !!el;
			}`, selector)

			if err == nil && hasRes != nil && hasRes.Value.Bool() {
				log.Printf("-> [Iframe #%d] Bắt trúng %s theo selector '%s'!\n", idx, fieldName, selector)
				inp, err := frame.Element(selector)
				if err == nil && inp != nil {
					_ = inp.ScrollIntoView()
					_ = inp.Focus()
					_ = inp.Click(proto.InputMouseButtonLeft, 1)
					time.Sleep(50 * time.Millisecond)

					// Xoá nội dung cũ
					_ = inp.SelectAllText()
					_ = frame.Keyboard.Press(input.ControlLeft)
					_ = frame.Keyboard.Press(input.KeyA)
					_ = frame.Keyboard.Release(input.ControlLeft)
					_ = frame.Keyboard.Press(input.Backspace)
					for i := 0; i < 15; i++ {
						_ = frame.Keyboard.Press(input.Backspace)
					}
					time.Sleep(50 * time.Millisecond)

					// Gõ giá trị
					if isExpiry {
						cleanDigits := ""
						for _, r := range value {
							if r >= '0' && r <= '9' {
								cleanDigits += string(r)
							}
						}
						if len(cleanDigits) == 4 {
							// 1. Gõ 2 số tháng (MM)
							_ = inp.Input(string(cleanDigits[0]))
							time.Sleep(40 * time.Millisecond)
							_ = inp.Input(string(cleanDigits[1]))

							// 2. Chờ nửa giây (500ms) để mask Shopify tự chèn " / "
							time.Sleep(500 * time.Millisecond)

							// 3. Gõ 2 số năm (YY)
							_ = inp.Input(string(cleanDigits[2]))
							time.Sleep(40 * time.Millisecond)
							_ = inp.Input(string(cleanDigits[3]))
							time.Sleep(100 * time.Millisecond)
						} else {
							for _, ch := range value {
								_ = inp.Input(string(ch))
								time.Sleep(35 * time.Millisecond)
							}
						}
					} else {
						for _, ch := range value {
							_ = inp.Input(string(ch))
							time.Sleep(35 * time.Millisecond)
						}
						time.Sleep(50 * time.Millisecond)

						// Set giá trị và kích hoạt sự kiện DOM trong Frame
						_, _ = frame.Eval(`(sel, val) => {
							const el = document.querySelector(sel);
							if (el) {
								el.value = val;
								el.dispatchEvent(new Event('input', { bubbles: true }));
								el.dispatchEvent(new Event('change', { bubbles: true }));
							}
						}`, selector, value)
					}

					log.Printf("-> ĐÃ ĐIỀN THÀNH CÔNG %s!\n", fieldName)
					return true
				}
			}
		}
	}

	// 2. Nếu nằm trên trang chính
	hasMain, _ := page.Eval(`(sel) => {
		const el = document.querySelector(sel);
		return !!el;
	}`, selector)

	if hasMain != nil && hasMain.Value.Bool() {
		inp, err := page.Element(selector)
		if err == nil && inp != nil {
			log.Printf("-> [Main DOM] Bắt trúng %s trên trang chính!\n", fieldName)
			_ = inp.ScrollIntoView()
			_ = inp.Focus()
			_ = inp.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(50 * time.Millisecond)

			_ = inp.SelectAllText()
			_ = page.Keyboard.Press(input.ControlLeft)
			_ = page.Keyboard.Press(input.KeyA)
			_ = page.Keyboard.Release(input.ControlLeft)
			_ = page.Keyboard.Press(input.Backspace)
			for i := 0; i < 15; i++ {
				_ = page.Keyboard.Press(input.Backspace)
			}
			time.Sleep(50 * time.Millisecond)

			if isExpiry {
				cleanDigits := ""
				for _, r := range value {
					if r >= '0' && r <= '9' {
						cleanDigits += string(r)
					}
				}
				if len(cleanDigits) == 4 {
					_ = inp.Input(string(cleanDigits[0]))
					time.Sleep(40 * time.Millisecond)
					_ = inp.Input(string(cleanDigits[1]))
					time.Sleep(500 * time.Millisecond)
					_ = inp.Input(string(cleanDigits[2]))
					time.Sleep(40 * time.Millisecond)
					_ = inp.Input(string(cleanDigits[3]))
					time.Sleep(100 * time.Millisecond)
				} else {
					for _, ch := range value {
						_ = inp.Input(string(ch))
						time.Sleep(35 * time.Millisecond)
					}
				}
			} else {
				for _, ch := range value {
					_ = inp.Input(string(ch))
					time.Sleep(35 * time.Millisecond)
				}
				time.Sleep(50 * time.Millisecond)

				_, _ = page.Eval(`(sel, val) => {
					const el = document.querySelector(sel);
					if (el) {
						el.value = val;
						el.dispatchEvent(new Event('input', { bubbles: true }));
						el.dispatchEvent(new Event('change', { bubbles: true }));
					}
				}`, selector, value)
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

	// 1. Điền Số thẻ: id="number" hoặc name="number"
	cardOk := fillFieldByExactSelector(page, "Số thẻ (Card Number)", `input#number, input[name="number"]`, profile.CardNumber)
	time.Sleep(300 * time.Millisecond)

	// 2. Điền Ngày hết hạn: id="expiry" hoặc name="expiry"
	expiryOk := fillFieldByExactSelector(page, "Hạn dùng (Expiration Date)", `input#expiry, input[name="expiry"]`, expCombined)
	time.Sleep(300 * time.Millisecond)

	// 3. Điền CVV: id="verification_value" hoặc name="verification_value"
	cvvOk := fillFieldByExactSelector(page, "Mã bảo mật (CVV)", `input#verification_value, input[name="verification_value"]`, profile.CVV)
	time.Sleep(300 * time.Millisecond)

	// 4. Điền Tên trên thẻ nếu có ô Name on card: id="name" hoặc name="name"
	if profile.FullName != "" {
		fillFieldByExactSelector(page, "Tên trên thẻ (Name on Card)", `input#name, input[name="name"], input[placeholder*="Name on card" i]`, profile.FullName)
		time.Sleep(200 * time.Millisecond)
	}

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
			el, err := page.Element(sel)
			if err == nil && el != nil {
				vis, _ := el.Visible()
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

			// Gõ mã giảm giá
			for _, ch := range discountCode {
				_ = reductionsInput.Input(string(ch))
				delay := 25 + rand.Intn(30)
				time.Sleep(time.Duration(delay) * time.Millisecond)
			}
			time.Sleep(300 * time.Millisecond)

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
			time.Sleep(2 * time.Second)
			return nil
		}

		time.Sleep(1 * time.Second)
	}

	log.Println("[!] Không tìm thấy ô nhập mã giảm giá name='reductions'.")
	return fmt.Errorf("không tìm thấy ô reductions")
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
	payBtn, err := page.Element(`button#checkout-pay-button, button[id*="pay-button"], button[type="submit"]`)
	if err == nil && payBtn != nil {
		vis, _ := payBtn.Visible()
		if vis {
			_ = payBtn.ScrollIntoView()
			time.Sleep(150 * time.Millisecond)
			_ = payBtn.Click(proto.InputMouseButtonLeft, 1)
			return true, "Pay now"
		}
	}

	return false, ""
}

// extractOrderSummaryAndPayNow kiểm tra giá tiền đã giảm và bấm Pay now (nhập lại thẻ trước khi thử lại tối đa 2 lần)
func extractOrderSummaryAndPayNow(page *rod.Page, profile *UserProfile) error {
	log.Println("==================================================")
	log.Println("-> [BƯỚC 8] ĐANG ĐỢI CẬP NHẬT GIÁ VÀ BẤM 'PAY NOW'...")
	log.Println("==================================================")

	// 1. Chờ hệ thống Shopify cập nhật bảng giá và tính toán mã giảm giá
	log.Println("-> Đang đợi hệ thống cập nhật tổng tiền thanh toán...")
	time.Sleep(4 * time.Second)

	// 2. Trích xuất thông tin giá tiền (Subtotal, Discount, Total)
	priceRes, err := page.Eval(`() => {
		let total = '';
		let discount = '';

		// Tìm phần tử Total price
		const totalEls = Array.from(document.querySelectorAll('[data-checkout-payment-due-target], strong[class*="total"], span[class*="total"], [aria-label*="Total"], [class*="payment-due"], [class*="final-price"]'));
		for (const el of totalEls) {
			const t = (el.innerText || el.textContent || '').trim();
			if (t.includes('$')) {
				total = t;
				break;
			}
		}

		// Nếu chưa thấy, quét các phần tử trong summary
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

		// Tìm Discount amount
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

	// 3. Thực hiện bấm nút "Pay now" (Thử tối đa 3 lần: lần 1 + 2 lần retry có nhập lại thẻ)
	maxRetries := 2
	for attempt := 1; attempt <= 1+maxRetries; attempt++ {
		if attempt > 1 {
			log.Println("==================================================")
			log.Printf("-> [RETRY %d/%d] Đang nhập lại toàn bộ thông tin thẻ tín dụng trước khi bấm Pay now...\n", attempt-1, maxRetries)
			log.Println("==================================================")
			_ = fillPaymentCardForm(page, profile)
			time.Sleep(1 * time.Second)
		}

		// Chờ ngẫu nhiên 3 đến 6 giây trước khi bấm Pay now
		randomWait := 3 + rand.Intn(4) // 3, 4, 5, hoặc 6 giây
		log.Printf("-> Đang chờ ngẫu nhiên %d giây (3-6s) trước khi bấm Pay now...\n", randomWait)
		time.Sleep(time.Duration(randomWait) * time.Second)

		log.Printf("-> Đang kiểm tra và bấm nút 'Pay now' (Lần %d/%d)...\n", attempt, 1+maxRetries)
		clicked, btnName := clickPayNowButton(page)
		if clicked {
			log.Printf("-> ĐÃ BẤM THÀNH CÔNG NÚT '%s' (Lần %d)!\n", btnName, attempt)
		} else {
			log.Println("-> Nút thanh toán không còn hiển thị (đơn hàng đang được xử lý hoặc đã chuyển trang thành công).")
			break
		}

		log.Println("-> Đang đợi hệ thống phản hồi kết quả thanh toán...")
		time.Sleep(5 * time.Second)

		// Kiểm tra nếu trang đã chuyển hướng sang trang hoàn tất
		info, _ := page.Info()
		if info != nil {
			urlLower := strings.ToLower(info.URL)
			if strings.Contains(urlLower, "thank_you") || strings.Contains(urlLower, "orders") || strings.Contains(urlLower, "order-received") {
				log.Printf("-> ĐÃ CHUYỂN HƯỚNG SANG TRANG XÁC NHẬN ĐƠN HÀNG THÀNH CÔNG!\n-> URL: %s\n", info.URL)
				break
			}
		}
	}

	return nil
}
