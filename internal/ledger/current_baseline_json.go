package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// currentBaselineJSON implements the draft's sorted JSON encoding. Financial
// decimals must be strings; only int64 JSON numbers are accepted in this store.
func currentBaselineJSON(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) || !json.Valid(raw) || !baselineUnicodeValid(raw) {
		return nil, ErrCurrentBaselineInput
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := baselineJSONValue(d, 0, true)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrCurrentBaselineInput
	}
	var b strings.Builder
	writeBaselineJSON(&b, v)
	return []byte(b.String()), nil
}

// ValidateCurrentBaselineSourceJSON checks the original archive without changing
// decimal tokens. Protocol, scope and coverage still need independent checks.
func ValidateCurrentBaselineSourceJSON(raw []byte) error {
	if len(raw) > currentBaselineMaxBytes || !utf8.Valid(raw) || !json.Valid(raw) || !baselineUnicodeValid(raw) {
		return ErrCurrentBaselineInput
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	_, err := baselineJSONValue(d, 0, false)
	return err
}

func baselineJSONValue(d *json.Decoder, depth int, integersOnly bool) (any, error) {
	if depth > 64 {
		return nil, ErrCurrentBaselineInput
	}
	t, err := d.Token()
	if err != nil {
		return nil, ErrCurrentBaselineInput
	}
	switch t := t.(type) {
	case json.Delim:
		if t == '{' {
			m := map[string]any{}
			for d.More() {
				key, err := d.Token()
				k, ok := key.(string)
				if err != nil || !ok {
					return nil, ErrCurrentBaselineInput
				}
				if _, exists := m[k]; exists {
					return nil, ErrCurrentBaselineInput
				}
				v, err := baselineJSONValue(d, depth+1, integersOnly)
				if err != nil {
					return nil, err
				}
				m[k] = v
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, ErrCurrentBaselineInput
			}
			return m, nil
		}
		if t == '[' {
			v := []any{}
			for d.More() {
				x, err := baselineJSONValue(d, depth+1, integersOnly)
				if err != nil {
					return nil, err
				}
				v = append(v, x)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, ErrCurrentBaselineInput
			}
			return v, nil
		}
		return nil, ErrCurrentBaselineInput
	case json.Number:
		if !integersOnly {
			return t, nil
		}
		n, err := strconv.ParseInt(string(t), 10, 64)
		if err != nil {
			return nil, ErrCurrentBaselineInput
		}
		return n, nil
	default:
		return t, nil
	}
}

func writeBaselineJSON(b *strings.Builder, v any) {
	switch v := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
	case string:
		b.WriteByte('"')
		for _, r := range v {
			switch r {
			case '"', '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if r < 32 {
					fmt.Fprintf(b, `\u%04x`, r)
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteByte('"')
	case []any:
		b.WriteByte('[')
		for i, x := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			writeBaselineJSON(b, x)
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeBaselineJSON(b, k)
			b.WriteByte(':')
			writeBaselineJSON(b, v[k])
		}
		b.WriteByte('}')
	}
}

// encoding/json replaces unpaired UTF-16 escapes with U+FFFD. Reject them so
// evidence is not silently changed before computing its canonical digest.
func baselineUnicodeValid(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
