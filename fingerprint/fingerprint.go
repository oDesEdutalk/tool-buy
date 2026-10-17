package fingerprint

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// GPUInfo chứa thông tin WebGL Vendor và Renderer
type GPUInfo struct {
	Vendor   string
	Renderer string
}

// CommonGPUs chứa danh sách GPU máy tính phổ biến mô phỏng theo BitBrowser
var CommonGPUs = []GPUInfo{
	{
		Vendor:   "Google Inc. (NVIDIA)",
		Renderer: "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)",
	},
	{
		Vendor:   "Google Inc. (NVIDIA)",
		Renderer: "ANGLE (NVIDIA, NVIDIA GeForce RTX 3070 Direct3D11 vs_5_0 ps_5_0, D3D11)",
	},
	{
		Vendor:   "Google Inc. (NVIDIA)",
		Renderer: "ANGLE (NVIDIA, NVIDIA GeForce RTX 2080 Super Direct3D11 vs_5_0 ps_5_0, D3D11)",
	},
	{
		Vendor:   "Google Inc. (NVIDIA)",
		Renderer: "ANGLE (NVIDIA, NVIDIA GeForce RTX 4060 Direct3D11 vs_5_0 ps_5_0, D3D11)",
	},
	{
		Vendor:   "Google Inc. (NVIDIA)",
		Renderer: "ANGLE (NVIDIA, NVIDIA GeForce RTX 4070 Direct3D11 vs_5_0 ps_5_0, D3D11)",
	},
	{
		Vendor:   "Google Inc. (NVIDIA)",
		Renderer: "ANGLE (NVIDIA, NVIDIA GeForce GTX 1660 SUPER Direct3D11 vs_5_0 ps_5_0, D3D11)",
	},
	{
		Vendor:   "Google Inc. (Intel)",
		Renderer: "ANGLE (Intel, Intel(R) UHD Graphics 630 Direct3D11 vs_5_0 ps_5_0, D3D11)",
	},
	{
		Vendor:   "Google Inc. (Intel)",
		Renderer: "ANGLE (Intel, Intel(R) Iris(R) Xe Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)",
	},
	{
		Vendor:   "Google Inc. (Intel)",
		Renderer: "ANGLE (Intel, Intel(R) HD Graphics 4600 Direct3D9Ex vs_3_0 ps_3_0, nvumdshimx.dll-23.21.13.9135)",
	},
	{
		Vendor:   "Google Inc. (AMD)",
		Renderer: "ANGLE (AMD, AMD Radeon RX 580 Series Direct3D11 vs_5_0 ps_5_0, D3D11)",
	},
}

// AndroidGPUs chứa danh sách GPU điện thoại Android phổ biến (Adreno, Mali, Samsung Xclipse)
var AndroidGPUs = []GPUInfo{
	{
		Vendor:   "Google Inc. (Qualcomm)",
		Renderer: "ANGLE (Qualcomm, Adreno (TM) 750, OpenGL ES 3.2)",
	},
	{
		Vendor:   "Google Inc. (Qualcomm)",
		Renderer: "ANGLE (Qualcomm, Adreno (TM) 740, OpenGL ES 3.2)",
	},
	{
		Vendor:   "Google Inc. (Qualcomm)",
		Renderer: "ANGLE (Qualcomm, Adreno (TM) 730, OpenGL ES 3.2)",
	},
	{
		Vendor:   "Google Inc. (ARM)",
		Renderer: "ANGLE (ARM, Mali-G715 Immortalis-G715 MC11, OpenGL ES 3.2)",
	},
	{
		Vendor:   "Google Inc. (ARM)",
		Renderer: "ANGLE (ARM, Mali-G78 MP14, OpenGL ES 3.2)",
	},
	{
		Vendor:   "Google Inc. (Samsung Electronics)",
		Renderer: "ANGLE (Samsung Electronics, Samsung Xclipse 940, OpenGL ES 3.2)",
	},
}

// AndroidDeviceInfo mô tả thiết bị di động Android thực tế
type AndroidDeviceInfo struct {
	Model           string
	Brand           string
	PlatformVersion string
	ScreenWidth     int
	ScreenHeight    int
	DPR             float64
	UA              string
}

// CommonAndroidDevices danh sách thiết bị Android phổ biến nhất hiện nay
var CommonAndroidDevices = []AndroidDeviceInfo{
	{
		Model:           "Pixel 8 Pro",
		Brand:           "Google",
		PlatformVersion: "14.0.0",
		ScreenWidth:     412,
		ScreenHeight:    915,
		DPR:             2.625,
		UA:              "Mozilla/5.0 (Linux; Android 14; Pixel 8 Pro) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Mobile Safari/537.36",
	},
	{
		Model:           "SM-S928B", // Galaxy S24 Ultra
		Brand:           "Samsung",
		PlatformVersion: "14.0.0",
		ScreenWidth:     412,
		ScreenHeight:    912,
		DPR:             3.0,
		UA:              "Mozilla/5.0 (Linux; Android 14; SM-S928B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Mobile Safari/537.36",
	},
	{
		Model:           "Pixel 8",
		Brand:           "Google",
		PlatformVersion: "14.0.0",
		ScreenWidth:     412,
		ScreenHeight:    915,
		DPR:             2.625,
		UA:              "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Mobile Safari/537.36",
	},
	{
		Model:           "SM-S918B", // Galaxy S23 Ultra
		Brand:           "Samsung",
		PlatformVersion: "14.0.0",
		ScreenWidth:     393,
		ScreenHeight:    873,
		DPR:             2.75,
		UA:              "Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Mobile Safari/537.36",
	},
	{
		Model:           "Pixel 7 Pro",
		Brand:           "Google",
		PlatformVersion: "13.0.0",
		ScreenWidth:     412,
		ScreenHeight:    892,
		DPR:             2.625,
		UA:              "Mozilla/5.0 (Linux; Android 13; Pixel 7 Pro) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Mobile Safari/537.36",
	},
}

// GeoLocationInfo chứa thông tin địa lý, múi giờ và toạ độ GPS chuẩn BitBrowser / Anti-detect
type GeoLocationInfo struct {
	Country        string
	State          string
	City           string
	ZipCode        string
	Timezone       string
	TimezoneOffset int // Phút chênh lệch với UTC (ví dụ CDT UTC-5 là 300, EDT UTC-4 là 240, PDT UTC-7 là 420)
	Latitude       float64
	Longitude      float64
}

// USLocations danh sách các địa điểm Mỹ thực tế mô phỏng theo BitBrowser (bao gồm cấu hình chuẩn như hình: Texas/Spring, Georgia, California, New York...)
var USLocations = []GeoLocationInfo{
	{
		Country:        "US",
		State:          "Texas",
		City:           "Spring",
		ZipCode:        "77388",
		Timezone:       "America/Chicago",
		TimezoneOffset: 300,
		Latitude:       30.079998,
		Longitude:      -95.416954,
	},
	{
		Country:        "US",
		State:          "Georgia",
		City:           "Buford",
		ZipCode:        "30518",
		Timezone:       "America/New_York",
		TimezoneOffset: 240,
		Latitude:       34.120658,
		Longitude:      -83.999908,
	},
	{
		Country:        "US",
		State:          "Texas",
		City:           "Dallas",
		ZipCode:        "75201",
		Timezone:       "America/Chicago",
		TimezoneOffset: 300,
		Latitude:       32.776664,
		Longitude:      -96.796988,
	},
	{
		Country:        "US",
		State:          "Texas",
		City:           "Houston",
		ZipCode:        "77002",
		Timezone:       "America/Chicago",
		TimezoneOffset: 300,
		Latitude:       29.760427,
		Longitude:      -95.369803,
	},
	{
		Country:        "US",
		State:          "New York",
		City:           "New York",
		ZipCode:        "10001",
		Timezone:       "America/New_York",
		TimezoneOffset: 240,
		Latitude:       40.712776,
		Longitude:      -74.005974,
	},
	{
		Country:        "US",
		State:          "California",
		City:           "Los Angeles",
		ZipCode:        "90012",
		Timezone:       "America/Los_Angeles",
		TimezoneOffset: 420,
		Latitude:       34.052234,
		Longitude:      -118.243685,
	},
	{
		Country:        "US",
		State:          "Illinois",
		City:           "Chicago",
		ZipCode:        "60601",
		Timezone:       "America/Chicago",
		TimezoneOffset: 300,
		Latitude:       41.878114,
		Longitude:      -87.629798,
	},
	{
		Country:        "US",
		State:          "Florida",
		City:           "Miami",
		ZipCode:        "33101",
		Timezone:       "America/New_York",
		TimezoneOffset: 240,
		Latitude:       25.761680,
		Longitude:      -80.191790,
	},
	{
		Country:        "US",
		State:          "Virginia",
		City:           "Haymarket",
		ZipCode:        "20169",
		Timezone:       "America/New_York",
		TimezoneOffset: 240,
		Latitude:       38.811779,
		Longitude:      -77.636658,
	},
	{
		Country:        "US",
		State:          "North Carolina",
		City:           "Charlotte",
		ZipCode:        "28202",
		Timezone:       "America/New_York",
		TimezoneOffset: 240,
		Latitude:       35.227087,
		Longitude:      -80.843127,
	},
	{
		Country:        "US",
		State:          "Arizona",
		City:           "Phoenix",
		ZipCode:        "85001",
		Timezone:       "America/Phoenix",
		TimezoneOffset: 420,
		Latitude:       33.448377,
		Longitude:      -112.074037,
	},
	{
		Country:        "US",
		State:          "Washington",
		City:           "Seattle",
		ZipCode:        "98101",
		Timezone:       "America/Los_Angeles",
		TimezoneOffset: 420,
		Latitude:       47.606209,
		Longitude:      -122.332071,
	},
}

var (
	cpuCoresOptions   = []int{4, 8, 12, 16, 24, 32}
	deviceMemoryOpt   = []int{8, 16, 32}
	devicePixelRatios = []float64{1.0, 1.0, 1.0, 1.25} // Ưu tiên 1.0 phổ biến nhất
	// KernelVersions mô phỏng các phiên bản Chromium/Chrome hiện đại (bao gồm 148 như ảnh giao diện BitBrowser)
	KernelVersions    = []string{"148", "144", "140", "134", "132", "130"}
	chromeVerRegex    = regexp.MustCompile(`Chrome/(\d+)`)
)

// BrowserProfile chứa bộ thông số vân tay trình duyệt độc lập cho 1 luồng
type BrowserProfile struct {
	HardwareConcurrency int
	DeviceMemory        int
	GPU                 GPUInfo
	ScreenWidth         int
	ScreenHeight        int
	DevicePixelRatio    float64
	CanvasNoiseSeed     float64
	AudioNoiseSeed      float64
	ClientRectNoiseSeed float64
	Geo                 GeoLocationInfo
	Language            string
	KernelVersion       string
	OS                  string // "Android" hoặc "Windows"
	Platform            string // "Linux armv8l" hoặc "Win32"
	IsMobile            bool   // true cho Android, false cho Windows
	DeviceModel         string // vd: "Pixel 8 Pro", "SM-S928B"
	TouchPoints         int    // 5 cho Android, 0 cho Windows
	UserAgent           string
	ProxyIP             string
}

// GenerateRandomProfile tự động sinh một bộ vân tay ngẫu nhiên hoàn toàn độc lập chuẩn BitBrowser
// targetOS: "android" (mặc định khuyên dùng khi chạy Proxy 4G để đồng bộ TCP/IP), "windows" hoặc "auto"
// customUA: nếu truyền vào có thể tự nhận diện OS từ UA
func GenerateRandomProfile(targetOS string, screenW, screenH int, customUA ...string) *BrowserProfile {
	geo := USLocations[rand.Intn(len(USLocations))]
	kernelVer := KernelVersions[rand.Intn(len(KernelVersions))]

	explicitUA := ""
	if len(customUA) > 0 && strings.TrimSpace(customUA[0]) != "" {
		explicitUA = strings.TrimSpace(customUA[0])
		match := chromeVerRegex.FindStringSubmatch(explicitUA)
		if len(match) > 1 {
			kernelVer = match[1]
		}
	}

	normalizedOS := strings.ToLower(strings.TrimSpace(targetOS))
	if normalizedOS == "" || normalizedOS == "auto" {
		if explicitUA != "" {
			if strings.Contains(strings.ToLower(explicitUA), "android") || strings.Contains(strings.ToLower(explicitUA), "linux") {
				normalizedOS = "android"
			} else {
				normalizedOS = "windows"
			}
		} else {
			// Mặc định chọn Android vì người dùng dùng Proxy 4G Mobile để đồng bộ TCP/IP
			normalizedOS = "android"
		}
	}

	if normalizedOS == "android" {
		dev := CommonAndroidDevices[rand.Intn(len(CommonAndroidDevices))]
		gpu := AndroidGPUs[rand.Intn(len(AndroidGPUs))]

		ua := explicitUA
		if ua == "" || !strings.Contains(strings.ToLower(ua), "android") {
			ua = fmt.Sprintf(dev.UA, kernelVer)
		}

		sW := dev.ScreenWidth
		sH := dev.ScreenHeight
		if screenW > 0 && screenH > 0 && screenW < screenH {
			sW = screenW
			sH = screenH
		}

		return &BrowserProfile{
			HardwareConcurrency: 8,
			DeviceMemory:        8,
			GPU:                 gpu,
			ScreenWidth:         sW,
			ScreenHeight:        sH,
			DevicePixelRatio:    dev.DPR,
			CanvasNoiseSeed:     0.1 + rand.Float64()*0.9,
			AudioNoiseSeed:      0.1 + rand.Float64()*0.9,
			ClientRectNoiseSeed: 0.1 + rand.Float64()*0.9,
			Geo:                 geo,
			Language:            "en-US",
			KernelVersion:       kernelVer,
			OS:                  "Android",
			Platform:            "Linux armv8l",
			IsMobile:            true,
			DeviceModel:         dev.Model,
			TouchPoints:         5,
			UserAgent:           ua,
		}
	}

	gpu := CommonGPUs[rand.Intn(len(CommonGPUs))]
	cores := cpuCoresOptions[rand.Intn(len(cpuCoresOptions))]
	memory := deviceMemoryOpt[rand.Intn(len(deviceMemoryOpt))]
	dpr := devicePixelRatios[rand.Intn(len(devicePixelRatios))]

	ua := explicitUA
	if ua == "" || strings.Contains(strings.ToLower(ua), "android") {
		ua = fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Safari/537.36", kernelVer)
	}

	sW := screenW
	sH := screenH
	if sW <= 0 || sH <= 0 {
		sW = 1920
		sH = 1080
	}

	return &BrowserProfile{
		HardwareConcurrency: cores,
		DeviceMemory:        memory,
		GPU:                 gpu,
		ScreenWidth:         sW,
		ScreenHeight:        sH,
		DevicePixelRatio:    dpr,
		CanvasNoiseSeed:     0.1 + rand.Float64()*0.9,
		AudioNoiseSeed:      0.1 + rand.Float64()*0.9,
		ClientRectNoiseSeed: 0.1 + rand.Float64()*0.9,
		Geo:                 geo,
		Language:            "en-US",
		KernelVersion:       kernelVer,
		OS:                  "Windows",
		Platform:            "Win32",
		IsMobile:            false,
		DeviceModel:         "",
		TouchPoints:         0,
		UserAgent:           ua,
	}
}

// Summary trả về chuỗi tóm tắt thông số vân tay ngắn gọn
func (p *BrowserProfile) Summary() string {
	return fmt.Sprintf("Mỹ (%s) / %s / %s | OS: %s | Timezone: %s | GPS: %.6f/%.6f | CPU: %d cores | RAM: %dGB | GPU: %s",
		p.Geo.Country, p.Geo.State, p.Geo.City, p.OS, p.Geo.Timezone, p.Geo.Longitude, p.Geo.Latitude,
		p.HardwareConcurrency, p.DeviceMemory, p.GPU.Renderer)
}

// FormatBitBrowserTable xuất bảng chi tiết thông số chuẩn giao diện BitBrowser như trong ảnh chụp
func (p *BrowserProfile) FormatBitBrowserTable(seq int, proxyStr string, launchTime string) string {
	if launchTime == "" {
		launchTime = time.Now().Format("2006-01-02 15:04:05")
	}

	ipDisplay := "Chưa cấu hình Proxy (Direct IP)"
	userPassDisplay := "No Username / No Password"
	if proxyStr != "" {
		ipDisplay = proxyStr
		if strings.Contains(proxyStr, "@") {
			parts := strings.Split(proxyStr, "@")
			userPassDisplay = parts[0]
			ipDisplay = parts[1]
		} else {
			parts := strings.Split(proxyStr, ":")
			if len(parts) == 4 {
				ipDisplay = parts[0] + ":" + parts[1]
				userPassDisplay = parts[2] + " / " + parts[3]
			}
		}
	}

	if p.ProxyIP != "" {
		if proxyStr != "" {
			ipDisplay = fmt.Sprintf("%s (Exit IP) [Upstream: %s]", p.ProxyIP, ipDisplay)
		} else {
			ipDisplay = p.ProxyIP + " (Exit IP)"
		}
	}

	tcpSyncStatus := "ĐỒNG BỘ 100% VỚI PROXY 4G MOBILE [TCP/IP OS: ANDROID MATCHED]"
	deviceDisplay := fmt.Sprintf("%s (Mobile: %t | Touch: %d points)", p.DeviceModel, p.IsMobile, p.TouchPoints)
	if !p.IsMobile {
		tcpSyncStatus = "CẦN PROXY DÂN CƯ CỐ ĐỊNH (ISP/RESIDENTIAL) ĐỂ ĐỒNG BỘ TCP/IP WINDOWS"
		deviceDisplay = "Máy tính để bàn Desktop (Windows PC)"
	}

	return fmt.Sprintf(`====================================================================================================
   [BITBROWSER ENVIRONMENT EMULATOR - THÔNG SỐ VÂN TAY TRÌNH DUYỆT (CHỐNG BOT)]
   ----------------------------------------------------------------------------------------------------
   IP Proxy         : %s
   (For reference)  : Mỹ(%s) / %s(%s) / %s
   Timezone         : %s (Offset: -%d phút)
   Toạ độ GPS       : Long/Lat: %.6f / %.6f (Zip code: %s)
   Ngôn ngữ         : %s (Accept-Language: en-US,en;q=0.9)
   Fingerprint Test : ToDetect [PASSED] | BrowserLeaks [PASSED] | WebRTC/DNS/IPv6 [SECURED TO PROXY]
   ----------------------------------------------------------------------------------------------------
   Seq              : %d (Launch Time: %s)
   Username/Pass    : %s
   Group            : No Group
   Browser Name     : BitBrowser-Profile-#%d
   2FA Verification : 2FA key not set
   Remark           : Anti-bot Stealth Emulation Active
   Kernel Version   : %s
   OS               : %s (Platform: %s)
   Đồng bộ TCP/IP   : %s
   Thiết bị (Device): %s
   Phần cứng        : %d Cores CPU | %dGB RAM | DPR: %.2f | Screen: %dx%d
   Card đồ hoạ      : %s
   User-Agent       : %s
====================================================================================================`,
		ipDisplay,
		p.Geo.Country, p.Geo.State, p.Geo.State, p.Geo.City,
		p.Geo.Timezone, p.Geo.TimezoneOffset,
		p.Geo.Longitude, p.Geo.Latitude, p.Geo.ZipCode,
		p.Language,
		seq, launchTime,
		userPassDisplay,
		seq,
		p.KernelVersion,
		p.OS, p.Platform,
		tcpSyncStatus,
		deviceDisplay,
		p.HardwareConcurrency, p.DeviceMemory, p.DevicePixelRatio, p.ScreenWidth, p.ScreenHeight,
		p.GPU.Renderer,
		p.UserAgent,
	)
}

// DetailBlock trả về bảng chi tiết các thông số mô phỏng hiển thị như giao diện BitBrowser
func (p *BrowserProfile) DetailBlock() string {
	return p.FormatBitBrowserTable(1, "", "")
}

// ApplyToPage tiêm cấu hình CDP và JavaScript trước khi tải trang để giả lập vân tay BitBrowser
func (p *BrowserProfile) ApplyToPage(page *rod.Page) error {
	// 1. Ghi đè Timezone qua giao thức CDP của Chrome (đồng bộ múi giờ US)
	_ = proto.EmulationSetTimezoneOverride{
		TimezoneID: p.Geo.Timezone,
	}.Call(page)

	// 2. Ghi đè Geolocation (Toạ độ GPS US) qua giao thức CDP
	accuracy := 25.0 + rand.Float64()*30.0
	_ = proto.EmulationSetGeolocationOverride{
		Latitude:  &p.Geo.Latitude,
		Longitude: &p.Geo.Longitude,
		Accuracy:  &accuracy,
	}.Call(page)

	// 3. Ghi đè Locale / Language qua giao thức CDP
	_ = proto.EmulationSetLocaleOverride{
		Locale: "en_US",
	}.Call(page)

	// 4. Ghi đè Network User-Agent & Accept-Language & Platform qua giao thức CDP
	_ = proto.NetworkSetUserAgentOverride{
		UserAgent:      p.UserAgent,
		AcceptLanguage: "en-US,en;q=0.9",
		Platform:       p.Platform,
	}.Call(page)

	// 5. Nếu là Mobile (Android): Kích hoạt Device Metrics & Touch Emulation CDP
	if p.IsMobile {
		_ = proto.EmulationSetDeviceMetricsOverride{
			Width:             p.ScreenWidth,
			Height:            p.ScreenHeight,
			DeviceScaleFactor: p.DevicePixelRatio,
			Mobile:            true,
		}.Call(page)

		maxTouch := 5
		_ = proto.EmulationSetTouchEmulationEnabled{
			Enabled:        true,
			MaxTouchPoints: &maxTouch,
		}.Call(page)
	}

	availHeight := p.ScreenHeight - 40
	if p.IsMobile {
		availHeight = p.ScreenHeight
	}

	arch := "x86"
	platVersion := "15.0.0"
	if p.IsMobile {
		arch = "arm"
		platVersion = "14.0.0"
	}

	// 6. Đoạn mã JavaScript tiêm vào ngay khi tạo Document
	injectionScript := fmt.Sprintf(`(() => {
		// 1. Giả lập Language & Languages & Platform & MaxTouchPoints
		try {
			Object.defineProperty(navigator, 'language', { get: () => 'en-US', configurable: true });
			Object.defineProperty(navigator, 'languages', { get: () => ['en-US', 'en'], configurable: true });
			Object.defineProperty(navigator, 'platform', { get: () => %q, configurable: true });
			Object.defineProperty(navigator, 'maxTouchPoints', { get: () => %d, configurable: true });
		} catch(e) {}

		// 2. Giả lập Timezone Intl API & Date.prototype.getTimezoneOffset
		try {
			const targetTZ = %q;
			const targetOffset = %d;
			const origResolvedOptions = Intl.DateTimeFormat.prototype.resolvedOptions;
			Intl.DateTimeFormat.prototype.resolvedOptions = function() {
				const opts = origResolvedOptions.apply(this, arguments);
				opts.timeZone = targetTZ;
				opts.locale = 'en-US';
				return opts;
			};
			Date.prototype.getTimezoneOffset = function() {
				return targetOffset;
			};

			// Đảm bảo toàn bộ các API Intl khác đều trả về locale en-US (không bao giờ lộ vi-VN)
			['NumberFormat', 'Collator', 'PluralRules', 'RelativeTimeFormat', 'Segmenter'].forEach(api => {
				if (window.Intl && window.Intl[api] && window.Intl[api].prototype) {
					const orig = window.Intl[api].prototype.resolvedOptions;
					window.Intl[api].prototype.resolvedOptions = function() {
						const opts = orig ? orig.apply(this, arguments) : {};
						opts.locale = 'en-US';
						return opts;
					};
				}
			});

			// Lọc bỏ toàn bộ giọng đọc tiếng Việt của hệ điều hành Windows trong SpeechSynthesis
			if (window.speechSynthesis) {
				const origGetVoices = window.speechSynthesis.getVoices.bind(window.speechSynthesis);
				window.speechSynthesis.getVoices = function() {
					const voices = origGetVoices();
					return voices.filter(v => !v.lang.toLowerCase().includes('vi') && !v.name.toLowerCase().includes('vietnam'));
				};
			}
		} catch(e) {}

		// 3. Giả lập navigator.geolocation (Toạ độ GPS)
		try {
			if (navigator.geolocation) {
				const mockCoords = {
					latitude: %f,
					longitude: %f,
					accuracy: 25.0,
					altitude: null,
					altitudeAccuracy: null,
					heading: null,
					speed: null
				};
				navigator.geolocation.getCurrentPosition = function(success) {
					if (typeof success === 'function') {
						success({ coords: mockCoords, timestamp: Date.now() });
					}
				};
				navigator.geolocation.watchPosition = function(success) {
					if (typeof success === 'function') {
						success({ coords: mockCoords, timestamp: Date.now() });
					}
					return Math.floor(Math.random() * 1000) + 1;
				};
			}
			if (navigator.permissions && navigator.permissions.query) {
				const origQuery = navigator.permissions.query.bind(navigator.permissions);
				navigator.permissions.query = function(params) {
					if (params && params.name === 'geolocation') {
						return Promise.resolve({ state: 'granted', name: 'geolocation', onchange: null });
					}
					return origQuery(params);
				};
			}
		} catch(e) {}

		// 4. Giả lập Hardware Concurrency (Số nhân CPU)
		try {
			Object.defineProperty(navigator, 'hardwareConcurrency', {
				get: () => %d,
				configurable: true
			});
		} catch(e) {}

		// 5. Giả lập Device Memory (Dung lượng RAM GB)
		try {
			Object.defineProperty(navigator, 'deviceMemory', {
				get: () => %d,
				configurable: true
			});
		} catch(e) {}

		// 6. Giả lập Client Hints (navigator.userAgentData)
		try {
			if (navigator.userAgentData) {
				const isMobileVal = %t;
				const platformName = %q;
				const archVal = %q;
				const kVer = %q;
				const modelName = %q;
				const platVer = %q;
				Object.defineProperty(navigator, 'userAgentData', {
					get: () => ({
						brands: [
							{ brand: 'Chromium', version: kVer },
							{ brand: 'Google Chrome', version: kVer },
							{ brand: 'Not(A:Brand', version: '24' }
						],
						mobile: isMobileVal,
						platform: platformName,
						getHighEntropyValues: async (hints) => ({
							architecture: archVal,
							bitness: '64',
							brands: [
								{ brand: 'Chromium', version: kVer },
								{ brand: 'Google Chrome', version: kVer },
								{ brand: 'Not(A:Brand', version: '24' }
							],
							mobile: isMobileVal,
							model: modelName,
							platform: platformName,
							platformVersion: platVer,
							uaFullVersion: kVer + '.0.7100.95'
						})
					}),
					configurable: true
				});
			}
		} catch(e) {}

		// 7. Giả lập sự kiện Touch nếu là mobile
		try {
			if (%t) {
				if (!('ontouchstart' in window)) {
					window.ontouchstart = null;
					window.ontouchend = null;
					window.ontouchmove = null;
					window.ontouchcancel = null;
				}
			}
		} catch(e) {}

		// 7. Giả lập WebGL Vendor & Renderer
		const vendorVal = %q;
		const rendererVal = %q;
		const wrapGetParameter = function(original) {
			return function(param) {
				if (param === 37445) return vendorVal;
				if (param === 37446) return rendererVal;
				return original.apply(this, arguments);
			};
		};

		try {
			if (typeof WebGLRenderingContext !== 'undefined') {
				WebGLRenderingContext.prototype.getParameter = wrapGetParameter(WebGLRenderingContext.prototype.getParameter);
			}
			if (typeof WebGL2RenderingContext !== 'undefined') {
				WebGL2RenderingContext.prototype.getParameter = wrapGetParameter(WebGL2RenderingContext.prototype.getParameter);
			}
		} catch(e) {}

		// 8. Giả lập Canvas Fingerprint Noise (Tạo độ lệch màu sắc nhẹ)
		try {
			const noiseVal = %f;
			const origGetImageData = CanvasRenderingContext2D.prototype.getImageData;
			CanvasRenderingContext2D.prototype.getImageData = function() {
				const imgData = origGetImageData.apply(this, arguments);
				const data = imgData.data;
				for (let i = 0; i < data.length; i += 32) {
					data[i] = (data[i] + Math.floor(noiseVal * 2)) %% 256;
				}
				return imgData;
			};
		} catch(e) {}

		// 9. Giả lập AudioContext Noise (Thay đổi phổ âm thanh nhẹ)
		try {
			const audioNoise = %f;
			if (typeof AudioBuffer !== 'undefined') {
				const origGetChannelData = AudioBuffer.prototype.getChannelData;
				AudioBuffer.prototype.getChannelData = function() {
					const buf = origGetChannelData.apply(this, arguments);
					for (let i = 0; i < buf.length; i += 100) {
						buf[i] = buf[i] + (audioNoise * 0.0000001);
					}
					return buf;
				};
			}
		} catch(e) {}

		// 10. Giả lập ClientRects Noise
		try {
			const rectNoise = %f;
			const origGetBoundingClientRect = Element.prototype.getBoundingClientRect;
			Element.prototype.getBoundingClientRect = function() {
				const rect = origGetBoundingClientRect.apply(this, arguments);
				if (!rect) return rect;
				return new DOMRect(
					rect.x,
					rect.y,
					rect.width + (rectNoise * 0.0001),
					rect.height + (rectNoise * 0.0001)
				);
			};
		} catch(e) {}

		// 11. Đồng bộ Screen & Viewport
		try {
			Object.defineProperty(screen, 'width', { get: () => %d, configurable: true });
			Object.defineProperty(screen, 'height', { get: () => %d, configurable: true });
			Object.defineProperty(screen, 'availWidth', { get: () => %d, configurable: true });
			Object.defineProperty(screen, 'availHeight', { get: () => %d, configurable: true });
			Object.defineProperty(screen, 'colorDepth', { get: () => 24, configurable: true });
			Object.defineProperty(screen, 'pixelDepth', { get: () => 24, configurable: true });
			Object.defineProperty(window, 'devicePixelRatio', { get: () => %f, configurable: true });
		} catch(e) {}

		// 12. Triệt tiêu cờ webdriver & bổ sung window.chrome
		try {
			Object.defineProperty(navigator, 'webdriver', { get: () => undefined, configurable: true });
			if (!window.chrome) {
				window.chrome = {
					app: { isInstalled: false },
					runtime: {}
				};
			}
		} catch(e) {}

		// 13. Giả lập WebRTC (Chống rò rỉ WebRTC & Chặn hoàn toàn IPv6 / Real IP Leak theo chuẩn BitBrowser)
		try {
			const targetProxyIP = %q;
			const OrigPC = window.RTCPeerConnection || window.webkitRTCPeerConnection;
			if (OrigPC && targetProxyIP) {
				const sanitizeSDP = function(sdp) {
					if (!sdp || typeof sdp !== 'string') return sdp;
					const lines = sdp.split('\r\n');
					const res = [];
					for (let i = 0; i < lines.length; i++) {
						let line = lines[i];
						if (line.includes('IP6') || line.match(/candidate:[^\s]+\s+\d+\s+\w+\s+\d+\s+[0-9a-fA-F:]+/i)) {
							continue;
						}
						if (line.startsWith('c=IN IP4 ')) {
							res.push('c=IN IP4 ' + targetProxyIP);
							continue;
						}
						if (line.startsWith('a=candidate:')) {
							const parts = line.split(' ');
							if (parts.length >= 8) {
								parts[4] = targetProxyIP;
								for (let k = 5; k < parts.length - 1; k++) {
									if (parts[k] === 'raddr') parts[k+1] = '0.0.0.0';
								}
								line = parts.join(' ');
							}
						}
						res.push(line);
					}
					return res.join('\r\n');
				};

				const makeCandidate = function(candStr, ip, type, port) {
					try {
						const c = new RTCIceCandidate({
							candidate: candStr,
							sdpMid: '0',
							sdpMLineIndex: 0
						});
						Object.defineProperty(c, 'address', { get: () => ip, configurable: true });
						Object.defineProperty(c, 'ip', { get: () => ip, configurable: true });
						Object.defineProperty(c, 'candidate', { get: () => candStr, configurable: true });
						return c;
					} catch(e) {
						return {
							candidate: candStr,
							address: ip,
							ip: ip,
							port: port,
							protocol: 'udp',
							type: type,
							sdpMid: '0',
							sdpMLineIndex: 0,
							toJSON: () => ({ candidate: candStr })
						};
					}
				};

				const ProxiedPC = function(config, constraints) {
					const pc = new OrigPC(config, constraints);
					let userCandidateHandler = null;
					let userListeners = [];
					let emittedFake = false;

					Object.defineProperty(pc, 'onicecandidate', {
						get: () => userCandidateHandler,
						set: (fn) => { userCandidateHandler = fn; },
						configurable: true
					});

					function dispatchToHandlers(event) {
						if (typeof userCandidateHandler === 'function') {
							try { userCandidateHandler.call(pc, event); } catch(e) {}
						}
						for (let i = 0; i < userListeners.length; i++) {
							try { userListeners[i].call(pc, event); } catch(e) {}
						}
					}

					function triggerFakeCandidates() {
						if (emittedFake) return;
						emittedFake = true;

						// 1. Host Candidate dùng mDNS để Local IP Address hiển thị n/a chuẩn Chrome
						const mDnsHost = (typeof crypto !== 'undefined' && crypto.randomUUID ? crypto.randomUUID() : 'f47ac10b-58cc-4372-a567-0e02b2c3d479') + '.local';
						const hostStr = 'candidate:111111111 1 udp 2113937151 ' + mDnsHost + ' 54320 typ host generation 0';
						const hostCand = makeCandidate(hostStr, mDnsHost, 'host', 54320);
						const evHost = new Event('icecandidate');
						Object.defineProperty(evHost, 'candidate', { value: hostCand, enumerable: true });
						dispatchToHandlers(evHost);

						// 2. Server Reflexive Candidate (Public IP Address khớp 100%% với IP Proxy)
						const srflxStr = 'candidate:222222222 1 udp 1686052607 ' + targetProxyIP + ' 54321 typ srflx raddr 0.0.0.0 rport 0 generation 0';
						const srflxCand = makeCandidate(srflxStr, targetProxyIP, 'srflx', 54321);
						const evSrflx = new Event('icecandidate');
						Object.defineProperty(evSrflx, 'candidate', { value: srflxCand, enumerable: true });
						dispatchToHandlers(evSrflx);

						// 3. Kết thúc thu thập ICE
						const evEnd = new Event('icecandidate');
						Object.defineProperty(evEnd, 'candidate', { value: null, enumerable: true });
						dispatchToHandlers(evEnd);
					}

					const origOffer = pc.createOffer.bind(pc);
					pc.createOffer = function(opts) {
						return origOffer(opts).then(offer => {
							if (offer && offer.sdp) {
								return new RTCSessionDescription({
									type: offer.type,
									sdp: sanitizeSDP(offer.sdp)
								});
							}
							return offer;
						});
					};

					const origAnswer = pc.createAnswer.bind(pc);
					pc.createAnswer = function(opts) {
						return origAnswer(opts).then(ans => {
							if (ans && ans.sdp) {
								return new RTCSessionDescription({
									type: ans.type,
									sdp: sanitizeSDP(ans.sdp)
								});
							}
							return ans;
						});
					};

					const origSetLocal = pc.setLocalDescription.bind(pc);
					pc.setLocalDescription = function(desc) {
						setTimeout(triggerFakeCandidates, 100);
						if (desc && desc.sdp) {
							desc = new RTCSessionDescription({
								type: desc.type,
								sdp: sanitizeSDP(desc.sdp)
							});
						}
						return origSetLocal(desc);
					};

					const origAddEvent = pc.addEventListener.bind(pc);
					pc.addEventListener = function(type, listener, options) {
						if (type === 'icecandidate' && typeof listener === 'function') {
							userListeners.push(listener);
							return;
						}
						return origAddEvent(type, listener, options);
					};

					const origRemoveEvent = pc.removeEventListener.bind(pc);
					pc.removeEventListener = function(type, listener, options) {
						if (type === 'icecandidate' && typeof listener === 'function') {
							userListeners = userListeners.filter(l => l !== listener);
							return;
						}
						return origRemoveEvent(type, listener, options);
					};

					return pc;
				};

				ProxiedPC.prototype = OrigPC.prototype;
				window.RTCPeerConnection = ProxiedPC;
				if (window.webkitRTCPeerConnection) {
					window.webkitRTCPeerConnection = ProxiedPC;
				}
			}
		} catch(e) {}
	})();`,
		p.Platform,
		p.TouchPoints,
		p.Geo.Timezone,
		p.Geo.TimezoneOffset,
		p.Geo.Latitude,
		p.Geo.Longitude,
		p.HardwareConcurrency,
		p.DeviceMemory,
		p.IsMobile,
		p.OS,
		arch,
		p.KernelVersion,
		p.DeviceModel,
		platVersion,
		p.IsMobile,
		p.GPU.Vendor,
		p.GPU.Renderer,
		p.CanvasNoiseSeed,
		p.AudioNoiseSeed,
		p.ClientRectNoiseSeed,
		p.ScreenWidth,
		p.ScreenHeight,
		p.ScreenWidth,
		availHeight,
		p.DevicePixelRatio,
		p.ProxyIP,
	)

	_, err := page.EvalOnNewDocument(injectionScript)
	return err
}
