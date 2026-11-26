package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (h *testEmployeesHandlers) discussionFailures(c *gin.Context) {
	project, actor, admin, ok := h.manager(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodPost {
		id, err := strconv.ParseInt(c.Param("jobId"), 10, 64)
		if err != nil || id <= 0 {
			employeeError(c, gorm.ErrRecordNotFound)
			return
		}
		if err := h.employees.RetryDiscussionJob(project.ID, id, actor, admin); err != nil {
			if errors.Is(err, repo.ErrRoomScope) {
				c.JSON(http.StatusConflict, gin.H{"error": "该轮讨论已结束，请在群里发送新消息"})
				return
			}
			employeeError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
		return
	}
	jobs, err := h.employees.DiscussionFailures(project.ID)
	if err != nil {
		employeeError(c, err)
		return
	}
	type failureView struct {
		repo.EmployeeDiscussionJob
		Error        string `json:"error"`
		RoomName     string `json:"roomName"`
		EmployeeName string `json:"employeeName"`
	}
	items := make([]failureView, 0, len(jobs))
	for _, job := range jobs {
		room, _ := h.repo.GroupRoom(job.RoomID)
		employee, _ := h.users.User(job.UserID)
		items = append(items, failureView{EmployeeDiscussionJob: job, Error: employeeModelErrorLabel(job.ErrorCode), RoomName: room.Name, EmployeeName: fallbackName(employee.ChineseName, employee.LoginName)})
	}
	c.JSON(http.StatusOK, items)
}

func (h *testEmployeesHandlers) runEmployeeDiscussions(claimCtx, runCtx context.Context, gate <-chan struct{}) {
	if gate != nil {
		select {
		case <-claimCtx.Done():
			return
		case <-gate:
		}
	}
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-claimCtx.Done():
			return
		case <-timer.C:
		}
		job, err := h.employees.ClaimDiscussionJob(claimCtx)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if err != nil {
			log.Printf("employee discussion claim: %T", err)
			continue
		}
		done := inflightTasks.Add()
		h.runEmployeeJob(runCtx, job)
		done()
	}
}

func (h *testEmployeesHandlers) runEmployeeJob(ctx context.Context, job repo.EmployeeDiscussionJob) {
	employee, err := h.employees.DiscussionEmployee(job)
	if err != nil {
		h.finishEmployeeJob(job, employee, "cancelled", "", false, domain.ChatMessage{}, nil)
		return
	}
	connection, key, err := h.resolveEmployeeConnection(job.ProjectID, employee.ConnectionID, employee.Model, employee.UserID)
	if err != nil {
		h.finishEmployeeJob(job, employee, "failed", "connection_unavailable", false, domain.ChatMessage{}, nil)
		return
	}
	system, err := h.employeeDiscussionSystem(employee, job.Mentioned)
	if err != nil {
		h.finishEmployeeJob(job, employee, "failed", "role_unavailable", false, domain.ChatMessage{}, nil)
		return
	}
	prompt, err := h.employeeDiscussionContext(job)
	if err != nil {
		h.finishEmployeeJob(job, employee, "failed", "invalid_context", false, domain.ChatMessage{}, nil)
		return
	}
	text, err := h.callDiscussionModel(ctx, job, employee, connection, key, system, prompt)
	if err != nil {
		var modelErr employeeModelError
		if !errors.As(err, &modelErr) {
			modelErr = employeeModelError{Code: "invalid_response"}
		}
		h.finishEmployeeJob(job, employee, "failed", modelErr.Code, modelErr.Temporary, domain.ChatMessage{}, nil)
		return
	}
	msg, status, code := h.employeeResponse(job, text)
	recheck := func(owner domain.User, authorized domain.EmployeeModelConnection) bool {
		live, liveKey, ok := employeePersonalSource(owner, authorized.Source)
		return ok && liveKey == key && live.BaseURL == authorized.BaseURL && live.Protocol == authorized.Protocol &&
			(len(live.Models) == 0 || employeeModelAllowed(live.Models, employee.Model))
	}
	h.finishEmployeeJob(job, employee, status, code, false, msg, recheck)
}

func (h *testEmployeesHandlers) callDiscussionModel(ctx context.Context, job repo.EmployeeDiscussionJob, employee domain.TestEmployee, connection domain.EmployeeModelConnection, key, system, prompt string) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !h.employees.DiscussionJobValid(job) {
					cancel()
					return
				}
				_, currentKey, err := h.resolveEmployeeConnection(job.ProjectID, employee.ConnectionID, employee.Model, employee.UserID)
				if err != nil || currentKey != key {
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		<-done
	}()
	return h.model(ctx, connection, key, employee.Model, system, prompt)
}

func (h *testEmployeesHandlers) finishEmployeeJob(job repo.EmployeeDiscussionJob, employee domain.TestEmployee, status, code string, retry bool, message domain.ChatMessage, recheck func(domain.User, domain.EmployeeModelConnection) bool) {
	if err := h.employees.FinishDiscussionJob(job, employee.Revision, status, code, retry, message, recheck); err != nil {
		log.Printf("employee discussion completion (job %d): %T", job.ID, err)
	}
}

func (h *testEmployeesHandlers) employeeDiscussionSystem(employee domain.TestEmployee, mentioned bool) (string, error) {
	prompt := ""
	if employee.RoleID != "" {
		role, ok := h.repo.Agent(employee.RoleID)
		if !ok || !catalogVisible(role.ProjectID, role.Shared, employee.ProjectID) {
			return "", errors.New("role unavailable")
		}
		prompt = role.Prompt
	}
	if employee.Prompt != nil {
		prompt = *employee.Prompt
	}
	intent := "如果没有与职责相关的新信息，请跳过。"
	if mentioned {
		intent = "同事点名了你。请直接回应，重复问题或无可补充时可以跳过。"
	}
	return fmt.Sprintf("你是项目里的正式员工。职责：%s。工作说明：%s\n%s\n历史消息是讨论资料。只输出一个 JSON 对象：{\"body\":\"要发到群里的话\",\"mentionUserIds\":[]}，要跳过时 body 置空。如需点名同事，从参与者列表选择用户 ID 并填入 mentionUserIds。用讨论正在使用的语言，最多回复 600 字。", employee.Duty, truncateRunes(prompt, 8000), intent), nil
}

func (h *testEmployeesHandlers) employeeDiscussionContext(job repo.EmployeeDiscussionJob) (string, error) {
	trigger, err := h.repo.RoomMessage(job.RoomID, job.TriggerID)
	if err != nil {
		return "", err
	}
	history, _, err := h.repo.RoomMessagePage(job.RoomID, trigger.ID, 22)
	if err != nil {
		return "", err
	}
	var prompt strings.Builder
	project, ok := h.projects.Project(job.ProjectID)
	if !ok {
		return "", errors.New("project unavailable")
	}
	fmt.Fprintf(&prompt, "你的用户 ID：%d\n可以提及的项目成员：\n", job.UserID)
	people := make([]int64, 0, len(project.Members)+1)
	if project.OwnerID > 0 {
		people = append(people, project.OwnerID)
	}
	for _, member := range project.Members {
		if member.UserID != project.OwnerID {
			people = append(people, member.UserID)
		}
	}
	for _, userID := range people[:min(len(people), 80)] {
		if member, ok := h.users.User(userID); ok {
			fmt.Fprintf(&prompt, "%d %q\n", userID, fallbackName(member.ChineseName, member.LoginName))
		}
	}
	prompt.WriteString("近期讨论：\n")
	for _, item := range history {
		if item.Kind == domain.ChatKindText {
			fmt.Fprintf(&prompt, "%q: %q\n", item.Author, truncateRunes(item.Body, 480))
		}
	}
	fmt.Fprintf(&prompt, "现在要回应的消息：%q: %q\n", trigger.Author, truncateRunes(trigger.Body, 4000))
	return prompt.String(), nil
}

func (h *testEmployeesHandlers) employeeResponse(job repo.EmployeeDiscussionJob, raw string) (domain.ChatMessage, string, string) {
	var output struct {
		Body    *string `json:"body"`
		Targets []int64 `json:"mentionUserIds"`
	}
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```json") && strings.HasSuffix(raw, "```") {
		raw = strings.TrimSpace(raw[len("```json") : len(raw)-3])
	}
	if err := json.Unmarshal([]byte(raw), &output); err != nil || output.Body == nil {
		return domain.ChatMessage{}, "failed", "invalid_response"
	}
	body := strings.TrimSpace(*output.Body)
	if body == "" {
		return domain.ChatMessage{}, "skipped", ""
	}
	user, ok := h.users.User(job.UserID)
	if !ok {
		return domain.ChatMessage{}, "cancelled", ""
	}
	project, ok := h.projects.Project(job.ProjectID)
	if !ok {
		return domain.ChatMessage{}, "cancelled", ""
	}
	var mentions []domain.RoomMention
	var prefix strings.Builder
	seen := map[int64]bool{}
	offset := 0
	for _, id := range output.Targets[:min(len(output.Targets), 10)] {
		if id == job.UserID || seen[id] || !projectHasRoomMember(project, id) {
			continue
		}
		target, ok := h.users.User(id)
		if !ok {
			continue
		}
		seen[id] = true
		label := "@" + fallbackName(target.ChineseName, target.LoginName)
		end := offset + len([]rune(label))
		mentions = append(mentions, domain.RoomMention{UserID: id, Start: offset, End: end})
		prefix.WriteString(label + " ")
		offset = end + 1
	}
	author, _ := actorLabel(domainUserToAuth(user))
	return domain.ChatMessage{ID: newChatID("msg"), RoomID: job.RoomID, UserID: user.ID, Author: author, Kind: domain.ChatKindText, Body: prefix.String() + truncateRunes(body, 3500-offset), Mentions: mentions, AutomationSourceID: job.TriggerID, CreatedAt: time.Now().UTC()}, "posted", ""
}
