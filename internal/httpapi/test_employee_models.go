package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
	"agentflow/internal/runtime"
)

const employeeModelTimeout = 45 * time.Second

type employeeModelError struct {
	Code      string
	Temporary bool
}

func (e employeeModelError) Error() string { return e.Code }

func employeeProtocol(protocol string) bool {
	switch protocol {
	case domain.AgentCLIProtocolChat, domain.AgentCLIProtocolResponses, domain.AgentCLIProtocolMessages:
		return true
	default:
		return false
	}
}

func employeeModelEndpoint(protocol, baseURL string) (string, bool) {
	if !employeeProtocol(protocol) {
		return "", false
	}
	target, ok := buildApprovalHTTPTarget(protocol, baseURL, "placeholder", "placeholder")
	if !ok {
		return "", false
	}
	u, err := url.Parse(target.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	return u.String(), true
}

func employeeModelClient(endpoint string, allowPrivate bool) *http.Client {
	u, _ := url.Parse(endpoint)
	var allowed []netip.Prefix
	ip, err := netip.ParseAddr(u.Hostname())
	if err == nil {
		port := u.Port()
		if port == "" {
			port = "443"
			if u.Scheme == "http" {
				port = "80"
			}
		}
		portNumber, _ := strconv.ParseUint(port, 10, 16)
		if allowPrivate {
			allowed = append(allowed, netip.PrefixFrom(ip.Unmap(), ip.Unmap().BitLen()))
		} else {
			for _, entry := range strings.Split(os.Getenv("AGENTFLOW_EMPLOYEE_MODEL_ALLOW_ADDRS"), ",") {
				addr, err := netip.ParseAddrPort(strings.TrimSpace(entry))
				if err == nil && addr.Addr().Unmap() == ip.Unmap() && addr.Port() == uint16(portNumber) {
					allowed = append(allowed, netip.PrefixFrom(ip.Unmap(), ip.Unmap().BitLen()))
					break
				}
			}
		}
	}
	return newApprovalEgressClientWithResolver(employeeModelTimeout, allowed, nil)
}

type employeeModelCaller func(context.Context, domain.EmployeeModelConnection, string, string, string, string) (string, error)

func callEmployeeModel(ctx context.Context, connection domain.EmployeeModelConnection, key, model, system, prompt string) (string, error) {
	endpoint, ok := employeeModelEndpoint(connection.Protocol, connection.BaseURL)
	if !ok {
		return "", employeeModelError{Code: "invalid_endpoint"}
	}
	payload := map[string]any{"model": model}
	switch connection.Protocol {
	case domain.AgentCLIProtocolResponses:
		payload["instructions"], payload["input"], payload["max_output_tokens"] = system, prompt, 1024
	case domain.AgentCLIProtocolMessages:
		payload["system"], payload["max_tokens"] = system, 1024
		payload["messages"] = []map[string]string{{"role": "user", "content": prompt}}
	default:
		payload["messages"] = []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": prompt}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", employeeModelError{Code: "invalid_endpoint"}
	}
	req.Header.Set("Content-Type", "application/json")
	if connection.Protocol == domain.AgentCLIProtocolMessages {
		req.Header.Set("anthropic-version", "2023-06-01")
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
	} else if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := employeeModelClient(endpoint, connection.AllowPrivate).Do(req)
	if err != nil {
		var policyErr errApprovalPolicyBlocked
		if errors.As(err, &policyErr) {
			return "", employeeModelError{Code: "endpoint_blocked"}
		}
		return "", employeeModelError{Code: "endpoint_unreachable", Temporary: true}
	}
	defer resp.Body.Close()
	switch status := resp.StatusCode; {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "", employeeModelError{Code: "authentication_failed"}
	case status == http.StatusNotFound:
		return "", employeeModelError{Code: "model_or_endpoint_not_found"}
	case status == http.StatusTooManyRequests || status >= 500:
		return "", employeeModelError{Code: "provider_unavailable", Temporary: true}
	case status < 200 || status >= 300:
		return "", employeeModelError{Code: "provider_rejected"}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (512<<10)+1))
	if err != nil {
		return "", employeeModelError{Code: "response_unavailable", Temporary: true}
	}
	if len(raw) > 512<<10 {
		return "", employeeModelError{Code: "invalid_response"}
	}
	result, err := extractApprovalHTTPText(connection.Protocol, raw)
	if err != nil || strings.TrimSpace(result) == "" {
		return "", employeeModelError{Code: "invalid_response"}
	}
	return result, nil
}

func employeePersonalSource(user domain.User, source string) (domain.EmployeeModelConnection, string, bool) {
	var models []domain.CLIModel
	var protocol, baseURL, key string
	switch source {
	case "codex":
		protocol, baseURL, key, models = domain.AgentCLIProtocolResponses, user.CodexBaseURL, user.CodexAPIKey, user.CodexModels
	case "claude-code":
		protocol, baseURL, key, models = domain.AgentCLIProtocolMessages, user.ClaudeCodeBaseURL, user.ClaudeCodeAPIKey, user.ClaudeCodeModels
	default:
		if !runtime.IsAgentCLI(source) {
			return domain.EmployeeModelConnection{}, "", false
		}
		credential, exists := user.AgentCLICredentials[source]
		if !exists {
			return domain.EmployeeModelConnection{}, "", false
		}
		protocol, baseURL, key, models = runtime.AgentCLIProtocol(source, credential.Protocol), credential.BaseURL, credential.APIKey, credential.Models
		if baseURL == "" {
			if source != runtime.AgentCLIDeepSeek {
				return domain.EmployeeModelConnection{}, "", false
			}
			baseURL = "https://api.deepseek.com/v1"
		}
	}
	endpoint, ok := employeeModelEndpoint(protocol, baseURL)
	if !ok || strings.TrimSpace(key) == "" {
		return domain.EmployeeModelConnection{}, "", false
	}
	connection := domain.EmployeeModelConnection{OwnerID: user.ID, Source: source, Name: source, Protocol: protocol, BaseURL: endpoint}
	for _, model := range models {
		connection.Models = append(connection.Models, model.ID)
	}
	return connection, key, true
}

func (h *testEmployeesHandlers) resolveEmployeeConnection(projectID, id, model string, userID int64) (domain.EmployeeModelConnection, string, error) {
	connection, key, err := h.employees.EmployeeConnection(projectID, id)
	if err != nil || (connection.EmployeeID != 0 && connection.EmployeeID != userID) || !employeeModelAllowed(connection.Models, model) {
		return connection, "", repo.ErrEmployeeConnection
	}
	connection.AllowPrivate = connection.Source == ""
	if connection.Source != "" {
		project, projectOK := h.projects.Project(projectID)
		owner, ownerOK := h.users.User(connection.OwnerID)
		connection.AllowPrivate = projectOK && ownerOK &&
			(project.OwnerID == owner.ID || domain.IsPlatformAdmin(owner))
	}
	if connection.Source == "" {
		return connection, key, nil
	}
	user, ok := h.users.User(connection.OwnerID)
	if !ok {
		return connection, "", repo.ErrEmployeeConnection
	}
	live, key, ok := employeePersonalSource(user, connection.Source)
	if !ok || live.BaseURL != connection.BaseURL || live.Protocol != connection.Protocol || (len(live.Models) > 0 && !employeeModelAllowed(live.Models, model)) {
		return connection, "", repo.ErrEmployeeConnection
	}
	return connection, key, nil
}

func employeeModelAllowed(models []string, model string) bool {
	for _, allowed := range models {
		if model == allowed {
			return true
		}
	}
	return false
}
