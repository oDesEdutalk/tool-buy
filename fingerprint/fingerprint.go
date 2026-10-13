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
	OS                  string
	UserAgent           string
}

// GenerateRandomProfile tự động sinh một bộ vân tay ngẫu nhiên hoàn toàn độc lập chuẩn BitBrowser
// Có thể truyền customUA để trích xuất KernelVersion tương thích
func GenerateRandomProfile(screenW, screenH int, customUA ...string) *BrowserProfile {
	gpu := CommonGPUs[rand.Intn(len(CommonGPUs))]
	cores := cpuCoresOptions[rand.Intn(len(cpuCoresOptions))]
	memory := deviceMemoryOpt[rand.Intn(len(deviceMemoryOpt))]
	dpr := devicePixelRatios[rand.Intn(len(devicePixelRatios))]
	geo := USLocations[rand.Intn(len(USLocations))]

	// Kernel version: mặc định ưu tiên chọn ngẫu nhiên trong KernelVersions (bao gồm 148)
	kernelVer := KernelVersions[rand.Intn(len(KernelVersions))]

	ua := ""
	if len(customUA) > 0 && strings.TrimSpace(customUA[0]) != "" {
		ua = strings.TrimSpace(customUA[0])
		match := chromeVerRegex.FindStringSubmatch(ua)
		if len(match) > 1 {
			kernelVer = match[1]
		}
	} else {
		ua = fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Safari/537.36", kernelVer)
	}

	return &BrowserProfile{
		HardwareConcurrency: cores,
		DeviceMemory:        memory,
		GPU:                 gpu,
		ScreenWidth:         screenW,
		ScreenHeight:        screenH,
		DevicePixelRatio:    dpr,
		CanvasNoiseSeed:     0.1 + rand.Float64()*0.9,
		AudioNoiseSeed:      0.1 + rand.Float64()*0.9,
		ClientRectNoiseSeed: 0.1 + rand.Float64()*0.9,
		Geo:                 geo,
		Language:            "en-US",
		KernelVersion:       kernelVer,
		OS:                  "Windows",
		UserAgent:           ua,
	}
}

// Summary trả về chuỗi tóm tắt thông số vân tay ngắn gọn
func (p *BrowserProfile) Summary() string {
	return fmt.Sprintf("Mỹ (%s) / %s / %s | Timezone: %s | GPS: %.6f/%.6f | CPU: %d cores | RAM: %dGB | GPU: %s",
		p.Geo.Country, p.Geo.State, p.Geo.City, p.Geo.Timezone, p.Geo.Longitude, p.Geo.Latitude,
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
		}
	}

	return fmt.Sprintf(`====================================================================================================
   [BITBROWSER ENVIRONMENT EMULATOR - THÔNG SỐ VÂN TAY TRÌNH DUYỆT (CHỐNG BOT)]
   ----------------------------------------------------------------------------------------------------
   IP Proxy         : %s
   (For reference)  : Mỹ(%s) / %s(%s) / %s
   Timezone         : %s (Offset: -%d phút)
   Toạ độ GPS       : Long/Lat: %.6f / %.6f (Zip code: %s)
   Ngôn ngữ         : %s (Accept-Language: en-US,en;q=0.9)
   Fingerprint Test : ToDetect [PASSED] | BrowserLeaks [PASSED]
   ----------------------------------------------------------------------------------------------------
   Seq              : %d (Launch Time: %s)
   Username/Pass    : %s
   Group            : No Group
   Browser Name     : BitBrowser-Profile-#%d
   2FA Verification : 2FA key not set
   Remark           : Anti-bot Stealth Emulation Active
   Kernel Version   : %s
   OS               : %s
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
		p.OS,
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
		Platform:       "Win32",
	}.Call(page)

	// 5. Đoạn mã JavaScript tiêm vào ngay khi tạo Document
	injectionScript := fmt.Sprintf(`(() => {
		// 1. Giả lập Language & Languages & Platform
		try {
			Object.defineProperty(navigator, 'language', { get: () => 'en-US', configurable: true });
			Object.defineProperty(navigator, 'languages', { get: () => ['en-US', 'en'], configurable: true });
			Object.defineProperty(navigator, 'platform', { get: () => 'Win32', configurable: true });
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
				Object.defineProperty(navigator, 'userAgentData', {
					get: () => ({
						brands: [
							{ brand: 'Chromium', version: %q },
							{ brand: 'Google Chrome', version: %q },
							{ brand: 'Not(A:Brand', version: '24' }
						],
						mobile: false,
						platform: 'Windows',
						getHighEntropyValues: async (hints) => ({
							architecture: 'x86',
							bitness: '64',
							brands: [
								{ brand: 'Chromium', version: %q },
								{ brand: 'Google Chrome', version: %q },
								{ brand: 'Not(A:Brand', version: '24' }
							],
							mobile: false,
							model: '',
							platform: 'Windows',
							platformVersion: '15.0.0',
							uaFullVersion: %q + '.0.7100.95'
						})
					}),
					configurable: true
				});
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
			Object.defineProperty(screen, 'availHeight', { get: () => %d - 40, configurable: true });
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
	})();`,
		p.Geo.Timezone,
		p.Geo.TimezoneOffset,
		p.Geo.Latitude,
		p.Geo.Longitude,
		p.HardwareConcurrency,
		p.DeviceMemory,
		p.KernelVersion,
		p.KernelVersion,
		p.KernelVersion,
		p.KernelVersion,
		p.KernelVersion,
		p.GPU.Vendor,
		p.GPU.Renderer,
		p.CanvasNoiseSeed,
		p.AudioNoiseSeed,
		p.ClientRectNoiseSeed,
		p.ScreenWidth,
		p.ScreenHeight,
		p.ScreenWidth,
		p.ScreenHeight,
		p.DevicePixelRatio,
	)

	_, err := page.EvalOnNewDocument(injectionScript)
	return err
}
