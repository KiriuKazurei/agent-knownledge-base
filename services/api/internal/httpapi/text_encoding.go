package httpapi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	textencoding "golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	textunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

var errUnsupportedTextEncoding = errors.New("unsupported or invalid text encoding")

type decodedTextCandidate struct {
	name string
	text string
}

func decodeTextBytes(raw []byte) (string, string, error) {
	if len(raw) == 0 {
		return "", "utf-8", nil
	}
	switch {
	case bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}):
		text := raw[3:]
		if !utf8.Valid(text) {
			return "", "", errUnsupportedTextEncoding
		}
		return string(text), "utf-8-sig", nil
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE, 0x00, 0x00}):
		text, err := decodeUTF32(raw[4:], binary.LittleEndian)
		return text, "utf-32le", err
	case bytes.HasPrefix(raw, []byte{0x00, 0x00, 0xFE, 0xFF}):
		text, err := decodeUTF32(raw[4:], binary.BigEndian)
		return text, "utf-32be", err
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}):
		text, err := decodeUTF16(raw[2:], textunicode.LittleEndian)
		return text, "utf-16le", err
	case bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		text, err := decodeUTF16(raw[2:], textunicode.BigEndian)
		return text, "utf-16be", err
	case utf8.Valid(raw):
		return string(raw), "utf-8", nil
	}

	candidates := []decodedTextCandidate{}
	if len(raw) >= 4 && len(raw)%2 == 0 {
		if text, err := decodeUTF16(raw, textunicode.LittleEndian); err == nil {
			candidates = append(candidates, decodedTextCandidate{name: "utf-16le", text: text})
		}
		if text, err := decodeUTF16(raw, textunicode.BigEndian); err == nil {
			candidates = append(candidates, decodedTextCandidate{name: "utf-16be", text: text})
		}
	}
	for _, candidate := range []struct {
		name     string
		encoding textencoding.Encoding
	}{
		{name: "gb18030", encoding: simplifiedchinese.GB18030},
		{name: "big5", encoding: traditionalchinese.Big5},
		{name: "windows-1252", encoding: charmap.Windows1252},
	} {
		if text, err := decodeWithEncoding(raw, candidate.encoding); err == nil {
			candidates = append(candidates, decodedTextCandidate{name: candidate.name, text: text})
		}
	}

	best := decodedTextCandidate{}
	bestScore := 0
	found := false
	for _, candidate := range candidates {
		score := textQualityScore(candidate.text)
		if !found || score > bestScore {
			best, bestScore, found = candidate, score, true
		}
	}
	if !found || strings.ContainsRune(best.text, utf8.RuneError) {
		return "", "", errUnsupportedTextEncoding
	}
	return best.text, best.name, nil
}

func decodeUTF16(raw []byte, endianness textunicode.Endianness) (string, error) {
	decoded, _, err := transform.Bytes(textunicode.UTF16(endianness, textunicode.IgnoreBOM).NewDecoder(), raw)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func decodeUTF32(raw []byte, order binary.ByteOrder) (string, error) {
	if len(raw)%4 != 0 {
		return "", errUnsupportedTextEncoding
	}
	var result strings.Builder
	for index := 0; index < len(raw); index += 4 {
		value := rune(order.Uint32(raw[index : index+4]))
		if value > utf8.MaxRune || value >= 0xD800 && value <= 0xDFFF {
			return "", errUnsupportedTextEncoding
		}
		result.WriteRune(value)
	}
	return result.String(), nil
}

func decodeWithEncoding(raw []byte, codec textencoding.Encoding) (string, error) {
	decoded, _, err := transform.Bytes(codec.NewDecoder(), raw)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func textQualityScore(text string) int {
	score := 0
	for _, value := range text {
		switch {
		case value == utf8.RuneError:
			score -= 10000
		case value == 0:
			score -= 1000
		case unicode.IsControl(value) && value != '\n' && value != '\r' && value != '\t':
			score -= 30
		default:
			score++
		}
		if unicode.Is(unicode.Han, value) {
			score += 8
		}
	}
	return score
}
