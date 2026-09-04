package imageredact

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestIsImageInput(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"photo.jpg", true},
		{"scan.PNG", true},
		{"report.dcm", true},
		{"data:image/png;base64,iVBOR", true},
		{"image:something", true},
		{"hello world", false},
		{"", false},
		{"document.pdf", false},
	}

	for _, tt := range tests {
		got := IsImageInput(tt.input)
		if got != tt.expected {
			t.Errorf("IsImageInput(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestSanitizeImage_NonImage(t *testing.T) {
	result, err := SanitizeImage("hello world", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello world" {
		t.Errorf("expected original value for non-image, got %q", result)
	}
}

func TestSanitizeImage_EmptyInput(t *testing.T) {
	result, err := SanitizeImage("", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestSanitizeImage_DisallowedPath(t *testing.T) {
	result, err := SanitizeImage("/etc/passwd.jpg", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != FailurePlaceholder {
		t.Errorf("expected failure placeholder for disallowed path, got %q", result)
	}
}

func TestSanitizeImage_ValidImagePath(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("AGENT_IMAGE_ALLOWED_DIRS", dir)
	defer os.Unsetenv("AGENT_IMAGE_ALLOWED_DIRS")

	imgPath := filepath.Join(dir, "test.png")
	createTestPNG(t, imgPath)

	outDir := filepath.Join(dir, "output")
	result, err := SanitizeImage(imgPath, outDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == FailurePlaceholder {
		t.Error("expected successful redaction for valid image")
	}
	// 验证输出文件存在
	if _, err := os.Stat(result); err != nil {
		t.Errorf("output file does not exist: %s", result)
	}
}

func TestSanitizeImage_Base64DataURI(t *testing.T) {
	var buf bytes.Buffer
	img := createTestImage()
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	b64 := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	result, err := SanitizeImage(b64, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == FailurePlaceholder {
		t.Error("expected successful redaction for valid base64 image")
	}
	if result == b64 {
		t.Error("expected modified output, got unchanged input")
	}
}

func TestIsPathAllowed(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("AGENT_IMAGE_ALLOWED_DIRS", dir)
	defer os.Unsetenv("AGENT_IMAGE_ALLOWED_DIRS")

	allowed := filepath.Join(dir, "photo.jpg")
	if !isPathAllowed(allowed) {
		t.Errorf("expected %s to be allowed", allowed)
	}

	if isPathAllowed("/etc/passwd") {
		t.Error("expected /etc/passwd to be denied")
	}
}

func TestCleanupOldImages(t *testing.T) {
	dir := t.TempDir()

	for i := 0; i < 5; i++ {
		name := filepath.Join(dir, "sanitized_test_"+string(rune('0'+i))+".png")
		os.WriteFile(name, []byte("test"), 0o644)
	}

	cleanupOldImages(dir)

	entries, _ := os.ReadDir(dir)
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			count++
		}
	}
	// 5 个文件 < maxSanitizedFiles(200)，不应删除
	if count != 5 {
		t.Errorf("expected 5 files (under limit), got %d", count)
	}
}

func TestDefaultBoxes(t *testing.T) {
	boxes := DefaultBoxes()
	if len(boxes) != 2 {
		t.Errorf("expected 2 default boxes, got %d", len(boxes))
	}
}

// ──────────────────────────────────────────────
// 辅助函数
// ──────────────────────────────────────────────

func createTestImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	return img
}

func createTestPNG(t *testing.T, path string) {
	t.Helper()
	img := createTestImage()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
