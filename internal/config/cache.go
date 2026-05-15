package config

import (
	"sync"
	"time"
)

// ConfigCache 配置缓存机制
type ConfigCache struct {
	config    *Config
	mu        sync.RWMutex
	expireAt  time.Time // 缓存过期时间
	cacheTTL  time.Duration
	dirty     bool  // 是否已修改
	hitCount  int64 // 缓存命中次数
	missCount int64 // 缓存未命中次数
	loadCount int64 // 从磁盘加载次数
	saveCount int64 // 保存次数
}

var globalCache *ConfigCache

// InitCache 初始化配置缓存
func InitCache(ttl time.Duration) {
	if globalCache == nil {
		globalCache = &ConfigCache{
			cacheTTL: ttl,
			expireAt: time.Now().Add(ttl),
		}
	}
}

// GetCached 获取缓存的配置
// 如果缓存过期或被修改，从磁盘重新加载
func GetCached() (*Config, error) {
	if globalCache == nil {
		InitCache(5 * time.Minute)
	}

	globalCache.mu.RLock()
	if globalCache.config != nil && time.Now().Before(globalCache.expireAt) && !globalCache.dirty {
		globalCache.hitCount++
		cfg := globalCache.config
		globalCache.mu.RUnlock()
		return cfg, nil
	}
	globalCache.missCount++
	globalCache.mu.RUnlock()

	// 缓存过期或不存在，从磁盘加载
	cfg, err := LoadOrCreate()
	if err != nil {
		return nil, err
	}

	globalCache.mu.Lock()
	globalCache.config = cfg
	globalCache.expireAt = time.Now().Add(globalCache.cacheTTL)
	globalCache.dirty = false
	globalCache.loadCount++
	globalCache.mu.Unlock()

	return cfg, nil
}

// InvalidateCache 使缓存失效（当配置被修改时调用）
func InvalidateCache() {
	if globalCache != nil {
		globalCache.mu.Lock()
		globalCache.dirty = true
		globalCache.mu.Unlock()
	}
}

// SaveCached 保存配置并更新缓存
func SaveCached(cfg *Config) error {
	if err := Save(cfg); err != nil {
		return err
	}

	if globalCache != nil {
		globalCache.mu.Lock()
		globalCache.config = cfg
		globalCache.expireAt = time.Now().Add(globalCache.cacheTTL)
		globalCache.dirty = false
		globalCache.saveCount++
		globalCache.mu.Unlock()
	}

	return nil
}

// GetCacheStats 获取缓存统计信息
func GetCacheStats() map[string]int64 {
	if globalCache == nil {
		return nil
	}

	globalCache.mu.RLock()
	defer globalCache.mu.RUnlock()

	return map[string]int64{
		"hitCount":  globalCache.hitCount,
		"missCount": globalCache.missCount,
		"loadCount": globalCache.loadCount,
		"saveCount": globalCache.saveCount,
	}
}

// ClearCache 清空缓存
func ClearCache() {
	if globalCache != nil {
		globalCache.mu.Lock()
		globalCache.config = nil
		globalCache.dirty = true
		globalCache.mu.Unlock()
	}
}
