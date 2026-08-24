package api

import (
	"net/http"

	"proxy-sentinel/internal/alert"
	"proxy-sentinel/internal/auth"
)

// alertConfigInfo GET /api/alert/config 响应结构
type alertConfigInfo struct {
	Rules                    alert.Rules `json:"rules"`
	DingTalkConfigured       bool        `json:"dingtalk_configured"`        // webhook 是否已在 config.yaml 配置
	CheckIntervalSeconds     int         `json:"check_interval_seconds"`    // 评估周期
}

// getAlertConfig GET /api/alert/config —— 告警规则 + 渠道状态
func (s *Server) getAlertConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, alertConfigInfo{
		Rules:                s.alertEngine.GetRules(),
		DingTalkConfigured:   s.alertEngine.DingConfigured(),
		CheckIntervalSeconds: s.alertEngine.CheckIntervalSeconds(),
	})
}

// updateAlertConfig PUT /api/alert/config —— 更新规则（持久化 + 热生效）
func (s *Server) updateAlertConfig(w http.ResponseWriter, r *http.Request) {
	var rules alert.Rules
	if err := readJSON(r, &rules); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := s.alertEngine.SetRules(r.Context(), rules); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	auth.Audit(auditCtx(r), s.db, auth.UsernameFromContext(r.Context()), "alert_rules_updated", ipFromRequest(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "告警规则已保存（立即生效，重启后保留）",
		"rules":   s.alertEngine.GetRules(),
	})
}

// testAlert POST /api/alert/test —— 向钉钉发送测试消息验证连通性
func (s *Server) testAlert(w http.ResponseWriter, r *http.Request) {
	if !s.alertEngine.DingConfigured() {
		writeError(w, http.StatusBadRequest, "钉钉 webhook 未配置：请在 config.yaml 填写 alert.dingtalk.webhook_url 后重启")
		return
	}
	username := auth.UsernameFromContext(r.Context())
	if err := s.alertEngine.SendTest(username); err != nil {
		writeError(w, http.StatusBadGateway, "发送失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "测试消息已发送，请到钉钉群查收"})
}
