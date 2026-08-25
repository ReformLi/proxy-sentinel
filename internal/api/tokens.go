package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"proxy-sentinel/internal/auth"
	"proxy-sentinel/internal/storage"
)

// generateToken 生成随机代理 Token：sk- + 32 位十六进制（128 bit 熵）
func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sk-" + hex.EncodeToString(b), nil
}

// listTokens GET /api/tokens —— 列出全部 Token 元数据（不含 Token 值）
func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.db.ListTokens(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询 Token 列表失败: "+err.Error())
		return
	}
	if tokens == nil {
		tokens = []storage.TokenInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tokens":       tokens,
		"default_rpm":  s.cfg.RateLimit.DefaultRPM,
	})
}

type createTokenRequest struct {
	Name          string `json:"name"`
	Token         string `json:"token"`          // 可选：不传则自动生成
	RateLimitRPM  int    `json:"rate_limit_rpm"` // 0 = 跟随全局默认
	ExpiresInDays *int   `json:"expires_in_days"` // nil/0 = 永不过期；>0 则 N 天后失效
}

// createToken POST /api/tokens —— 新增 Token，明文值仅在本次响应中返回一次
func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "Token 名称不能为空")
		return
	}
	if len(name) > 64 {
		writeError(w, http.StatusBadRequest, "Token 名称过长（≤64 字符）")
		return
	}
	if req.RateLimitRPM < 0 {
		writeError(w, http.StatusBadRequest, "限流值不能为负数")
		return
	}
	if req.ExpiresInDays != nil && *req.ExpiresInDays < 1 {
		writeError(w, http.StatusBadRequest, "过期天数必须 ≥1 或留空（永不过期）")
		return
	}

	// Token 值：用户指定或自动生成
	token := strings.TrimSpace(req.Token)
	if token == "" {
		var err error
		token, err = generateToken()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "生成 Token 失败")
			return
		}
	} else if len(token) < 16 || len(token) > 128 {
		writeError(w, http.StatusBadRequest, "自定义 Token 长度需在 16~128 字符之间")
		return
	}

	// 过期时间：nil 或 0 = 永不过期（零值）；否则 N 天后失效
	var expiresAt time.Time
	if req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		expiresAt = time.Now().AddDate(0, 0, *req.ExpiresInDays)
	}

	exists, err := s.db.TokenExists(r.Context(), token)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询 Token 失败: "+err.Error())
		return
	}
	if exists {
		writeError(w, http.StatusBadRequest, "该 Token 已存在")
		return
	}

	if err := s.db.AddToken(r.Context(), token, name, req.RateLimitRPM, expiresAt); err != nil {
		writeError(w, http.StatusInternalServerError, "创建 Token 失败: "+err.Error())
		return
	}

	auth.Audit(auditCtx(r), s.db, auth.UsernameFromContext(r.Context()), "token_created", ipFromRequest(r))
	resp := map[string]any{
		"message":        "Token 已创建，明文值仅本次返回，请立即保存",
		"token":          token,
		"name":           name,
		"rate_limit_rpm": req.RateLimitRPM,
	}
	if !expiresAt.IsZero() {
		resp["expires_at"] = expiresAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusCreated, resp)
}

type updateTokenRequest struct {
	Name         string `json:"name"`
	RateLimitRPM *int   `json:"rate_limit_rpm"` // 指针区分"未传"与"传 0"
}

// updateToken PUT /api/tokens/{id} —— 重命名 / 调整独立限流值
func (s *Server) updateToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "无效的 Token ID")
		return
	}
	var req updateTokenRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	name := strings.TrimSpace(req.Name)
	if req.Name != "" && name == "" {
		writeError(w, http.StatusBadRequest, "Token 名称不能为空白")
		return
	}
	if len(name) > 64 {
		writeError(w, http.StatusBadRequest, "Token 名称过长（≤64 字符）")
		return
	}
	if req.RateLimitRPM != nil && *req.RateLimitRPM < 0 {
		writeError(w, http.StatusBadRequest, "限流值不能为负数")
		return
	}

	tok, err := s.db.GetToken(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询 Token 失败: "+err.Error())
		return
	}
	if tok == nil {
		writeError(w, http.StatusNotFound, "Token 不存在")
		return
	}

	if err := s.db.UpdateTokenMeta(r.Context(), id, name, req.RateLimitRPM); err != nil {
		writeError(w, http.StatusInternalServerError, "更新 Token 失败: "+err.Error())
		return
	}

	// 限流值变更后清理该 Token 的内存计数，立即按新额度执行
	if req.RateLimitRPM != nil {
		s.limiter.Remove(id)
	}

	auth.Audit(auditCtx(r), s.db, auth.UsernameFromContext(r.Context()), "token_updated", ipFromRequest(r))
	writeJSON(w, http.StatusOK, map[string]any{"message": "Token 已更新"})
}

// deleteToken DELETE /api/tokens/{id} —— 吊销（删除）Token
func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "无效的 Token ID")
		return
	}
	deleted, err := s.db.DeleteToken(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除 Token 失败: "+err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "Token 不存在")
		return
	}
	s.limiter.Remove(id)

	auth.Audit(auditCtx(r), s.db, auth.UsernameFromContext(r.Context()), "token_deleted", ipFromRequest(r))
	writeJSON(w, http.StatusOK, map[string]any{"message": "Token 已吊销（立即生效）"})
}

// rateLimitMiddleware 按 Token 限流：Token 独立值优先，否则用全局默认；超限返回 429
func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenID := auth.TokenIDFromContext(r.Context())
		limit := auth.TokenRPMFromContext(r.Context())
		if limit <= 0 {
			limit = s.cfg.RateLimit.DefaultRPM
		}
		if limit <= 0 {
			next.ServeHTTP(w, r) // 全局未启用限流
			return
		}

		ok, retry := s.limiter.Allow(tokenID, limit)
		if !ok {
			secs := int(retry/time.Second) + 1
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			http.Error(w,
				`{"error":"rate limit exceeded","limit_per_minute":`+strconv.Itoa(limit)+`}`,
				http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
