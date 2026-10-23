package workbench

import (
	"regexp"
	"strings"
)

var bearerSecret = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=\-]+`)
var namedSecret = regexp.MustCompile(`(?i)(["']?(?:password|passwd|authorization|access[_-]?token|refresh[_-]?token|api[_-]?key|secret|token|cookie)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}]+)`)
var emailSecret = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
var phoneSecret = regexp.MustCompile(`\b1[3-9][0-9]{9}\b`)

func maskText(s string) string {
	s = bearerSecret.ReplaceAllString(s, "Bearer [REDACTED]")
	s = namedSecret.ReplaceAllString(s, "${1}[REDACTED]")
	s = emailSecret.ReplaceAllString(s, "[REDACTED_EMAIL]")
	return phoneSecret.ReplaceAllString(s, "[REDACTED_PHONE]")
}
func sensitiveField(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	return contains([]string{"password", "passwd", "authorization", "token", "accesstoken", "refreshtoken", "apikey", "xapikey", "xloganuploadtoken", "secret", "cookie", "setcookie", "email", "phone", "mobile"}, normalized)
}
func maskValue(v any) any {
	switch value := v.(type) {
	case string:
		return maskText(value)
	case map[string]any:
		return maskValue(Record(value))
	case Record:
		r := clone(value)
		for k, x := range r {
			if sensitiveField(k) {
				r[k] = "[REDACTED]"
			} else {
				r[k] = maskValue(x)
			}
		}
		return r
	case []any:
		out := []any{}
		for _, x := range value {
			out = append(out, maskValue(x))
		}
		return out
	case []Record:
		out := []any{}
		for _, x := range value {
			out = append(out, maskValue(x))
		}
		return out
	}
	return v
}
