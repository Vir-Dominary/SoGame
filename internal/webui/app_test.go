package app

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

// TestEncodeInviteV2ShorterThanV1 验证 v2 格式比 v1 更短
func TestEncodeInviteV2ShorterThanV1(t *testing.T) {
	data := inviteData{
		Community: "community-2d76abfa",
		Key:       "f8d990cbb2b02b995800115cf0b3007a",
		Supernode: "8.148.244.159:10090",
	}

	v2, err := encodeInvite(data)
	if err != nil {
		t.Fatalf("encodeInvite failed: %v", err)
	}

	// 生成 v1 格式用于对比
	jsonBytes, _ := json.Marshal(data)
	v1 := base64.StdEncoding.EncodeToString(jsonBytes)

	t.Logf("v1 length: %d, code: %s", len(v1), v1)
	t.Logf("v2 length: %d, code: %s", len(v2), v2)

	if len(v2) >= len(v1) {
		t.Errorf("v2 (%d) should be shorter than v1 (%d)", len(v2), len(v1))
	}
}

// TestDecodeInviteV1 验证能解码 v1 格式的邀请码（向后兼容）
func TestDecodeInviteV1(t *testing.T) {
	// 用户提供的深圳节点 v1 邀请码
	v1Code := "eyJjIjoiY29tbXVuaXR5LTJkNzZhYmZhIiwiayI6ImY4ZDk5MGNiYjJiMDJiOTk1ODAwMTE1Y2YwYjMwMDdhIiwicyI6IjguMTQ4LjI0NC4xNTk6MTAwOTAifQ=="

	data, err := decodeInvite(v1Code)
	if err != nil {
		t.Fatalf("decodeInvite v1 failed: %v", err)
	}

	if data.Community != "community-2d76abfa" {
		t.Errorf("community = %s, want community-2d76abfa", data.Community)
	}
	if data.Key != "f8d990cbb2b02b995800115cf0b3007a" {
		t.Errorf("key = %s, want f8d990cbb2b02b995800115cf0b3007a", data.Key)
	}
	if data.Supernode != "8.148.244.159:10090" {
		t.Errorf("supernode = %s, want 8.148.244.159:10090", data.Supernode)
	}
}

// TestDecodeInviteV1Beijing 验证北京节点的 v1 邀请码
func TestDecodeInviteV1Beijing(t *testing.T) {
	v1Code := "eyJjIjoiY29tbXVuaXR5LTJkNzZhYmZhIiwiayI6ImY4ZDk5MGNiYjJiMDJiOTk1ODAwMTE1Y2YwYjMwMDdhIiwicyI6IjExNy43Mi44Ni4yMjQ6MTAwOTAifQ=="

	data, err := decodeInvite(v1Code)
	if err != nil {
		t.Fatalf("decodeInvite v1 beijing failed: %v", err)
	}

	if data.Supernode != "117.72.86.224:10090" {
		t.Errorf("supernode = %s, want 117.72.86.224:10090", data.Supernode)
	}
}

// TestEncodeDecodeRoundTrip 验证 v2 编码后解码能还原原始数据
func TestEncodeDecodeRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		community string
		key       string
		supernode string
	}{
		{
			name:      "shenzhen",
			community: "community-2d76abfa",
			key:       "f8d990cbb2b02b995800115cf0b3007a",
			supernode: "8.148.244.159:10090",
		},
		{
			name:      "beijing",
			community: "community-abcdef12",
			key:       "0123456789abcdef0123456789abcdef",
			supernode: "117.72.86.224:10090",
		},
		{
			name:      "ipv6_supernode",
			community: "community-deadbeef",
			key:       "fedcba9876543210fedcba9876543210",
			supernode: "[2603:c024:5:5f5f:203d:234:6c3d:593c]:10090",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := inviteData{
				Community: tt.community,
				Key:       tt.key,
				Supernode: tt.supernode,
			}

			encoded, err := encodeInvite(original)
			if err != nil {
				t.Fatalf("encodeInvite failed: %v", err)
			}

			t.Logf("%s: encoded = %s (len=%d)", tt.name, encoded, len(encoded))

			decoded, err := decodeInvite(encoded)
			if err != nil {
				t.Fatalf("decodeInvite failed: %v", err)
			}

			if decoded.Community != original.Community {
				t.Errorf("community = %s, want %s", decoded.Community, original.Community)
			}
			if decoded.Key != original.Key {
				t.Errorf("key = %s, want %s", decoded.Key, original.Key)
			}
			if decoded.Supernode != original.Supernode {
				t.Errorf("supernode = %s, want %s", decoded.Supernode, original.Supernode)
			}
		})
	}
}

// TestDecodeInvalidCode 验证无效邀请码返回错误
func TestDecodeInvalidCode(t *testing.T) {
	invalidCodes := []string{
		"",
		"not-a-valid-base64!!!",
		"bm90anNvbg==", // "notjson" in base64, not JSON, no separator
	}

	for _, code := range invalidCodes {
		_, err := decodeInvite(code)
		if err == nil {
			t.Errorf("expected error for code %q, got nil", code)
		}
	}
}
