package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// NativeRecord is the Java LoganLogItem.transferToDetail projection. Nil content
// preserves missing and JSON null values.
type NativeRecord struct {
	Content *string
	LogType int
	LogTime int64
}

// ParseNativeRecords streams UTF-8 records separated by CR, LF, or CRLF, as
// BufferedReader.readLine does. The supported input dialect is standard JSON;
// Fastjson extensions (comments, single quotes, unquoted keys) are rejected.
// Limits count line bytes excluding separators, and nonblank records. Positive
// limits are mandatory so callers cannot accidentally disable resource bounds.
func ParseNativeRecords(ctx context.Context, src io.Reader, maxLine, maxRecords int, emit func(NativeRecord) error) error {
	if maxLine <= 0 || maxRecords <= 0 {
		return ErrLimit
	}
	r := bufio.NewReader(src)
	line := make([]byte, 0, min(maxLine, 4096))
	count := 0
	afterCR := false
	process := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if nativeBlank(string(line)) {
			return nil
		}
		if count >= maxRecords {
			return ErrLimit
		}
		count++
		obj, err := nativeBean(ctx, line)
		if err != nil {
			return err
		}
		c, e := nativeString(ctx, obj["c"])
		if e != nil {
			return e
		}
		f, e := nativeString(ctx, obj["f"])
		if e != nil {
			return e
		}
		l, e := nativeString(ctx, obj["l"])
		if e != nil {
			return e
		}
		rec := NativeRecord{Content: c}
		if f != nil {
			n, err := strconv.ParseInt(*f, 10, 32)
			if err == nil && n >= 0 && n <= 3 {
				rec.LogType = int(n)
			}
		}
		if l != nil {
			n, err := strconv.ParseInt(*l, 10, 64)
			if err == nil {
				rec.LogTime = n
			}
		}
		return emit(rec)
	}
	// Read bounded fragments and search delimiters in bulk. This preserves
	// BufferedReader's CR/LF/CRLF semantics without one context call per byte.
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		part, readErr := r.ReadSlice('\n')
		for len(part) > 0 {
			if afterCR {
				afterCR = false
				if part[0] == '\n' {
					part = part[1:]
					continue
				}
			}
			at := bytes.IndexAny(part, "\r\n")
			size := len(part)
			if at >= 0 {
				size = at
			}
			if size > maxLine-len(line) {
				return ErrLimit
			}
			line = append(line, part[:size]...)
			if at < 0 {
				break
			}
			if err := process(); err != nil {
				return err
			}
			line = line[:0]
			afterCR = part[at] == '\r'
			part = part[at+1:]
		}
		if readErr == io.EOF {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(line) > 0 {
				return process()
			}
			return nil
		}
		if readErr != nil && readErr != bufio.ErrBufferFull {
			return readErr
		}
	}
}
func nativeBlank(s string) bool {
	for _, r := range s { // Character.isWhitespace excludes nonbreaking spaces.
		if !(r >= '\t' && r <= '\r' || r >= 0x1c && r <= 0x1f || unicode.Is(unicode.Zs, r) && r != 0xa0 && r != 0x2007 && r != 0x202f || r == 0x2028 || r == 0x2029) {
			return false
		}
	}
	return true
}
func nativeString(ctx context.Context, raw json.RawMessage) (*string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if raw[0] == '"' {
		if e := json.Unmarshal(raw, &s); e != nil {
			return nil, e
		}
	} else {
		if raw[0] == '{' || raw[0] == '[' {
			var err error
			s, err = nativeComposite(ctx, raw, 0)
			if err != nil {
				return nil, err
			}
		} else {
			s = string(raw)
			if strings.ContainsAny(s, "eE") && (raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') {
				var err error
				s, err = nativeDecimal(s)
				if err != nil {
					return nil, err
				}
			}
		}

	}
	return &s, nil
}

// nativeDecimal reproduces BigDecimal.toString for JSON exponent numbers.
func nativeDecimal(s string) (string, error) {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == 'e' || r == 'E' })
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid decimal")
	}
	exp, err := strconv.Atoi(parts[1])
	if err != nil || exp > 100000 || exp < -100000 {
		return "", ErrLimit
	}
	mant := parts[0]
	negative := strings.HasPrefix(mant, "-")
	mant = strings.TrimPrefix(mant, "-")
	fraction := 0
	if dot := strings.IndexByte(mant, '.'); dot >= 0 {
		fraction = len(mant) - dot - 1
		mant = mant[:dot] + mant[dot+1:]
	}
	digits := strings.TrimLeft(mant, "0")
	if digits == "" {
		digits = "0"
		negative = false
	}
	scale := fraction - exp
	adjusted := len(digits) - scale - 1
	prefix := ""
	if negative {
		prefix = "-"
	}
	if scale >= 0 && adjusted >= -6 {
		if scale == 0 {
			return prefix + digits, nil
		}
		if scale >= len(digits) {
			return prefix + "0." + strings.Repeat("0", scale-len(digits)) + digits, nil
		}
		return prefix + digits[:len(digits)-scale] + "." + digits[len(digits)-scale:], nil
	}
	result := prefix + digits[:1]
	if len(digits) > 1 {
		result += "." + digits[1:]
	}
	result += "E"
	if adjusted >= 0 {
		result += "+"
	}
	return result + strconv.Itoa(adjusted), nil
}

// Read properties in wire order: Fastjson's exact setters can replace earlier
// exact setters; smart matches are ignored once that field has been set.
func nativeBean(ctx context.Context, raw []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return nil, fmt.Errorf("invalid native record")
	}
	out := map[string]json.RawMessage{}
	exact := map[string]bool{}
	for d.More() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t, e = d.Token()
		if e != nil {
			return nil, e
		}
		key := t.(string)
		var value json.RawMessage
		if e = d.Decode(&value); e != nil {
			return nil, e
		}
		name := strings.ToLower(key)
		if name != "c" && name != "f" && name != "l" {
			continue
		}
		_, seen := out[name]
		if !seen || key == name && exact[name] {
			out[name] = value
			exact[name] = key == name
		}
	}
	if _, e = d.Token(); e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, fmt.Errorf("invalid native record suffix")
	}
	return out, nil
}

type nativeProperty struct {
	key  string
	raw  json.RawMessage
	hash uint32
}

func nativeHash(s string) uint32 {
	var h uint32
	for _, c := range utf16.Encode([]rune(s)) {
		h = 31*h + uint32(c)
	}
	return h ^ (h >> 16)
}
func nativeQuote(s string) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}
func nativeComposite(ctx context.Context, raw json.RawMessage, depth int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if depth > 128 {
		return "", ErrLimit
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", fmt.Errorf("missing value")
	}
	switch raw[0] {
	case '{':
		d := json.NewDecoder(bytes.NewReader(raw))
		_, _ = d.Token()
		props := []nativeProperty{}
		indices := map[string]int{}
		for d.More() {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			token, e := d.Token()
			if e != nil {
				return "", e
			}
			key := token.(string)
			var value json.RawMessage
			if e = d.Decode(&value); e != nil {
				return "", e
			}
			if i, ok := indices[key]; ok {
				props[i].raw = value
			} else {
				indices[key] = len(props)
				props = append(props, nativeProperty{key, value, nativeHash(key)})
			}
		}
		capacity := 16
		buckets := make([]int, capacity)
		for i, p := range props {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			slot := int(p.hash & uint32(capacity-1))
			grow := buckets[slot] >= 8 && capacity < 64
			buckets[slot]++
			if grow || i+1 > capacity*3/4 {
				capacity *= 2
				buckets = make([]int, capacity)
				for _, prior := range props[:i+1] {
					if err := ctx.Err(); err != nil {
						return "", err
					}
					buckets[prior.hash&uint32(capacity-1)]++
				}
			}
		}
		// Counting-sort by the final HashMap bucket. Stable placement keeps
		// collision chains in insertion order, without a quadratic scan.
		offsets := make([]int, capacity)
		total := 0
		for i, n := range buckets {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			offsets[i] = total
			total += n
		}
		ordered := make([]nativeProperty, len(props))
		for _, p := range props {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			slot := p.hash & uint32(capacity-1)
			ordered[offsets[slot]] = p
			offsets[slot]++
		}
		props = ordered
		parts := make([]string, 0, len(props))
		for _, p := range props {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if string(p.raw) == "null" {
				continue
			}
			value, e := nativeComposite(ctx, p.raw, depth+1)
			if e != nil {
				return "", e
			}
			parts = append(parts, nativeQuote(p.key)+":"+value)
		}
		return "{" + strings.Join(parts, ",") + "}", nil
	case '[':
		var values []json.RawMessage
		if e := json.Unmarshal(raw, &values); e != nil {
			return "", e
		}
		parts := make([]string, len(values))
		for i, v := range values {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var e error
			parts[i], e = nativeComposite(ctx, v, depth+1)
			if e != nil {
				return "", e
			}
		}
		return "[" + strings.Join(parts, ",") + "]", nil
	case '"':
		var s string
		if e := json.Unmarshal(raw, &s); e != nil {
			return "", e
		}
		return nativeQuote(s), nil
	case 'n', 't', 'f':
		return string(raw), nil
	default:
		s := string(raw)
		if strings.ContainsAny(s, ".eE") {
			if !strings.ContainsAny(s, "eE") {
				s += "e0"
			}
			return nativeDecimal(s)
		}
		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return "", fmt.Errorf("invalid integer")
		}
		return n.String(), nil
	}
}
