package tools

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// utf8Encoding represents detected file encoding.
type utf8Encoding string

const (
	utf8EncUTF8       utf8Encoding = "utf8"
	utf8EncUTF8BOM    utf8Encoding = "utf8_bom"
	utf8EncUTF16LE    utf8Encoding = "utf16le"
	utf8EncUTF16BE    utf8Encoding = "utf16be"
	utf8EncUTF16LEBOM utf8Encoding = "utf16le_bom"
	utf8EncUTF16BEBOM utf8Encoding = "utf16be_bom"
	utf8EncNullLaced  utf8Encoding = "null_laced"
	utf8EncMixed      utf8Encoding = "mixed"
)

// utf8Issue describes an encoding problem found.
type utf8Issue string

const (
	utf8IssueBOM               utf8Issue = "bom"
	utf8IssueNullLacing        utf8Issue = "null_lacing"
	utf8IssueUnpairedSurrogate utf8Issue = "unpaired_surrogate"
	utf8IssueMixedEncoding     utf8Issue = "mixed_encoding"
)

// utf8Detection holds encoding analysis results.
type utf8Detection struct {
	encoding      utf8Encoding
	issues        []utf8Issue
	byteOrderMark bool
	nullLaced     bool
	mixed         bool
}

// utf8Result is the JSON output.
type utf8Result struct {
	File     string       `json:"file"`
	Detected utf8Encoding `json:"detected"`
	Issues   []utf8Issue  `json:"issues"`
	BytesIn  int          `json:"bytes_in"`
	BytesOut int          `json:"bytes_out"`
	Backup   string       `json:"backup,omitempty"`
	Status   string       `json:"status"`
}

const utf8SampleSize = 4096

// UTF8Def detects and fixes corrupted UTF-16 files.
var UTF8Def = ToolDef{
	Name:        "utf8",
	Description: "Detect and fix corrupted UTF-16 files by converting to clean UTF-8 in-place. Handles null-laced ASCII, missing BOMs, mixed encoding, and unpaired surrogates.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to fix.",
			},
			"backup": map[string]any{
				"type":        "boolean",
				"default":     true,
				"description": "Create a .bak backup before modifying.",
			},
		},
		"required": []string{"file"},
	},
	Builtin: builtinUTF8,
	Timeout: 10 * time.Second,
}

func builtinUTF8(ctx context.Context, input map[string]any, workDir string) Result {
	filePath, ok := input["file"].(string)
	if !ok || filePath == "" {
		return Result{IsError: true, Error: "file is required"}
	}
	filePath = resolvePath(filePath, workDir)

	backup := true
	if v, ok := input["backup"].(bool); ok {
		backup = v
	}

	r, err := utf8FixFile(filePath, backup)
	if err != nil {
		return Result{IsError: true, Error: err.Error()}
	}

	return resultJSON(r)
}

func utf8FixFile(path string, backup bool) (*utf8Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	det := utf8Detect(data)

	if det.encoding == utf8EncUTF8 {
		return &utf8Result{
			File:     path,
			Detected: utf8EncUTF8,
			Issues:   []utf8Issue{},
			BytesIn:  len(data),
			BytesOut: len(data),
			Status:   "already_utf8",
		}, nil
	}

	converted, issues := utf8Convert(data, det)
	allIssues := append(det.issues, issues...)

	result := &utf8Result{
		File:     path,
		Detected: det.encoding,
		Issues:   allIssues,
		BytesIn:  len(data),
		BytesOut: len(converted),
		Status:   "converted",
	}

	if backup {
		backupPath := path + ".bak"
		if writeErr := os.WriteFile(backupPath, data, 0644); writeErr != nil {
			return nil, fmt.Errorf("writing backup: %w", writeErr)
		}
		result.Backup = backupPath
	}

	if writeErr := AtomicWrite(path, converted); writeErr != nil {
		return nil, writeErr
	}

	return result, nil
}

func utf8Detect(data []byte) utf8Detection {
	det := utf8Detection{issues: []utf8Issue{}}

	if len(data) == 0 {
		det.encoding = utf8EncUTF8
		return det
	}

	// BOM detection
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		det.encoding = utf8EncUTF8BOM
		det.byteOrderMark = true
		det.issues = append(det.issues, utf8IssueBOM)
		return det
	}
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		det.encoding = utf8EncUTF16LEBOM
		det.byteOrderMark = true
		det.issues = append(det.issues, utf8IssueBOM)
		return det
	}
	if len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF {
		det.encoding = utf8EncUTF16BEBOM
		det.byteOrderMark = true
		det.issues = append(det.issues, utf8IssueBOM)
		return det
	}

	// Null byte pattern analysis
	analysis := utf8AnalyzeNulls(data)

	if analysis.isNullLaced {
		det.encoding = utf8EncNullLaced
		det.nullLaced = true
		det.issues = append(det.issues, utf8IssueNullLacing)
		return det
	}
	if analysis.isUTF16LE {
		det.encoding = utf8EncUTF16LE
		return det
	}
	if analysis.isUTF16BE {
		det.encoding = utf8EncUTF16BE
		return det
	}

	if utf8.Valid(data) {
		det.encoding = utf8EncUTF8
		return det
	}

	// Mixed encoding check
	segments := utf8FindSegments(data)
	if len(segments) > 1 {
		det.encoding = utf8EncMixed
		det.mixed = true
		det.issues = append(det.issues, utf8IssueMixedEncoding)
		return det
	}

	det.encoding = utf8EncUTF16LE // fallback
	return det
}

type utf8NullAnalysis struct {
	isNullLaced bool
	isUTF16LE   bool
	isUTF16BE   bool
}

func utf8AnalyzeNulls(data []byte) utf8NullAnalysis {
	sample := data
	if len(sample) > utf8SampleSize {
		sample = sample[:utf8SampleSize]
	}
	if len(sample) < 2 {
		return utf8NullAnalysis{}
	}

	sampleLen := len(sample)
	if sampleLen%2 != 0 {
		sampleLen--
	}
	pairs := sampleLen / 2
	if pairs == 0 {
		return utf8NullAnalysis{}
	}

	oddNulls := 0
	evenNulls := 0
	evenAllASCII := true

	for i := 0; i < sampleLen; i += 2 {
		lo := sample[i]
		hi := sample[i+1]
		if hi == 0 {
			oddNulls++
		}
		if lo == 0 {
			evenNulls++
		}
		if lo > 127 {
			evenAllASCII = false
		}
	}

	oddRatio := float64(oddNulls) / float64(pairs)
	evenRatio := float64(evenNulls) / float64(pairs)

	if oddRatio > 0.80 && evenAllASCII {
		return utf8NullAnalysis{isNullLaced: true}
	}
	if oddRatio > 0.30 {
		return utf8NullAnalysis{isUTF16LE: true}
	}
	if evenRatio > 0.30 {
		return utf8NullAnalysis{isUTF16BE: true}
	}

	return utf8NullAnalysis{}
}

func utf8Convert(data []byte, det utf8Detection) ([]byte, []utf8Issue) {
	switch det.encoding {
	case utf8EncUTF8:
		return data, nil
	case utf8EncUTF8BOM:
		return data[3:], nil
	case utf8EncUTF16LEBOM:
		return utf8DecodeUTF16(data[2:], binary.LittleEndian)
	case utf8EncUTF16BEBOM:
		return utf8DecodeUTF16(data[2:], binary.BigEndian)
	case utf8EncUTF16LE:
		return utf8DecodeUTF16(data, binary.LittleEndian)
	case utf8EncUTF16BE:
		return utf8DecodeUTF16(data, binary.BigEndian)
	case utf8EncNullLaced:
		return utf8StripNullLacing(data), nil
	case utf8EncMixed:
		return utf8ConvertMixed(data)
	default:
		return data, nil
	}
}

func utf8DecodeUTF16(data []byte, order binary.ByteOrder) ([]byte, []utf8Issue) {
	if len(data) < 2 {
		return data, nil
	}

	dataLen := len(data)
	if dataLen%2 != 0 {
		dataLen--
	}

	units := make([]uint16, dataLen/2)
	for i := range units {
		units[i] = order.Uint16(data[i*2 : i*2+2])
	}

	// Check for unpaired surrogates
	var issues []utf8Issue
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xD800 && u <= 0xDBFF {
			if i+1 >= len(units) || units[i+1] < 0xDC00 || units[i+1] > 0xDFFF {
				issues = append(issues, utf8IssueUnpairedSurrogate)
				break
			}
			i++
		} else if u >= 0xDC00 && u <= 0xDFFF {
			issues = append(issues, utf8IssueUnpairedSurrogate)
			break
		}
	}

	runes := utf16.Decode(units)

	var buf bytes.Buffer
	buf.Grow(len(runes) * 2)
	for _, r := range runes {
		buf.WriteRune(r)
	}

	return buf.Bytes(), issues
}

func utf8StripNullLacing(data []byte) []byte {
	result := make([]byte, 0, len(data)/2+1)
	for i := 0; i < len(data); i += 2 {
		result = append(result, data[i])
	}
	return result
}

type utf8Segment struct {
	start    int
	end      int
	encoding utf8Encoding
}

func utf8FindSegments(data []byte) []utf8Segment {
	if len(data) == 0 {
		return nil
	}

	chunkSize := 256
	if len(data) < chunkSize {
		return []utf8Segment{{start: 0, end: len(data), encoding: utf8ClassifyChunk(data)}}
	}

	var segments []utf8Segment
	current := utf8Segment{start: 0, encoding: utf8ClassifyChunk(data[0:min(chunkSize, len(data))])}

	for offset := chunkSize; offset < len(data); offset += chunkSize {
		end := min(offset+chunkSize, len(data))
		chunkEnc := utf8ClassifyChunk(data[offset:end])

		if chunkEnc != current.encoding {
			current.end = offset
			segments = append(segments, current)
			current = utf8Segment{start: offset, encoding: chunkEnc}
		}
	}

	current.end = len(data)
	segments = append(segments, current)

	return segments
}

func utf8ClassifyChunk(chunk []byte) utf8Encoding {
	if len(chunk) < 2 {
		return utf8EncUTF8
	}

	nullCount := 0
	for _, b := range chunk {
		if b == 0 {
			nullCount++
		}
	}

	nullRatio := float64(nullCount) / float64(len(chunk))
	if nullRatio > 0.20 {
		analysis := utf8AnalyzeNulls(chunk)
		if analysis.isNullLaced {
			return utf8EncNullLaced
		}
		if analysis.isUTF16LE {
			return utf8EncUTF16LE
		}
		if analysis.isUTF16BE {
			return utf8EncUTF16BE
		}
	}

	if utf8.Valid(chunk) {
		return utf8EncUTF8
	}

	return utf8EncUTF16LE
}

func utf8ConvertMixed(data []byte) ([]byte, []utf8Issue) {
	segments := utf8FindSegments(data)
	var result bytes.Buffer
	allIssues := []utf8Issue{utf8IssueMixedEncoding}

	for _, seg := range segments {
		chunk := data[seg.start:seg.end]
		det := utf8Detection{encoding: seg.encoding, issues: []utf8Issue{}}
		converted, issues := utf8Convert(chunk, det)
		result.Write(converted)
		allIssues = append(allIssues, issues...)
	}

	return result.Bytes(), allIssues
}
