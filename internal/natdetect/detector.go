package natdetect

import (
	"context"
	"fmt"
	"net"
	"time"
)

func Detect(ctx context.Context, stunA, stunB string) NATResult {
	result := NATResult{
		STUNServerA: stunA,
		STUNServerB: stunB,
		Type:        NATTypeUnknown,
	}

	laddr, err := getLocalAddr()
	if err != nil {
		result.Error = fmt.Sprintf("获取本地地址失败: %v", err)
		return result
	}
	result.LocalIP = laddr.IP.String()
	result.LocalPort = laddr.Port

	ma1, err := sendSTUN(stunA)
	if err != nil {
		result.Type = NATTypeUDPBlocked
		result.Error = fmt.Sprintf("STUN 请求失败: %v", err)
		result.Suggestion = result.SuggestionText()
		return result
	}
	result.PublicIP = ma1.IP.String()
	result.PublicPort = ma1.Port

	if ma1.IP.Equal(laddr.IP) && ma1.Port == laddr.Port {
		result.Type = NATTypeOpen
		result.Suggestion = result.SuggestionText()
		return result
	}

	ma2, err := sendSTUN(stunB)
	if err != nil {
		result.Type = NATTypeRestrictedCone
		result.Suggestion = result.SuggestionText()
		return result
	}

	if !ma1.IP.Equal(ma2.IP) || ma1.Port != ma2.Port {
		result.Type = NATTypeSymmetric
		result.Suggestion = result.SuggestionText()
		return result
	}

	result.Type = NATTypeFullCone
	result.Suggestion = result.SuggestionText()
	return result
}

func getLocalAddr() (*net.UDPAddr, error) {
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("8.8.8.8"), Port: 53})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr), nil
}

func DetectWithTimeout(stunA, stunB string, timeout time.Duration) NATResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return Detect(ctx, stunA, stunB)
}
