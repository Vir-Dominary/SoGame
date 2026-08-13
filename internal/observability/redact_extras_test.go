package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactingHandlerMasksURLCredentials(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(&output, slog.LevelDebug))

	logger.ErrorContext(context.Background(), "request failed",
		"endpoint", "https://admin:secret@example.com/room/AAAA-BBBB-CCCC",
	)

	got := output.String()
	for _, secret := range []string{"admin:secret", "AAAA-BBBB-CCCC"} {
		if strings.Contains(got, secret) {
			t.Fatalf("log contains secret %q: %s", secret, got)
		}
	}
}

func TestRedactingHandlerMasksArbitraryValueObjects(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(&output, slog.LevelDebug))

	// KindAny 的值必须经过脱敏后再渲染,不能绕过 redactAttr。
	logger.LogAttrs(context.Background(), slog.LevelError, "anything",
		slog.Any("setup_key", 2.5e18),
		slog.Any("room_code", "7X4K-329B-YY95"),
		slog.Any("payload", []byte("7X4K-329B-YY95")),
	)

	got := output.String()
	for _, secret := range []string{"7X4K-329B-YY95", "2.5e+18"} {
		if strings.Contains(got, secret) {
			t.Fatalf("log contains secret %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redaction marker: %s", got)
	}
}

func TestRedactMasksURLsWithCredentialsAndRoomCodes(t *testing.T) {
	input := "control plane url=https://user:pwd@legengen.top:8443/panel?room=AAAA-BBBB-CCCC"
	got := Redact(input)
	for _, secret := range []string{"user:pwd", "AAAA-BBBB-CCCC"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted value contains %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "legengen.top") {
		t.Fatalf("redaction removed the URL host: %s", got)
	}
}