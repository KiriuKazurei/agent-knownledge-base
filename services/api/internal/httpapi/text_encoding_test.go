package httpapi

import (
	"context"
	"encoding/binary"
	"os"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/config"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func TestDecodeTextBytesPreservesChineseAcrossCommonEncodings(t *testing.T) {
	expected := "# \u4e2d\u6587\u6807\u9898\n\n\u4e2d\u6587\u6b63\u6587\u4e0d\u80fd\u4e71\u7801\u3002\n"
	gb18030, _, err := transform.Bytes(simplifiedchinese.GB18030.NewEncoder(), []byte(expected))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		raw  []byte
	}{
		{name: "utf8-bom", raw: append([]byte{0xEF, 0xBB, 0xBF}, []byte(expected)...)},
		{name: "gb18030", raw: gb18030},
		{name: "utf16le-no-bom", raw: utf16LittleEndian(expected)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			actual, _, err := decodeTextBytes(test.raw)
			if err != nil {
				t.Fatal(err)
			}
			if actual != expected {
				t.Fatalf("decoded text mismatch: %q", actual)
			}
			if strings.ContainsRune(actual, '\uFFFD') || strings.ContainsRune(actual, 0) {
				t.Fatalf("decoded Chinese contains corruption markers: %q", actual)
			}
		})
	}
}

func TestParseDocumentFallbackPreservesGB18030Chinese(t *testing.T) {
	expected := "# \u4e2d\u6587\u6807\u9898\n\n\u964d\u7ea7\u89e3\u6790\u4e5f\u5fc5\u987b\u4fdd\u7559\u4e2d\u6587\u3002\n"
	raw, _, err := transform.Bytes(simplifiedchinese.GB18030.NewEncoder(), []byte(expected))
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/fallback-gb18030.md"
	if err := os.WriteFile(path, raw, 0o640); err != nil {
		t.Fatal(err)
	}
	server := New(config.Config{}, nil, nil, nil)
	chunks, err := server.parseDocument(context.Background(), model.Document{ID: "fallback", MediaType: "text/markdown", SourcePath: path}, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || !strings.Contains(chunks[0].Text, "\u964d\u7ea7\u89e3\u6790\u4e5f\u5fc5\u987b\u4fdd\u7559\u4e2d\u6587\u3002") {
		t.Fatalf("fallback chunks lost Chinese text: %+v", chunks)
	}
}

func utf16LittleEndian(value string) []byte {
	units := utf16.Encode([]rune(value))
	result := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(result[index*2:], unit)
	}
	return result
}
