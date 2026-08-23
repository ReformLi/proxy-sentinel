package stats

import (
	"context"
	"time"

	"proxy-sentinel/internal/storage"
)

// RealtimeService 实时统计服务
type RealtimeService struct {
	db *storage.DB
}

// NewRealtimeService 创建实时统计服务
func NewRealtimeService(db *storage.DB) *RealtimeService {
	return &RealtimeService{db: db}
}

// Get 返回实时统计指标
func (s *RealtimeService) Get(ctx context.Context) (*storage.RealtimeStats, error) {
	return s.db.GetRealtimeStats(ctx)
}

// ParseWindow 解析时间窗口字符串，返回 (from, bucketSeconds)
// 支持：1m / 1h / 24h / 7d
func ParseWindow(window string) (time.Time, int) {
	now := time.Now()
	switch window {
	case "1m":
		return now.Add(-1 * time.Minute), 1
	case "1h":
		return now.Add(-1 * time.Hour), 60
	case "24h":
		return now.Add(-24 * time.Hour), 300
	case "7d":
		return now.Add(-7 * 24 * time.Hour), 3600
	default:
		return now.Add(-1 * time.Hour), 60
	}
}
