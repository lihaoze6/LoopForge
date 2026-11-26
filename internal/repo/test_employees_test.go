package repo

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"agentflow/internal/domain"
	"gorm.io/gorm"
)

func TestEmployeeAccountMembershipAndRoomLifecycle(t *testing.T) {
	checkEmployeeRoomLifecycle(t, OpenTest)
}

func TestMySQLEmployeeAccountMembershipAndRoomLifecycle(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("AGENTFLOW_TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("AGENTFLOW_TEST_MYSQL_DSN is not set")
	}
	checkEmployeeRoomLifecycle(t, func() (*Store, error) { return Open(dsn) })
}

func checkEmployeeRoomLifecycle(t *testing.T, open func() (*Store, error)) {
	t.Helper()
	t.Setenv("AGENTFLOW_EMPLOYEE_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	store, err := open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	prefix := fmt.Sprintf("employee-%x", time.Now().UnixNano())
	projectID, roomID, spaceID := prefix+"-p", prefix+"-r", prefix+"-s"
	t.Cleanup(func() {
		store.DB.Where("project_id = ?", projectID).Delete(&employeeDiscussionJobRow{})
		store.DB.Where("room_id = ?", roomID).Delete(&employeeDiscussionRow{})
		store.DB.Where("project_id = ?", projectID).Delete(&employeeConnectionRow{})
		store.DB.Where("project_id = ?", projectID).Delete(&testEmployeeRow{})
		store.DB.Where("room_id = ?", roomID).Delete(&roomMentionRow{})
		store.DB.Where("room_id IN ?", []string{roomID, spaceID}).Delete(&chatMessageRow{})
		store.DB.Where("id = ?", spaceID).Delete(&requirementSpaceRow{})
		store.DB.Where("id IN ?", []string{roomID, spaceID}).Delete(&chatRoomRow{})
		store.DB.Where("project_id = ?", projectID).Delete(&projectMemberRow{})
		store.DB.Where("id = ?", projectID).Delete(&projectRow{})
		for _, suffix := range []string{"-owner", "-viewer", "-lin"} {
			if user, exists := store.UserByLoginName(prefix + suffix); exists {
				store.DB.Delete(&localAccountRow{}, "user_id = ?", user.ID)
				store.DB.Delete(&userRow{}, "id = ?", user.ID)
			}
		}
	})
	owner, err := store.CreateLocalAccount(prefix+"-owner", "owner-password", false)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := store.CreateLocalAccount(prefix+"-viewer", "viewer-password", false)
	if err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: projectID, Key: "EMP", Name: "Project", OwnerID: owner.ID, Members: []domain.ProjectMember{{UserID: viewer.ID, Role: "viewer"}}}
	if err := store.PutProject(project); err != nil {
		t.Fatal(err)
	}
	stale, _ := store.Project(project.ID)
	employee := domain.TestEmployee{ProjectID: project.ID, Model: "test-model", Duty: "test engineer"}
	invalid := employee
	invalid.ConnectionID = "unknown"
	if _, err := store.CreateTestEmployee(owner.ID, false, prefix+"-rollback", "Rollback", "test-password", invalid, nil, ""); !errors.Is(err, ErrEmployeeConnection) {
		t.Fatalf("invalid connection: %v", err)
	}
	if _, exists := store.UserByLoginName(prefix + "-rollback"); exists {
		t.Fatal("failed creation left an account")
	}
	connection := &domain.EmployeeModelConnection{ID: prefix + "-conn", ProjectID: project.ID, Name: "Test connection", Protocol: "chat", Models: []string{employee.Model}}
	employee, err = store.CreateTestEmployee(owner.ID, false, prefix+"-lin", "小林", "test-password", employee, connection, "model-key")
	if err != nil {
		t.Fatal(err)
	}
	if !store.CheckLocalPassword(employee.UserID, "test-password") {
		t.Fatal("test employee cannot log in with the local password")
	}
	if _, err := store.CreateTestEmployee(owner.ID, false, strings.ToUpper(prefix+"-lin"), "Duplicate", "changed-password", employee, nil, ""); !errors.Is(err, ErrLoginTaken) {
		t.Fatalf("duplicate account: %v", err)
	}
	if !store.CheckLocalPassword(employee.UserID, "test-password") {
		t.Fatal("duplicate creation replaced password")
	}
	if err := store.PutProject(stale); !errors.Is(err, ErrProjectMembersConflict) {
		t.Fatalf("stale member replacement: %v", err)
	}
	stale.Name, stale.Members = "Renamed", nil
	if err := store.PutProject(stale); err != nil {
		t.Fatal(err)
	}
	fresh, _ := store.Project(project.ID)
	if len(fresh.Members) != 2 || fresh.Members[0].Role != "viewer" {
		t.Fatalf("roster changed after saving metadata: %#v", fresh.Members)
	}

	room := domain.ChatRoom{ID: roomID, Name: "Room", ProjectID: project.ID, Kind: domain.RoomKindProject}
	if err := store.PutRoom(room); err != nil {
		t.Fatal(err)
	}
	space := domain.ChatRoom{ID: spaceID, Name: "Space", ProjectID: project.ID, Kind: domain.RoomKindRequirement}
	if err := store.CreateRequirementRoom(space, domain.RequirementSpace{ID: space.ID, ProjectID: project.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GroupRoom(space.ID); !errors.Is(err, ErrRoomScope) {
		t.Fatalf("requirement space available as a group: %v", err)
	}
	if err := store.SetDiscussionSettings(room, owner.ID, false, domain.RoomDiscussionSettings{Enabled: true, ReplyLimit: 4}); err != nil {
		t.Fatal(err)
	}
	msg := domain.ChatMessage{RoomID: room.ID, UserID: owner.ID, Body: "@小林", Kind: domain.ChatKindText, CreatedAt: time.Now().UTC(), Mentions: []domain.RoomMention{{UserID: employee.UserID, Start: 0, End: 3}}}
	saved, created, err := store.PostGroupMessage(room, msg, "client-once", false, true)
	if err != nil || !created {
		t.Fatalf("post: %v, %v", created, err)
	}
	again, created, err := store.PostGroupMessage(room, msg, "client-once", false, true)
	if err != nil || created || again.ID != saved.ID {
		t.Fatalf("idempotent retry: %#v, %v, %v", again, created, err)
	}
	inbox, unread, err := store.RoomMentions(employee.UserID, project.ID)
	if err != nil || unread != 1 || len(inbox) != 1 || inbox[0].Message.ID != saved.ID {
		t.Fatalf("mention inbox: %#v, %d, %v", inbox, unread, err)
	}
	job, err := store.ClaimDiscussionJob(context.Background())
	if err != nil || !job.Mentioned || job.UserID != employee.UserID {
		t.Fatalf("claim: %#v, %v", job, err)
	}
	if _, err := store.ClaimDiscussionJob(context.Background()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("discussion workers overlapped: %v", err)
	}
	prepared, err := store.DiscussionEmployee(job)
	if err != nil {
		t.Fatal(err)
	}
	paused := true
	if _, err := store.UpdateTestEmployee(employee.UserID, false, employee, &paused, nil, ""); err != nil {
		t.Fatal(err)
	}
	reply := domain.ChatMessage{ID: "should-not-post", RoomID: room.ID, UserID: employee.UserID, Body: "reply", Kind: domain.ChatKindText, CreatedAt: time.Now().UTC()}
	if err := store.FinishDiscussionJob(job, prepared.Revision, "posted", "", false, reply, nil); err != nil {
		t.Fatal(err)
	}
	if messages := store.Messages(room.ID, 10); len(messages) != 1 {
		t.Fatalf("paused employee published a reply: %#v", messages)
	}
}
