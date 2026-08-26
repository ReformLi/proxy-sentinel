package api

import (
	"net/http"
	"strconv"
	"strings"

	"proxy-sentinel/internal/auth"
	"proxy-sentinel/internal/storage"
)

// listUsers GET /api/users —— 列出全部用户（不含密码哈希）
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.db.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询用户列表失败: "+err.Error())
		return
	}
	if users == nil {
		users = []storage.UserInfo{}
	}
	currentUser := auth.UsernameFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"users":         users,
		"current_user":  currentUser,
	})
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// createUser POST /api/users —— 新建用户
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		writeError(w, http.StatusBadRequest, "用户名不能为空")
		return
	}
	if len(username) < 3 || len(username) > 32 {
		writeError(w, http.StatusBadRequest, "用户名长度需 3~32 字符")
		return
	}
	if len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, "密码长度至少 6 位")
		return
	}

	exists, err := s.db.UserExists(r.Context(), username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询用户失败: "+err.Error())
		return
	}
	if exists {
		writeError(w, http.StatusBadRequest, "用户名已存在")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "密码加密失败")
		return
	}
	if err := s.db.CreateUser(r.Context(), username, hash); err != nil {
		writeError(w, http.StatusInternalServerError, "创建用户失败: "+err.Error())
		return
	}

	auth.Audit(auditCtx(r), s.db, auth.UsernameFromContext(r.Context()),
		"创建用户: "+username, ipFromRequest(r))
	writeJSON(w, http.StatusCreated, map[string]any{"message": "用户已创建"})
}

// deleteUser DELETE /api/users/{id} —— 删除用户（禁止删除自己）
func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "无效的用户 ID")
		return
	}
	user, err := s.db.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询用户失败: "+err.Error())
		return
	}
	if user == nil {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	currentUser := auth.UsernameFromContext(r.Context())
	if user.Username == currentUser {
		writeError(w, http.StatusBadRequest, "不能删除当前登录的自己")
		return
	}
	// 防止删掉最后一个用户
	users, err := s.db.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询用户列表失败: "+err.Error())
		return
	}
	if len(users) <= 1 {
		writeError(w, http.StatusBadRequest, "不能删除最后一个用户")
		return
	}

	deleted, err := s.db.DeleteUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除用户失败: "+err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	auth.Audit(auditCtx(r), s.db, currentUser,
		"删除用户: "+user.Username, ipFromRequest(r))
	writeJSON(w, http.StatusOK, map[string]any{"message": "用户已删除"})
}

type resetPasswordRequest struct {
	Password string `json:"password"`
}

// resetPassword PUT /api/users/{id}/password —— 重置用户密码
func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "无效的用户 ID")
		return
	}
	var req resetPasswordRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, "密码长度至少 6 位")
		return
	}
	user, err := s.db.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询用户失败: "+err.Error())
		return
	}
	if user == nil {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "密码加密失败")
		return
	}
	if err := s.db.UpdatePasswordHash(r.Context(), user.Username, hash); err != nil {
		writeError(w, http.StatusInternalServerError, "更新密码失败: "+err.Error())
		return
	}
	auth.Audit(auditCtx(r), s.db, auth.UsernameFromContext(r.Context()),
		"重置用户密码: "+user.Username, ipFromRequest(r))
	writeJSON(w, http.StatusOK, map[string]any{"message": "密码已重置"})
}
