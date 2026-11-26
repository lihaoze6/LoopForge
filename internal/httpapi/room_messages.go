package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (h *roomsHandlers) groupAccess(c *gin.Context, write bool) (domain.ChatRoom, bool) {
	room, err := h.repo.GroupRoom(c.Param("id"))
	if err != nil {
		roomMessageError(c, err)
		return room, false
	}
	project, found := h.repo.Project(room.ProjectID)
	if !found || project.ArchivedAt != nil {
		roomMessageError(c, repo.ErrRoomScope)
		return room, false
	}
	return room, h.roomAccess(c, room.ProjectID, write)
}

func (h *roomsHandlers) messages(c *gin.Context) {
	room, ok := h.groupAccess(c, c.Request.Method != http.MethodGet)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodGet {
		c.JSON(http.StatusOK, h.syncRoomRuns(room, h.repo.TimelineMessages(room.ID, "", 200)))
		return
	}
	var req struct {
		Body     string               `json:"body"`
		ClientID string               `json:"clientId"`
		Mentions []domain.RoomMention `json:"mentions"`
	}
	if !decodeLimit(c, &req, 32<<10) {
		return
	}
	runes := []rune(req.Body)
	if strings.TrimSpace(req.Body) == "" || len(runes) > 4000 || len(req.ClientID) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入 1–4000 个字符的消息"})
		return
	}
	if !validRoomMentions(runes, req.Mentions) {
		roomMessageError(c, repo.ErrRoomMention)
		return
	}
	if len(req.Mentions) == 0 {
		req.Body = strings.TrimSpace(req.Body)
		req.Mentions = nil
	}
	userID, author := h.roomActor(c)
	msg := domain.ChatMessage{ID: newChatID("msg"), RoomID: room.ID, UserID: userID, Author: author, Kind: domain.ChatKindText, Body: req.Body, Mentions: req.Mentions, CreatedAt: time.Now().UTC()}
	stored, created, err := h.employees.PostGroupMessage(room, msg, req.ClientID, h.isPlatformAdmin(c.Request), userID > 0)
	if err != nil {
		roomMessageError(c, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, stored)
}

func validRoomMentions(text []rune, mentions []domain.RoomMention) bool {
	if len(mentions) > 20 {
		return false
	}
	end := 0
	for _, mention := range mentions {
		if mention.UserID <= 0 || mention.Start < end || mention.Start >= len(text) ||
			mention.End-mention.Start < 2 || mention.End > len(text) || text[mention.Start] != '@' {
			return false
		}
		end = mention.End
	}
	return true
}

func (h *roomsHandlers) messageHistory(c *gin.Context) {
	room, ok := h.groupAccess(c, false)
	if !ok {
		return
	}
	before := c.Query("before")
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit 必须在 1 到 200 之间"})
			return
		}
		limit = n
	}
	items, hasMore, err := h.repo.RoomMessagePage(room.ID, before, limit)
	if err != nil {
		roomMessageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"messages": items, "hasMore": hasMore})
}

func (h *roomsHandlers) messageByID(c *gin.Context) {
	room, ok := h.groupAccess(c, false)
	if !ok {
		return
	}
	item, err := h.repo.RoomMessage(room.ID, c.Param("messageId"))
	if err != nil {
		roomMessageError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (h *roomsHandlers) roomMentions(c *gin.Context) {
	userID, _ := h.roomActor(c)
	projectID := c.Query("projectId")
	project, found := h.repo.Project(projectID)
	if !found || project.ArchivedAt != nil {
		roomMessageError(c, repo.ErrRoomScope)
		return
	}
	if userID <= 0 || !projectHasRoomMember(project, userID) {
		c.JSON(http.StatusOK, gin.H{"mentions": []domain.RoomMentionNotice{}, "unread": 0})
		return
	}
	items, unread, err := h.repo.RoomMentions(userID, projectID)
	if err != nil {
		roomMessageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"mentions": items, "unread": unread})
}

func (h *roomsHandlers) readMention(c *gin.Context) {
	room, ok := h.groupAccess(c, false)
	if !ok {
		return
	}
	userID, _ := h.roomActor(c)
	if userID > 0 {
		if err := h.repo.ReadRoomMention(userID, room.ID, c.Param("messageId")); err != nil {
			roomMessageError(c, err)
			return
		}
	}
	c.Status(http.StatusNoContent)
}

func (h *roomsHandlers) discussionSettings(c *gin.Context) {
	room, ok := h.groupAccess(c, false)
	if !ok {
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
		settings, err := h.employees.DiscussionSettings(room.ID)
		if err != nil {
			roomMessageError(c, err)
			return
		}
		c.JSON(http.StatusOK, settings)
	case http.MethodPut:
		userID, _ := h.roomActor(c)
		if userID <= 0 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
			return
		}
		var settings domain.RoomDiscussionSettings
		if !decodeLimit(c, &settings, 4096) {
			return
		}
		if settings.ReplyLimit < 1 || settings.ReplyLimit > 12 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "连续回复上限应在 1–12 条之间"})
			return
		}
		if err := h.employees.SetDiscussionSettings(room, userID, h.isPlatformAdmin(c.Request), settings); err != nil {
			roomMessageError(c, err)
			return
		}
		c.JSON(http.StatusOK, settings)
	}
}

func roomMessageError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repo.ErrRoomScope), errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "找不到项目协作群或群消息"})
	case errors.Is(err, repo.ErrRoomMention):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, repo.ErrEmployeeForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "该账号没有执行此操作的项目权限"})
	case errors.Is(err, repo.ErrRoomMessageConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		respond(c, err)
	}
}
