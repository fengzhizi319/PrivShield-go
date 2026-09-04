package imageredact

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createSyntheticDICOM() []byte {
	buf := new(bytes.Buffer)

	// 128 字节 preamble
	buf.Write(make([]byte, 128))
	// 4 字节 "DICM"
	buf.WriteString("DICM")

	// Helper to write Explicit VR element
	writeElem := func(group, elem uint16, vr string, val []byte) {
		binary.Write(buf, binary.LittleEndian, group)
		binary.Write(buf, binary.LittleEndian, elem)
		buf.WriteString(vr)
		if isLongVR(vr) {
			buf.Write([]byte{0x00, 0x00})
			binary.Write(buf, binary.LittleEndian, uint32(len(val)))
		} else {
			binary.Write(buf, binary.LittleEndian, uint16(len(val)))
		}
		buf.Write(val)
	}

	// PatientName (0x0010, 0x0010)
	name := []byte("ZHANG^SAN ")
	writeElem(0x0010, 0x0010, "PN", name)

	// PatientID (0x0010, 0x0020)
	id := []byte("110101199001011234")
	writeElem(0x0010, 0x0020, "LO", id)

	// PatientBirthDate (0x0010, 0x0030)
	dob := []byte("19900518")
	writeElem(0x0010, 0x0030, "DA", dob)

	// StudyDescription (0x0008, 0x1030)
	desc := []byte("既往有艾滋病，现诊断为肺腺癌")
	if len(desc)%2 != 0 {
		desc = append(desc, ' ')
	}
	writeElem(0x0008, 0x1030, "LO", desc)

	// PixelData (0x7FE0, 0x0010)
	pixels := []byte{0x10, 0x20, 0x30, 0x40}
	writeElem(0x7FE0, 0x0010, "OW", pixels)

	return buf.Bytes()
}

func TestIsDICOM(t *testing.T) {
	dcm := createSyntheticDICOM()
	if !IsDICOM(dcm) {
		t.Error("IsDICOM returned false for synthetic DICOM")
	}

	if IsDICOM([]byte("not a dicom file")) {
		t.Error("IsDICOM returned true for non-DICOM bytes")
	}
}

func TestAnonymizeDICOM(t *testing.T) {
	dcm := createSyntheticDICOM()

	anonymized, err := AnonymizeDICOM(dcm)
	if err != nil {
		t.Fatalf("AnonymizeDICOM failed: %v", err)
	}

	if !IsDICOM(anonymized) {
		t.Fatal("Anonymized data is no longer valid DICOM")
	}

	anonStr := string(anonymized)
	if strings.Contains(anonStr, "ZHANG^SAN") {
		t.Error("PatientName was not anonymized")
	}
	if strings.Contains(anonStr, "110101199001011234") {
		t.Error("PatientID was not anonymized")
	}
	if strings.Contains(anonStr, "艾滋病") || strings.Contains(anonStr, "肺腺癌") {
		t.Error("Clinical diagnosis terms were not redacted")
	}

	// 确认包含匿名化标志
	if !strings.Contains(anonStr, "ANONYMOUS^PATIENT") {
		t.Error("Missing ANONYMOUS^PATIENT placeholder")
	}
	if !strings.Contains(anonStr, "ANON_") {
		t.Error("Missing ANON_ PatientID prefix")
	}
}

func TestSanitizeDICOMFileAndIntegration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "dicom_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dcm := createSyntheticDICOM()
	inPath := filepath.Join(tmpDir, "patient_001.dcm")
	if err := os.WriteFile(inPath, dcm, 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "sanitized")
	outPath, err := SanitizeImage(inPath, outDir, nil)
	if err != nil {
		t.Fatalf("SanitizeImage on DICOM file failed: %v", err)
	}

	if outPath == FailurePlaceholder {
		t.Fatalf("SanitizeImage returned FailurePlaceholder for valid DICOM")
	}

	outData, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("Read sanitized dicom file: %v", err)
	}

	if !IsDICOM(outData) {
		t.Error("Sanitized file is not valid DICOM")
	}
	if strings.Contains(string(outData), "ZHANG^SAN") {
		t.Error("Sanitized DICOM file still contains patient name")
	}
}
