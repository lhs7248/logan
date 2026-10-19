// Package nativecontent mirrors the original Java native content handlers.
package nativecontent

// Type is the public log type tuple returned by LogTypeEnum.allLogTypes.
type Type struct {
	First  int    `json:"first"`
	Second string `json:"second"`
}

func LogTypes() []Type {
	return []Type{{1, "日志类型1"}, {2, "日志类型2"}, {3, "日志类型3"}}
}

// Simple preserves content for all known types and the unknown fallback handler.
func Simple(logType int, content string) (string, error) { return content, nil }

// Format preserves content, including whitespace, escapes, and JSON text.
func Format(logType int, content string) (string, error) { return content, nil }
