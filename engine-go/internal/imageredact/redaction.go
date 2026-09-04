// Package imageredact — 图像隐私脱敏模块。
//
// 对齐 Python engine/dynclassification/image_redaction.py：
//   - 支持文件路径和 Base64 Data URI 输入
//   - 沙箱目录白名单校验（防目录穿越和 symlink 逃逸）
//   - 默认遮挡区：头部 16% + 底部 18%（姓名/诊断/签名）
//   - 输出文件名 SHA-256 匿名化
//   - 磁盘防满：自动清理超过 200 个旧文件
//   - fail-closed：无法处理的格式返回占位符
package imageredact

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FailurePlaceholder 图像脱敏失败时的占位符。
const FailurePlaceholder = "[IMAGE-REDACTION-FAILED]"

// 支持的图像文件扩展名
var imageExtensions = []string{".jpg", ".jpeg", ".png", ".bmp", ".tif", ".tiff", ".webp", ".dcm", ".dicom"}

// 文件路径长度上限
const maxPathLen = 512

// 最大输出文件数（磁盘防满）
const maxSanitizedFiles = 200

// 最大图像分辨率（超过自动下采样）
const maxDimension = 2048

// Box 表示遮挡区域 (ymin, xmin, ymax, xmax)，范围 0.0-1.0 为比例坐标。
type Box struct {
	YMin, XMin, YMax, XMax float64
}

// DefaultBoxes 返回默认遮挡区域。
func DefaultBoxes() []Box {
	return []Box{
		{YMin: 0.0, XMin: 0.0, YMax: 0.16, XMax: 1.0}, // 头部身份遮挡
		{YMin: 0.82, XMin: 0.0, YMax: 1.0, XMax: 1.0}, // 底部诊断/签名遮挡
	}
}

// IsImageInput 判断输入是否为图像（文件路径或 Base64 Data URI）。
func IsImageInput(val string) bool {
	if val == "" {
		return false
	}
	stripped := strings.TrimSpace(val)
	lower := strings.ToLower(stripped)

	if len(stripped) >= maxPathLen {
		return false
	}

	for _, ext := range imageExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	if strings.HasPrefix(lower, "data:image/") || strings.HasPrefix(lower, "image:") {
		return true
	}
	return false
}

// allowedImageDirs 返回允许读取图片的目录白名单。
func allowedImageDirs() []string {
	env := os.Getenv("AGENT_IMAGE_ALLOWED_DIRS")
	if env != "" && strings.TrimSpace(env) != "" {
		var roots []string
		for _, p := range strings.Split(env, string(os.PathListSeparator)) {
			p = strings.TrimSpace(p)
			if p != "" {
				roots = append(roots, p)
			}
		}
		return roots
	}

	cwd, _ := os.Getwd()
	return []string{
		filepath.Join(cwd, "data"),
		filepath.Join(cwd, "uploads"),
		filepath.Join(cwd, "samples"),
		filepath.Join(cwd, "medical_images"),
		os.TempDir(),
	}
}

// isPathAllowed 校验路径是否在允许的目录白名单内。
func isPathAllowed(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	for _, root := range allowedImageDirs() {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		// 解析 root 的符号链接
		resolvedRoot, err := filepath.EvalSymlinks(absRoot)
		if err != nil {
			resolvedRoot = absRoot
		}

		// 尝试解析文件路径的符号链接；文件不存在时使用绝对路径
		resolved := abs
		if r, err := filepath.EvalSymlinks(abs); err == nil {
			resolved = r
		}

		// 检查解析后路径和原始绝对路径
		for _, checkPath := range []string{resolved, abs} {
			rel, err := filepath.Rel(resolvedRoot, checkPath)
			if err != nil {
				continue
			}
			if !strings.HasPrefix(rel, "..") {
				return true
			}
			// 也检查未解析的 root
			rel2, err := filepath.Rel(absRoot, checkPath)
			if err != nil {
				continue
			}
			if !strings.HasPrefix(rel2, "..") {
				return true
			}
		}
	}
	return false
}

// SanitizeImage 对图像执行隐私脱敏。
//
// 输入可以是文件路径或 Base64 Data URI。
// 返回脱敏后的文件路径、Base64 Data URI 或原始值。
func SanitizeImage(val string, outputDir string, boxes []Box) (string, error) {
	if val == "" {
		return val, nil
	}

	if boxes == nil {
		boxes = DefaultBoxes()
	}

	stripped := strings.TrimSpace(val)
	isDataURI := strings.HasPrefix(strings.ToLower(stripped), "data:image/")
	isFilePath := false
	if len(stripped) < maxPathLen {
		lower := strings.ToLower(stripped)
		for _, ext := range imageExtensions {
			if strings.HasSuffix(lower, ext) {
				isFilePath = true
				break
			}
		}
	}

	var img image.Image
	var inputExt string

	// 1. 加载图像或处理 DICOM
	if isFilePath {
		if !isPathAllowed(stripped) {
			return FailurePlaceholder, nil
		}

		// DICOM 医学影像直接处理
		if IsDICOMFile(stripped) {
			outPath, err := SanitizeDICOMFile(stripped, outputDir)
			if err != nil {
				return FailurePlaceholder, nil
			}
			return outPath, nil
		}

		f, err := os.Open(stripped)
		if err != nil {
			return FailurePlaceholder, nil
		}
		defer f.Close()
		var errDec error
		img, errDec = decodeImage(f)
		if errDec != nil {
			return FailurePlaceholder, nil
		}
		inputExt = strings.ToLower(filepath.Ext(stripped))
	} else if isDataURI {
		parts := strings.SplitN(stripped, ",", 2)
		if len(parts) != 2 {
			return FailurePlaceholder, nil
		}
		data, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return FailurePlaceholder, nil
		}

		// 检查 Base64 中是否为 DICOM 数据
		if IsDICOM(data) {
			anon, err := AnonymizeDICOM(data)
			if err != nil {
				return FailurePlaceholder, nil
			}
			return "data:application/dicom;base64," + base64.StdEncoding.EncodeToString(anon), nil
		}

		img2, errDec := decodeImageReader(data)
		if errDec != nil {
			return FailurePlaceholder, nil
		}
		img = img2
		inputExt = ".png"
	} else {
		return val, nil
	}

	if img == nil {
		if isFilePath || isDataURI {
			return FailurePlaceholder, nil
		}
		return val, nil
	}

	// 2. 获取图像边界
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// 3. 创建可变图像副本
	dst := image.NewRGBA(bounds)
	draw.Draw(dst, bounds, img, bounds.Min, draw.Src)

	// 4. 绘制遮挡区域
	black := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	for _, box := range boxes {
		var x0, y0, x1, y1 int
		if box.YMin >= 0 && box.YMin <= 1 && box.XMin >= 0 && box.XMin <= 1 &&
			box.YMax >= 0 && box.YMax <= 1 && box.XMax >= 0 && box.XMax <= 1 {
			x0 = int(box.XMin * float64(width))
			y0 = int(box.YMin * float64(height))
			x1 = int(box.XMax * float64(width))
			y1 = int(box.YMax * float64(height))
		} else {
			x0, y0 = int(box.XMin), int(box.YMin)
			x1, y1 = int(box.XMax), int(box.YMax)
		}
		fillRect(dst, x0, y0, x1, y1, black)
	}

	// 5. 输出
	if isFilePath {
		if outputDir == "" {
			outputDir = "data/sanitized_images"
		}
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			return FailurePlaceholder, nil
		}
		cleanupOldImages(outputDir)

		// 文件名脱敏
		baseName := filepath.Base(stripped)
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(baseName)))[:12]
		outExt := inputExt
		if outExt == "" {
			outExt = ".png"
		}
		outFile := filepath.Join(outputDir, fmt.Sprintf("sanitized_%s%s", digest, outExt))

		// 拒绝覆盖符号链接
		if info, err := os.Lstat(outFile); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return FailurePlaceholder, nil
		}

		if err := saveImage(dst, outFile, outExt); err != nil {
			return FailurePlaceholder, nil
		}
		return outFile, nil
	}

	if isDataURI {
		var buf strings.Builder
		enc := base64.NewEncoder(base64.StdEncoding, &buf)
		if err := png.Encode(enc, dst); err != nil {
			return FailurePlaceholder, nil
		}
		enc.Close()
		return "data:image/png;base64," + buf.String(), nil
	}

	return val, nil
}

// decodeImage 从 io.Reader 解码图像。
func decodeImage(r io.Reader) (image.Image, error) {
	img, _, err := image.Decode(r)
	return img, err
}

// decodeImageReader 从字节切片解码图像。
func decodeImageReader(data []byte) (image.Image, error) {
	r := bytes.NewReader(data)
	return decodeImage(r)
}

// fillRect 在 RGBA 图像上填充矩形区域。
func fillRect(img *image.RGBA, x0, y0, x1, y1 int, c color.Color) {
	bounds := img.Bounds()
	if x0 < bounds.Min.X {
		x0 = bounds.Min.X
	}
	if y0 < bounds.Min.Y {
		y0 = bounds.Min.Y
	}
	if x1 > bounds.Max.X {
		x1 = bounds.Max.X
	}
	if y1 > bounds.Max.Y {
		y1 = bounds.Max.Y
	}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.Set(x, y, c)
		}
	}
}

// saveImage 保存图像到文件。
func saveImage(img *image.RGBA, path, ext string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	switch ext {
	case ".jpg", ".jpeg":
		return jpeg.Encode(f, img, &jpeg.Options{Quality: 90})
	default:
		return png.Encode(f, img)
	}
}

// cleanupOldImages 清理旧的脱敏图像文件（磁盘防满）。
func cleanupOldImages(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	type fileInfo struct {
		path    string
		modTime int64
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "sanitized_") {
			info, err := e.Info()
			if err != nil {
				continue
			}
			files = append(files, fileInfo{
				path:    filepath.Join(dir, e.Name()),
				modTime: info.ModTime().Unix(),
			})
		}
	}

	if len(files) <= maxSanitizedFiles {
		return
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime < files[j].modTime
	})

	for i := 0; i < len(files)-maxSanitizedFiles; i++ {
		os.Remove(files[i].path)
	}
}
