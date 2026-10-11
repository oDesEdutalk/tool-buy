package fingerprint

import (
	"fmt"
	"math/rand"

	"github.com/go-rod/rod"
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

var (
	cpuCoresOptions    = []int{4, 8, 12, 16, 24, 32}
	deviceMemoryOpt    = []int{8, 16, 32}
	devicePixelRatios  = []float64{1.0, 1.0, 1.0, 1.25} // Ưu tiên 1.0 phổ biến nhất
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
}

// GenerateRandomProfile tự động sinh một bộ vân tay ngẫu nhiên hoàn toàn độc lập
func GenerateRandomProfile(screenW, screenH int) *BrowserProfile {
	gpu := CommonGPUs[rand.Intn(len(CommonGPUs))]
	cores := cpuCoresOptions[rand.Intn(len(cpuCoresOptions))]
	memory := deviceMemoryOpt[rand.Intn(len(deviceMemoryOpt))]
	dpr := devicePixelRatios[rand.Intn(len(devicePixelRatios))]

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
	}
}

// Summary trả về chuỗi tóm tắt thông số vân tay để in ra log
func (p *BrowserProfile) Summary() string {
	return fmt.Sprintf("CPU: %d cores | RAM: %dGB | DPR: %.2f | GPU: %s",
		p.HardwareConcurrency, p.DeviceMemory, p.DevicePixelRatio, p.GPU.Renderer)
}

// ApplyToPage tiêm đoạn mã JavaScript trước khi tải trang để giả lập vân tay BitBrowser
func (p *BrowserProfile) ApplyToPage(page *rod.Page) error {
	injectionScript := fmt.Sprintf(`(() => {
		// 1. Giả lập Hardware Concurrency (Số nhân CPU)
		try {
			Object.defineProperty(navigator, 'hardwareConcurrency', {
				get: () => %d,
				configurable: true
			});
		} catch(e) {}

		// 2. Giả lập Device Memory (Dung lượng RAM GB)
		try {
			Object.defineProperty(navigator, 'deviceMemory', {
				get: () => %d,
				configurable: true
			});
		} catch(e) {}

		// 3. Giả lập WebGL Vendor & Renderer
		const vendorVal = %q;
		const rendererVal = %q;
		const wrapGetParameter = function(original) {
			return function(param) {
				// UNMASKED_VENDOR_WEBGL = 37445 (0x9245)
				if (param === 37445) return vendorVal;
				// UNMASKED_RENDERER_WEBGL = 37446 (0x9246)
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

		// 4. Giả lập Canvas Fingerprint Noise (Tạo độ lệch màu sắc nhẹ, ngăn tracking)
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

		// 5. Giả lập AudioContext Noise (Thay đổi phổ âm thanh nhẹ)
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

		// 6. Giả lập ClientRects Noise
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

		// 7. Đồng bộ Screen & Viewport
		try {
			Object.defineProperty(screen, 'width', { get: () => %d, configurable: true });
			Object.defineProperty(screen, 'height', { get: () => %d, configurable: true });
			Object.defineProperty(screen, 'availWidth', { get: () => %d, configurable: true });
			Object.defineProperty(screen, 'availHeight', { get: () => %d - 40, configurable: true });
			Object.defineProperty(screen, 'colorDepth', { get: () => 24, configurable: true });
			Object.defineProperty(screen, 'pixelDepth', { get: () => 24, configurable: true });
			Object.defineProperty(window, 'devicePixelRatio', { get: () => %f, configurable: true });
		} catch(e) {}
	})();`,
		p.HardwareConcurrency,
		p.DeviceMemory,
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
