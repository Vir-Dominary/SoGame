package platform

import (
	"encoding/json"
	"os"
	"path/filepath"

	"netjoin/internal/logger"
)

// adapterRecord 持久化的网卡适配器记录
type adapterRecord struct {
	GUID string `json:"guid"`
	Name string `json:"name"`
}

// adapterStorePath 返回适配器记录文件路径。测试中可设置 adapterStorePathOverride 覆盖。
var adapterStorePathOverride string

func adapterStorePath() (string, error) {
	if adapterStorePathOverride != "" {
		return adapterStorePathOverride, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "SoGame", "adapter.json"), nil
}

func loadAdapterRecord() *adapterRecord {
	path, err := adapterStorePath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rec adapterRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil
	}
	if rec.GUID == "" {
		return nil
	}
	return &rec
}

func saveAdapterRecord(guid, name string) {
	if guid == "" {
		return
	}
	path, err := adapterStorePath()
	if err != nil {
		logger.Warnf("无法确定适配器记录文件路径: %v", err)
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	rec := adapterRecord{GUID: guid, Name: name}
	data, _ := json.Marshal(rec)
	if err := os.WriteFile(path, data, 0600); err != nil {
		logger.Warnf("保存适配器记录失败: %v", err)
	}
}

func findAdapterByGUID(guid string) *AdapterInfo {
	for _, a := range getAllAdapters() {
		if a.GUID == guid {
			return &a
		}
	}
	return nil
}

func getAdapterGUID(name string) string {
	for _, a := range getAllAdapters() {
		if a.Name == name {
			return a.GUID
		}
	}
	return ""
}
