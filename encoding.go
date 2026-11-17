package main

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// textEncoding remembers how a file is stored so the editor can show UTF-8
// text and put the bytes back exactly as they were.
type textEncoding struct {
	Charset string // utf-8 or gbk
	EOL     string // lf or crlf
	BOM     bool
}

func (e textEncoding) label() string {
	charset := "UTF-8"
	if e.Charset == "gbk" {
		charset = "GBK"
	}
	eol := "LF"
	if e.EOL == "crlf" {
		eol = "CRLF"
	}
	return charset + " · " + eol
}

// decodeEditorText turns file bytes into UTF-8 with LF line endings and
// reports the original encoding.
func decodeEditorText(data []byte) (string, textEncoding) {
	enc := textEncoding{Charset: "utf-8", EOL: "lf"}
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		enc.BOM = true
		data = data[3:]
	}
	text := ""
	if utf8.Valid(data) {
		text = string(data)
	} else if decoded, _, err := transform.String(simplifiedchinese.GBK.NewDecoder(), string(data)); err == nil {
		text = decoded
		enc.Charset = "gbk"
	} else {
		text = string(data)
	}
	if strings.Contains(text, "\r\n") {
		enc.EOL = "crlf"
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	return text, enc
}

// encodeEditorText writes the text back with its original charset and line
// endings. A character the charset cannot hold falls back to UTF-8, and the
// returned encoding says so.
func encodeEditorText(text string, enc textEncoding) ([]byte, textEncoding) {
	charset := enc.Charset
	if charset == "" {
		charset = "utf-8"
	}
	if enc.EOL == "crlf" {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	var data []byte
	if charset == "gbk" {
		if encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text)); err == nil {
			data = encoded
		} else {
			charset = "utf-8"
		}
	}
	if data == nil {
		data = []byte(text)
	}
	if enc.BOM {
		data = append([]byte{0xEF, 0xBB, 0xBF}, data...)
	}
	enc.Charset = charset
	return data, enc
}
