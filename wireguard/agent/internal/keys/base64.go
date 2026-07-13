package keys

import (
	"encoding/base64"
)

// base64Decode 解码 base64 字符串到字节切片
func base64Decode(s string, dst []byte) (int, error) {
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return 0, err
	}
	copy(dst, decoded)
	return len(decoded), nil
}

// base64Encode 编码字节切片为 base64 字符串
func base64Encode(src []byte) string {
	return base64.StdEncoding.EncodeToString(src)
}
