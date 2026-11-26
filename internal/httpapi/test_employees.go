package httpapi

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type testEmployeesHandlers struct {
	*deps
	employees *repo.TestEmployeesRepo
	users     *repo.UsersRepo
	projects  *repo.ProjectsRepo
	model     employeeModelCaller
}

type employeeConnectionInput struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`
	Model    string `json:"model"`
}

type testEmployeeInput struct {
	domain.TestEmployee
	LoginName   string                   `json:"loginName"`
	DisplayName string                   `json:"displayName"`
	Password    string                   `json:"password"`
	Connection  *employeeConnectionInput `json:"connection"`
}

func (h *testEmployeesHandlers) manager(c *gin.Context) (domain.Project, int64, bool, bool) {
	user, ok := h.requestUser(c.Request, true)
	if !ok || user.ID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return domain.Project{}, 0, false, false
	}
	project, ok := h.projects.Project(c.Param("id"))
	if !ok || project.ArchivedAt != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return project, 0, false, false
	}
	admin := h.isPlatformAdmin(c.Request)
	if !admin && project.OwnerID != user.ID {
		employeeError(c, repo.ErrEmployeeForbidden)
		return project, 0, false, false
	}
	return project, user.ID, admin, true
}

func (h *testEmployeesHandlers) projectEmployees(c *gin.Context) {
	project, actor, admin, ok := h.manager(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodGet {
		employees, err := h.employees.TestEmployees(project.ID)
		if err != nil {
			employeeError(c, err)
			return
		}
		active := make([]domain.TestEmployee, 0, len(employees))
		for _, employee := range employees {
			if projectHasRoomMember(project, employee.UserID) {
				active = append(active, employee)
			}
		}
		connections, err := h.employees.EmployeeConnections(project.ID)
		if err != nil {
			employeeError(c, err)
			return
		}
		roles := make([]domain.AgentDefinition, 0)
		for _, role := range h.repo.Agents() {
			if catalogVisible(role.ProjectID, role.Shared, project.ID) {
				roles = append(roles, domain.AgentDefinition{ID: role.ID, Name: role.Name, Prompt: role.Prompt, Model: role.Model})
			}
		}
		c.JSON(http.StatusOK, gin.H{"employees": active, "connections": connections, "roles": roles})
		return
	}
	var input testEmployeeInput
	if !decodeLimit(c, &input, 32<<10) {
		return
	}
	login, name := strings.TrimSpace(input.LoginName), strings.TrimSpace(input.DisplayName)
	if !loginPattern.MatchString(login) || name == "" || len([]rune(name)) > 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入有效的登录名和显示名（最多 64 个字）"})
		return
	}
	if !validAccountPassword(c, input.Password) {
		return
	}
	input.TestEmployee.ProjectID, input.TestEmployee.Paused = project.ID, false
	connection, key, ok := h.validateEmployee(c, &input)
	if !ok {
		return
	}
	employee, err := h.employees.CreateTestEmployee(actor, admin, login, name, input.Password, input.TestEmployee, connection, key)
	if err != nil {
		employeeError(c, err)
		return
	}
	saved, _ := h.projects.Project(project.ID)
	c.JSON(http.StatusCreated, gin.H{"employee": employee, "project": domain.PublicProject(saved), "user": gin.H{"id": employee.UserID, "loginName": login, "chineseName": name}})
}

func (h *testEmployeesHandlers) updateEmployee(c *gin.Context) {
	project, actor, admin, ok := h.manager(c)
	if !ok {
		return
	}
	userID, err := strconv.ParseInt(c.Param("userId"), 10, 64)
	if err != nil || userID <= 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "员工不存在"})
		return
	}
	var input testEmployeeInput
	if !decodeLimit(c, &input, 32<<10) {
		return
	}
	input.TestEmployee.ProjectID, input.TestEmployee.UserID = project.ID, userID
	connection, key, ok := h.validateEmployee(c, &input)
	if !ok {
		return
	}
	employee, err := h.employees.UpdateTestEmployee(actor, admin, input.TestEmployee, nil, connection, key)
	if err != nil {
		employeeError(c, err)
		return
	}
	c.JSON(http.StatusOK, employee)
}

func (h *testEmployeesHandlers) employeePause(c *gin.Context) {
	actor, ok := h.requestUser(c.Request, true)
	if !ok || actor.ID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	userID, err := strconv.ParseInt(c.Param("userId"), 10, 64)
	if err != nil || userID <= 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "员工不存在"})
		return
	}
	projectID := c.Param("id")
	if c.Request.Method == http.MethodGet {
		if actor.ID != userID {
			employeeError(c, repo.ErrEmployeeForbidden)
			return
		}
		employees, err := h.employees.TestEmployees(projectID)
		if err != nil {
			employeeError(c, err)
			return
		}
		project, found := h.projects.Project(projectID)
		if found && project.ArchivedAt == nil && projectHasRoomMember(project, userID) {
			for _, item := range employees {
				if item.UserID == userID {
					c.JSON(http.StatusOK, gin.H{"paused": item.Paused})
					return
				}
			}
		}
		employeeError(c, gorm.ErrRecordNotFound)
		return
	}
	var input struct {
		Paused *bool `json:"paused"`
	}
	if !decodeLimit(c, &input, 4096) {
		return
	}
	if input.Paused == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择暂停或恢复"})
		return
	}
	employee, err := h.employees.UpdateTestEmployee(actor.ID, h.isPlatformAdmin(c.Request), domain.TestEmployee{ProjectID: projectID, UserID: userID}, input.Paused, nil, "")
	if err != nil {
		employeeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"paused": employee.Paused, "revision": employee.Revision})
}

func (h *testEmployeesHandlers) validateEmployee(c *gin.Context, input *testEmployeeInput) (*domain.EmployeeModelConnection, string, bool) {
	employee := &input.TestEmployee
	employee.RoleID, employee.Duty, employee.Model = strings.TrimSpace(employee.RoleID), strings.TrimSpace(employee.Duty), strings.TrimSpace(employee.Model)
	if len([]rune(employee.Duty)) > 120 || (employee.Prompt != nil && len([]rune(*employee.Prompt)) > 8000) || len(employee.Model) > 250 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "职责、提示词或模型标识过长"})
		return nil, "", false
	}
	if employee.RoleID != "" {
		role, found := h.repo.Agent(employee.RoleID)
		if !found || !catalogVisible(role.ProjectID, role.Shared, employee.ProjectID) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "角色不可用于此项目"})
			return nil, "", false
		}
		if employee.Model == "" {
			employee.Model = role.Model
		}
	} else if employee.Duty == "" && (employee.Prompt == nil || strings.TrimSpace(*employee.Prompt) == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择角色，或填写职责与提示词"})
		return nil, "", false
	}
	if input.Connection == nil {
		return nil, "", true
	}
	connection, ok := employeeCustomConnection(employee.ProjectID, *input.Connection)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入有效的专用模型地址、协议和模型"})
		return nil, "", false
	}
	employee.Model = connection.Models[0]
	return &connection, input.Connection.APIKey, true
}

func employeeCustomConnection(projectID string, input employeeConnectionInput) (domain.EmployeeModelConnection, bool) {
	name, baseURL, model := strings.TrimSpace(input.Name), strings.TrimSpace(input.BaseURL), strings.TrimSpace(input.Model)
	if name == "" || len([]rune(name)) > 80 || baseURL == "" || len(baseURL) > 2048 || model == "" || len(model) > 250 || len(input.APIKey) > 4096 {
		return domain.EmployeeModelConnection{}, false
	}
	endpoint, ok := employeeModelEndpoint(input.Protocol, baseURL)
	if !ok {
		return domain.EmployeeModelConnection{}, false
	}
	return domain.EmployeeModelConnection{ID: newChatID("conn"), ProjectID: projectID, Name: name, Protocol: input.Protocol, BaseURL: endpoint, Models: []string{model}}, true
}

func (h *testEmployeesHandlers) connectionSources(c *gin.Context) {
	user, ok := h.requestUser(c.Request, true)
	if !ok || user.ID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	stored, ok := h.users.User(user.ID)
	if !ok {
		employeeError(c, gorm.ErrRecordNotFound)
		return
	}
	sources := []string{"codex", "claude-code"}
	for engine := range stored.AgentCLICredentials {
		sources = append(sources, engine)
	}
	items := make([]domain.EmployeeModelConnection, 0, len(sources))
	sort.Strings(sources)
	for _, source := range sources {
		item, _, ok := employeePersonalSource(stored, source)
		if ok {
			item.BaseURL = ""
			items = append(items, item)
		}
	}
	c.JSON(http.StatusOK, items)
}

func (h *testEmployeesHandlers) employeeConnectionProject(c *gin.Context) (domain.Project, int64, bool, bool) {
	user, ok := h.requestUser(c.Request, true)
	if !ok || user.ID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return domain.Project{}, 0, false, false
	}
	project, ok := h.projects.Project(c.Param("id"))
	if !ok || project.ArchivedAt != nil {
		employeeError(c, gorm.ErrRecordNotFound)
		return project, 0, false, false
	}
	admin := h.isPlatformAdmin(c.Request)
	if !admin && !projectHasRoomMember(project, user.ID) {
		employeeError(c, repo.ErrEmployeeForbidden)
		return project, 0, false, false
	}
	return project, user.ID, admin, true
}

func (h *testEmployeesHandlers) authorizeConnection(c *gin.Context) {
	project, actor, admin, ok := h.employeeConnectionProject(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodGet {
		items, err := h.employees.EmployeeConnections(project.ID)
		if err != nil {
			employeeError(c, err)
			return
		}
		owned := make([]domain.EmployeeModelConnection, 0)
		for _, item := range items {
			if item.Source != "" && item.OwnerID == actor {
				owned = append(owned, item)
			}
		}
		c.JSON(http.StatusOK, owned)
		return
	}
	var input struct {
		Source string   `json:"source"`
		Name   string   `json:"name"`
		Models []string `json:"models"`
	}
	if !decodeLimit(c, &input, 16<<10) {
		return
	}
	stored, _ := h.users.User(actor)
	item, _, ok := employeePersonalSource(stored, input.Source)
	if !ok || len(input.Models) == 0 || len(input.Models) > 50 {
		employeeError(c, repo.ErrEmployeeConnection)
		return
	}
	models := make([]string, 0, len(input.Models))
	for _, model := range input.Models {
		model = strings.TrimSpace(model)
		if model == "" || len(model) > 250 || (len(item.Models) > 0 && !employeeModelAllowed(item.Models, model)) {
			employeeError(c, repo.ErrEmployeeConnection)
			return
		}
		if !employeeModelAllowed(models, model) {
			models = append(models, model)
		}
	}
	if name := strings.TrimSpace(input.Name); name != "" && len([]rune(name)) <= 80 {
		item.Name = name
	}
	item.ID, item.ProjectID, item.Models = newChatID("conn"), project.ID, models
	if err := h.employees.SaveEmployeeConnection(item, admin); err != nil {
		employeeError(c, err)
		return
	}
	item.BaseURL = ""
	c.JSON(http.StatusCreated, item)
}

func (h *testEmployeesHandlers) revokeConnection(c *gin.Context) {
	actor, ok := h.requestUser(c.Request, true)
	if !ok || actor.ID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	if err := h.employees.RevokeEmployeeConnection(c.Param("id"), c.Param("connectionId"), actor.ID, h.isPlatformAdmin(c.Request)); err != nil {
		employeeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *testEmployeesHandlers) testConnection(c *gin.Context) {
	project, _, _, ok := h.manager(c)
	if !ok {
		return
	}
	var input struct {
		ConnectionID string                   `json:"connectionId"`
		UserID       int64                    `json:"userId"`
		Model        string                   `json:"model"`
		Connection   *employeeConnectionInput `json:"connection"`
	}
	if !decodeLimit(c, &input, 16<<10) {
		return
	}
	var connection domain.EmployeeModelConnection
	var key string
	if input.Connection == nil {
		var err error
		connection, key, err = h.resolveEmployeeConnection(project.ID, input.ConnectionID, input.Model, input.UserID)
		if err != nil {
			employeeError(c, err)
			return
		}
	} else {
		var valid bool
		connection, valid = employeeCustomConnection(project.ID, *input.Connection)
		if !valid {
			employeeError(c, repo.ErrEmployeeConnection)
			return
		}
		connection.AllowPrivate = true
		key, input.Model = input.Connection.APIKey, connection.Models[0]
	}
	if _, err := h.model(c.Request.Context(), connection, key, input.Model, "Reply briefly.", "Reply OK."); err != nil {
		var modelErr employeeModelError
		if errors.As(err, &modelErr) {
			c.JSON(http.StatusBadGateway, gin.H{"error": employeeModelErrorLabel(modelErr.Code), "code": modelErr.Code})
			return
		}
		employeeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "model": input.Model})
}

func employeeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repo.ErrEmployeeForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, repo.ErrEmployeeConflict), errors.Is(err, repo.ErrLoginTaken):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, repo.ErrEmployeeConnection):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "记录不存在"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存或读取员工配置失败"})
	}
}

func employeeModelErrorLabel(code string) string {
	switch code {
	case "endpoint_blocked":
		return "平台未允许访问此模型地址"
	case "endpoint_unreachable":
		return "模型地址不可达或请求超时"
	case "authentication_failed":
		return "模型服务鉴权失败"
	case "model_or_endpoint_not_found":
		return "模型或接口地址不存在"
	case "provider_unavailable":
		return "模型服务暂时不可用"
	case "connection_unavailable":
		return "模型连接不可用或授权已撤回"
	case "role_unavailable":
		return "员工绑定的角色不可用"
	default:
		return "模型服务返回了无法使用的结果"
	}
}

func projectHasRoomMember(project domain.Project, userID int64) bool {
	if userID <= 0 {
		return false
	}
	if project.OwnerID == userID {
		return true
	}
	for _, member := range project.Members {
		if member.UserID == userID {
			return true
		}
	}
	return false
}
