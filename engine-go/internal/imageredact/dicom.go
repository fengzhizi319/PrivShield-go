// Package imageredact 提供 DICOM 医学影像原生解析与脱敏功能。
//
// 核心能力：
//  1. DICOM 格式检测（128 字节 Preamble + "DICM" 魔数）；
//  2. DICOM 数据元素（Explicit VR / Implicit VR）解析与重构；
//  3. 患者 PII 敏感标签脱敏（姓名、ID、生日、地址、就诊机构、医生姓名）；
//  4. 检查与序列 UID 匿名化（SHA-256 确定性哈希派生）；
//  5. 临床描述脱敏与像素数据（Pixel Data）完整保留。
package imageredact

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/medical"
)

// DICOM 魔数与偏移
const (
	dicomPreambleLen = 128
	dicomMagic       = "DICM"
	// defaultMaxDICOMFileSize 默认 DICOM 文件大小上限（256MB），防止 OOM
	defaultMaxDICOMFileSize = 256 << 20
)

// 常见敏感 DICOM Tag 定义 (Group << 16 | Element)
const (
	TagSOPInstanceUID         uint32 = 0x00080018
	TagStudyDate              uint32 = 0x00080020
	TagStudyTime              uint32 = 0x00080030
	TagInstitutionName        uint32 = 0x00080080
	TagReferringPhysicianName uint32 = 0x00080090
	TagStudyDescription       uint32 = 0x00081030
	TagSeriesDescription      uint32 = 0x0008103E
	TagPatientName            uint32 = 0x00100010
	TagPatientID              uint32 = 0x00100020
	TagPatientBirthDate       uint32 = 0x00100030
	TagPatientSex             uint32 = 0x00100040
	TagPatientAge             uint32 = 0x00101010
	TagPatientAddress         uint32 = 0x00101040
	TagPatientComments        uint32 = 0x00104000
	TagStudyInstanceUID       uint32 = 0x0020000D
	TagSeriesInstanceUID      uint32 = 0x0020000E
	TagPixelData              uint32 = 0x7FE00010
)

// maxDICOMFileSize 返回可配置的 DICOM 文件大小上限。
func maxDICOMFileSize() int64 {
	if env := os.Getenv("PRIVACY_DICOM_MAX_FILE_SIZE"); env != "" {
		if n, err := strconv.ParseInt(env, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxDICOMFileSize
}

// IsDICOM 检查字节流是否为合法的 DICOM 格式。
func IsDICOM(data []byte) bool {
	if len(data) < dicomPreambleLen+4 {
		return false
	}
	return string(data[dicomPreambleLen:dicomPreambleLen+4]) == dicomMagic
}

// IsDICOMFile 检查文件是否为 DICOM 文件。
func IsDICOMFile(path string) bool {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".dcm") || strings.HasSuffix(lower, ".dicom") {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	header := make([]byte, dicomPreambleLen+4)
	n, err := f.Read(header)
	if err != nil || n < dicomPreambleLen+4 {
		return false
	}
	return IsDICOM(header)
}

// AnonymizeDICOM 对 DICOM 二进制数据流执行患者隐私与元数据脱敏。
func AnonymizeDICOM(data []byte) ([]byte, error) {
	if !IsDICOM(data) {
		return nil, fmt.Errorf("invalid DICOM: missing DICM magic header")
	}

	out := bytes.NewBuffer(make([]byte, 0, len(data)))
	// 写入 128 字节 preamble + "DICM"
	out.Write(data[:dicomPreambleLen+4])

	pos := dicomPreambleLen + 4
	dataLen := len(data)

	// 遍历解析并重构 DICOM 元素
	for pos+4 <= dataLen {
		if pos+4 > dataLen {
			break
		}
		group := binary.LittleEndian.Uint16(data[pos : pos+2])
		elem := binary.LittleEndian.Uint16(data[pos+2 : pos+4])
		tag := (uint32(group) << 16) | uint32(elem)
		pos += 4

		// 检查是否为 Explicit VR
		var vr string
		var valLen uint32
		isExplicit := false

		if pos+2 <= dataLen {
			candidateVR := string(data[pos : pos+2])
			if isStandardVR(candidateVR) {
				vr = candidateVR
				isExplicit = true
				pos += 2
				if isLongVR(vr) {
					// 2 字节保留 + 4 字节长度
					if pos+6 <= dataLen {
						pos += 2 // skip reserved
						valLen = binary.LittleEndian.Uint32(data[pos : pos+4])
						pos += 4
					} else {
						break
					}
				} else {
					// 2 字节长度
					if pos+2 <= dataLen {
						valLen = uint32(binary.LittleEndian.Uint16(data[pos : pos+2]))
						pos += 2
					} else {
						break
					}
				}
			}
		}

		if !isExplicit {
			// Implicit VR: 4 字节长度
			if pos+4 <= dataLen {
				valLen = binary.LittleEndian.Uint32(data[pos : pos+4])
				pos += 4
			} else {
				break
			}
		}

		// 遇到未定义长度 (0xFFFFFFFF) 或 PixelData 时，直接写入剩余全部字节
		if valLen == 0xFFFFFFFF || tag == TagPixelData {
			// 写入 tag 与 VR
			writeElementHeader(out, group, elem, vr, isExplicit, uint32(dataLen-pos))
			out.Write(data[pos:])
			break
		}

		if int(pos+int(valLen)) > dataLen {
			valLen = uint32(dataLen - pos)
		}

		valBytes := data[pos : pos+int(valLen)]
		pos += int(valLen)

		// 脱敏处理
		anonymizedVal := anonymizeTagValue(tag, valBytes)

		// 确保偶数字节对齐（DICOM 规范要求）
		if len(anonymizedVal)%2 != 0 {
			anonymizedVal = append(anonymizedVal, ' ')
		}

		// 写回重构元素
		writeElementHeader(out, group, elem, vr, isExplicit, uint32(len(anonymizedVal)))
		out.Write(anonymizedVal)
	}

	return out.Bytes(), nil
}

func writeElementHeader(buf *bytes.Buffer, group, elem uint16, vr string, isExplicit bool, length uint32) {
	_ = binary.Write(buf, binary.LittleEndian, group)
	_ = binary.Write(buf, binary.LittleEndian, elem)

	if isExplicit {
		buf.WriteString(vr)
		if isLongVR(vr) {
			buf.Write([]byte{0x00, 0x00}) // reserved
			_ = binary.Write(buf, binary.LittleEndian, length)
		} else {
			_ = binary.Write(buf, binary.LittleEndian, uint16(length))
		}
	} else {
		_ = binary.Write(buf, binary.LittleEndian, length)
	}
}

func anonymizeTagValue(tag uint32, original []byte) []byte {
	strVal := strings.TrimRight(string(original), " \x00")

	switch tag {
	case TagPatientName:
		return []byte("ANONYMOUS^PATIENT")
	case TagPatientID:
		h := sha256.Sum256([]byte(strVal))
		return []byte(fmt.Sprintf("ANON_%x", h[:4]))
	case TagPatientBirthDate:
		if len(strVal) >= 6 {
			return []byte(strVal[:6] + "01")
		}
		return []byte("19000101")
	case TagPatientAddress, TagReferringPhysicianName, TagInstitutionName:
		return []byte("***")
	case TagPatientAge:
		return []byte("000Y")
	case TagPatientComments:
		return []byte("")
	case TagStudyDescription, TagSeriesDescription:
		redacted := medical.RedactMedicalText(strVal)
		return []byte(redacted)
	case TagStudyInstanceUID, TagSeriesInstanceUID, TagSOPInstanceUID:
		h := sha256.Sum256([]byte(strVal))
		return []byte(fmt.Sprintf("1.2.826.0.1.3680043.9.%x", h[:8]))
	default:
		return original
	}
}

func isStandardVR(vr string) bool {
	switch vr {
	case "AE", "AS", "AT", "CS", "DA", "DS", "DT", "FL", "FD", "IS", "LO", "LT",
		"OB", "OF", "OW", "PN", "SH", "SL", "SQ", "SS", "ST", "TM", "UI", "UL", "UN", "US", "UT":
		return true
	default:
		return false
	}
}

func isLongVR(vr string) bool {
	switch vr {
	case "OB", "OW", "OF", "SQ", "UT", "UN":
		return true
	default:
		return false
	}
}

// SanitizeDICOMFile 对磁盘上的 DICOM 文件执行脱敏并输出到安全沙箱目录。
func SanitizeDICOMFile(inputPath string, outputDir string) (string, error) {
	if !isPathAllowed(inputPath) {
		return "", fmt.Errorf("access denied: path outside allowed directories: %s", inputPath)
	}

	// 文件大小上限检查（防 OOM）
	info, err := os.Stat(inputPath)
	if err != nil {
		return "", fmt.Errorf("stat dicom file: %w", err)
	}
	if info.Size() > maxDICOMFileSize() {
		return "", fmt.Errorf("dicom file too large: %d bytes (max %d)", info.Size(), maxDICOMFileSize())
	}

	data, err := os.ReadFile(inputPath)
	if err != nil {
		return "", fmt.Errorf("read dicom file: %w", err)
	}

	anonymized, err := AnonymizeDICOM(data)
	if err != nil {
		return "", fmt.Errorf("anonymize dicom: %w", err)
	}

	if outputDir == "" {
		outputDir = "data/sanitized_images"
	}
	_ = os.MkdirAll(outputDir, 0755)

	baseName := filepath.Base(inputPath)
	h := sha256.Sum256([]byte(baseName))
	outName := fmt.Sprintf("sanitized_%x%s", h[:6], filepath.Ext(baseName))
	if !strings.HasSuffix(strings.ToLower(outName), ".dcm") {
		outName += ".dcm"
	}
	outPath := filepath.Join(outputDir, outName)

	if err := os.WriteFile(outPath, anonymized, 0644); err != nil {
		return "", fmt.Errorf("write anonymized dicom: %w", err)
	}

	return outPath, nil
}
