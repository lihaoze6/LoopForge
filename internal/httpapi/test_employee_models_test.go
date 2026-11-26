package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow/internal/domain"
)

func TestEmployeeModelProtocols(t *testing.T) {
	for _, test := range []struct {
		name, path, header, key, reply string
	}{
		{"chat", "/v1/chat/completions", "Authorization", "Bearer test", `{"choices":[{"message":{"content":"ok"}}]}`},
		{"responses", "/v1/responses", "Authorization", "Bearer test", `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`},
		{"messages", "/v1/messages", "X-Api-Key", "test", `{"content":[{"type":"text","text":"ok"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path || r.Method != http.MethodPost || r.Header.Get(test.header) != test.key {
					t.Errorf("unexpected request: %s %s %s", r.Method, r.URL.Path, r.Header.Get(test.header))
				}
				fmt.Fprint(w, test.reply)
			}))
			t.Cleanup(server.Close)
			t.Setenv("AGENTFLOW_EMPLOYEE_MODEL_ALLOW_ADDRS", strings.TrimPrefix(server.URL, "http://"))
			connection := domain.EmployeeModelConnection{Protocol: test.name, BaseURL: server.URL + "/v1"}
			text, err := callEmployeeModel(context.Background(), connection, "test", "model", "system", "prompt")
			if err != nil || text != "ok" {
				t.Fatalf("got %q, %v", text, err)
			}
		})
	}
}

func TestEmployeeModelConnectionAllowsExactPrivateEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(server.Close)
	t.Setenv("AGENTFLOW_EMPLOYEE_MODEL_ALLOW_ADDRS", "")
	connection := domain.EmployeeModelConnection{
		Protocol: "chat", BaseURL: server.URL + "/v1", AllowPrivate: true,
	}
	text, err := callEmployeeModel(context.Background(), connection, "test", "model", "system", "prompt")
	if err != nil || text != "ok" {
		t.Fatalf("got %q, %v", text, err)
	}
}

func TestRoomMentionRangesUseCodePoints(t *testing.T) {
	text := []rune("🙂 @小林")
	if !validRoomMentions(text, []domain.RoomMention{{UserID: 1, Start: 2, End: 5}}) {
		t.Fatal("valid code point range rejected")
	}
	if validRoomMentions(text, []domain.RoomMention{{UserID: 1, Start: 3, End: 5}}) {
		t.Fatal("range without @ accepted")
	}
}
