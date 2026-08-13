package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

var (
	credentialAssignment = regexp.MustCompile(`(?i)(authorization|setup[-_ ]?key|room[-_ ]?code|admin[-_ ]?token|private[-_ ]?key|bearer)(\s*[:=]\s*|\s+)[^\s,;]+`)
	urlCredentialPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`)
	roomCodePattern      = regexp.MustCompile(`(?i)\b[A-Z0-9]{4}(?:-[A-Z0-9]{4}){2}\b`)
	setupKeyPattern      = regexp.MustCompile(`(?i)\b[0-9A-F]{8}(?:-[0-9A-F]{4}){3}-[0-9A-F]{12}\b`)
	privateKeyBlock      = regexp.MustCompile(`(?is)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`)
	ipv4Pattern          = regexp.MustCompile(`\b(?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}\b`)
	ipv6Pattern          = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{0,4}:){2,}[0-9a-f:]{2,}\b`)
	hostURLPattern       = regexp.MustCompile(`(?i)(https?://)[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?`)
	hostnamePattern      = regexp.MustCompile(`(?i)\b[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z]{2,})+\b`)
	peerIDPattern        = regexp.MustCompile(`(?i)\b[0-9a-f]{64}\b`)
)

var sensitiveKeys = map[string]struct{}{
	"authorization": {},
	"setupkey":      {},
	"setup_key":     {},
	"roomcode":      {},
	"room_code":     {},
	"token":         {},
	"privatekey":    {},
	"private_key":   {},
	"password":      {},
}

func Redact(value string) string {
	value = privateKeyBlock.ReplaceAllString(value, "[PRIVATE_KEY]")
	value = credentialAssignment.ReplaceAllString(value, "$1=[REDACTED]")
	value = urlCredentialPattern.ReplaceAllString(value, "${1}[REDACTED]@")
	value = setupKeyPattern.ReplaceAllString(value, "[REDACTED]")
	return roomCodePattern.ReplaceAllString(value, "[REDACTED]")
}

// Anonymize applies the credential redaction rules and removes network and
// peer identifiers before diagnostic data leaves the in-memory report.
func Anonymize(value string) string {
	value = Redact(value)
	value = ipv4Pattern.ReplaceAllString(value, "[IP]")
	value = ipv6Pattern.ReplaceAllString(value, "[IP]")
	value = peerIDPattern.ReplaceAllString(value, "[PEER_ID]")
	value = hostURLPattern.ReplaceAllString(value, "${1}[HOST]")
	return hostnamePattern.ReplaceAllString(value, "[HOST]")
}

func isSensitiveKey(key string) bool {
	_, ok := sensitiveKeys[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

type RedactingHandler struct {
	next slog.Handler
}

func NewRedactingHandler(w io.Writer, level slog.Leveler) *RedactingHandler {
	return &RedactingHandler{next: slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})}
}

func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *RedactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, Redact(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		clean.AddAttrs(redactAttr(attr))
		return true
	})
	return h.next.Handle(ctx, clean)
}

func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		clean[i] = redactAttr(attr)
	}
	return &RedactingHandler{next: h.next.WithAttrs(clean)}
}

func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{next: h.next.WithGroup(name)}
}

func redactAttr(attr slog.Attr) slog.Attr {
	if isSensitiveKey(attr.Key) {
		return slog.String(attr.Key, "[REDACTED]")
	}
	if attr.Value.Kind() == slog.KindString {
		return slog.String(attr.Key, Redact(attr.Value.String()))
	}
	if attr.Value.Kind() == slog.KindAny {
		// KindAny 可能携带含密钥/房间码的结构体或 map,
		// 统一字符串化后过一遍脱敏规则,堵住脱敏旁路。
		return slog.String(attr.Key, Redact(fmt.Sprintf("%v", attr.Value.Any())))
	}
	if attr.Value.Kind() == slog.KindGroup {
		group := attr.Value.Group()
		for i := range group {
			group[i] = redactAttr(group[i])
		}
		return slog.Group(attr.Key, attrsToAny(group)...)
	}
	return attr
}

func attrsToAny(attrs []slog.Attr) []any {
	values := make([]any, len(attrs))
	for i := range attrs {
		values[i] = attrs[i]
	}
	return values
}
