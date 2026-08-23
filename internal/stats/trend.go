package stats

import (
	"context"
	"time"

	"proxy-sentinel/internal/storage"
)

// TrendData 趋势聚合结果
type TrendData struct {
	Points       []storage.TrendPoint `json:"points"`
	Percentiles  *storage.Percentiles `json:"percentiles"`
	StatusDist   []storage.StatusBucket `json:"status_distribution"`
	TopPaths     []storage.TopPath    `json:"top_paths"`
}

// TrendService 趋势统计服务
type TrendService struct {
	db *storage.DB
}

// NewTrendService 创建趋势统计服务
func NewTrendService(db *storage.DB) *TrendService {
	return &TrendService{db: db}
}

// Get 返回指定时间窗口的趋势聚合数据
func (s *TrendService) Get(ctx context.Context, window string) (*TrendData, error) {
	from, bucket := ParseWindow(window)
	now := time.Now()

	points, err := s.db.GetTrend(ctx, from, now, bucket)
	if err != nil {
		return nil, err
	}
	percentiles, err := s.db.GetPercentiles(ctx, from, now)
	if err != nil {
		return nil, err
	}
	statusDist, err := s.db.GetStatusDistribution(ctx, from, now)
	if err != nil {
		return nil, err
	}
	topPaths, err := s.db.GetTopPaths(ctx, from, now, 10)
	if err != nil {
		return nil, err
	}

	return &TrendData{
		Points:      points,
		Percentiles: percentiles,
		StatusDist:  statusDist,
		TopPaths:    topPaths,
	}, nil
}

// FlowService 数据流向服务
type FlowService struct {
	db *storage.DB
}

// NewFlowService 创建数据流向服务
func NewFlowService(db *storage.DB) *FlowService {
	return &FlowService{db: db}
}

// Get 返回数据流向拓扑数据
func (s *FlowService) Get(ctx context.Context, window string) ([]storage.FlowNode, error) {
	from, _ := ParseWindow(window)
	return s.db.GetFlowMap(ctx, from, time.Now())
}
