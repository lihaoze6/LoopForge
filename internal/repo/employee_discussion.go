package repo

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"agentflow/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	discussionWorkerLock     = "_employee_discussion_worker"
	maxDiscussionEvaluations = 24
	employeeDiscussionLease  = 90 * time.Second
)

type employeeDiscussionRow struct {
	RoomID      string `gorm:"primaryKey;size:64"`
	Enabled     bool
	ReplyLimit  int
	RoundID     string `gorm:"size:64"`
	Replies     int
	Evaluations int
	LeaseUntil  *time.Time
}

func (employeeDiscussionRow) TableName() string { return "employee_room_discussions" }

type employeeDiscussionJobRow struct {
	ID               int64  `gorm:"primaryKey;autoIncrement"`
	ProjectID        string `gorm:"size:64;index"`
	RoomID           string `gorm:"size:64;index"`
	RoundID          string `gorm:"size:64"`
	TriggerID        string `gorm:"size:64;uniqueIndex:uq_employee_discussion_trigger"`
	UserID           int64  `gorm:"uniqueIndex:uq_employee_discussion_trigger"`
	EmployeeRevision int64
	Mentioned        bool
	Priority         int
	Status           string `gorm:"size:16;index:idx_employee_discussion_pending"`
	Attempts         int
	LeaseUntil       *time.Time
	AvailableAt      time.Time `gorm:"index:idx_employee_discussion_pending,priority:2"`
	Model            string    `gorm:"size:250"`
	ErrorCode        string    `gorm:"size:64"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (employeeDiscussionJobRow) TableName() string { return "employee_discussion_jobs" }

type EmployeeDiscussionJob struct {
	ID               int64     `json:"id"`
	RoomID           string    `json:"roomId"`
	UserID           int64     `json:"userId"`
	Model            string    `json:"model,omitempty"`
	Status           string    `json:"status"`
	ErrorCode        string    `json:"errorCode,omitempty"`
	UpdatedAt        time.Time `json:"updatedAt"`
	ProjectID        string    `json:"-"`
	RoundID          string    `json:"-"`
	TriggerID        string    `json:"-"`
	Mentioned        bool      `json:"-"`
	Attempt          int       `json:"-"`
	EmployeeRevision int64     `json:"-"`
}

func employeeJobView(row employeeDiscussionJobRow) EmployeeDiscussionJob {
	return EmployeeDiscussionJob{ID: row.ID, RoomID: row.RoomID, UserID: row.UserID, Model: row.Model, Status: row.Status, ErrorCode: row.ErrorCode, UpdatedAt: row.UpdatedAt, ProjectID: row.ProjectID, RoundID: row.RoundID, TriggerID: row.TriggerID, Mentioned: row.Mentioned, Attempt: row.Attempts, EmployeeRevision: row.EmployeeRevision}
}

func (r *TestEmployeesRepo) DiscussionSettings(roomID string) (domain.RoomDiscussionSettings, error) {
	var row employeeDiscussionRow
	err := r.DB.Where("room_id = ?", roomID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.RoomDiscussionSettings{ReplyLimit: 4}, nil
	}
	return domain.RoomDiscussionSettings{Enabled: row.Enabled, ReplyLimit: row.ReplyLimit}, err
}

func (r *TestEmployeesRepo) SetDiscussionSettings(room domain.ChatRoom, actor int64, admin bool, value domain.RoomDiscussionSettings) error {
	return r.DB.Transaction(func(tx *gorm.DB) error {
		if err := r.projects.lockEmployeeProject(tx, room.ProjectID, actor, admin); err != nil {
			return err
		}
		if _, err := r.rooms.groupRoom(tx, room.ID, true); err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&employeeDiscussionRow{RoomID: discussionWorkerLock}).Error; err != nil {
			return err
		}
		updates := map[string]any{"enabled": value.Enabled, "reply_limit": value.ReplyLimit}
		if !value.Enabled {
			updates["round_id"] = ""
			if err := r.cancelDiscussionJobs(tx, room.ID, ""); err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{DoUpdates: clause.Assignments(updates)}).
			Create(&employeeDiscussionRow{RoomID: room.ID, Enabled: value.Enabled, ReplyLimit: value.ReplyLimit}).Error
	})
}

func (r *TestEmployeesRepo) PostGroupMessage(room domain.ChatRoom, msg domain.ChatMessage, clientID string, admin, authenticated bool) (domain.ChatMessage, bool, error) {
	var created bool
	err := r.DB.Transaction(func(tx *gorm.DB) error {
		if err := r.projects.lockEmployeeProject(tx, room.ProjectID, msg.UserID, true); err != nil {
			return err
		}
		if _, err := r.rooms.groupRoom(tx, room.ID, true); err != nil {
			return err
		}
		if authenticated && !admin {
			if err := r.projects.checkGroupWrite(tx, room.ProjectID, msg.UserID); err != nil {
				return err
			}
		}
		if err := r.checkMentionTargets(tx, room.ProjectID, msg.Mentions); err != nil {
			return err
		}
		var err error
		msg, created, err = r.rooms.addTextMessage(tx, msg, clientID)
		if err != nil || !created || !authenticated {
			return err
		}
		var discussion employeeDiscussionRow
		err = tx.Where("room_id = ? AND enabled = ?", room.ID, true).Take(&discussion).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		discussion.RoundID, discussion.Replies, discussion.Evaluations = msg.ID, 0, 0
		if err := tx.Save(&discussion).Error; err != nil {
			return err
		}
		if err := r.cancelDiscussionJobs(tx, room.ID, msg.ID); err != nil {
			return err
		}
		return r.enqueueDiscussionJobs(tx, room, discussion.RoundID, msg)
	})
	return msg, created, err
}

func (r *TestEmployeesRepo) checkMentionTargets(tx *gorm.DB, projectID string, mentions []domain.RoomMention) error {
	for _, mention := range mentions {
		ok, err := r.projects.groupMember(tx, projectID, mention.UserID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrRoomMention
		}
	}
	return nil
}

func (r *TestEmployeesRepo) cancelDiscussionJobs(tx *gorm.DB, roomID, currentRound string) error {
	return tx.Model(&employeeDiscussionJobRow{}).Where("room_id = ? AND round_id <> ? AND status IN ?", roomID, currentRound, []string{"pending", "running"}).Update("status", "cancelled").Error
}

func (r *TestEmployeesRepo) enqueueDiscussionJobs(tx *gorm.DB, room domain.ChatRoom, roundID string, msg domain.ChatMessage) error {
	var rows []testEmployeeRow
	if err := tx.Where("project_id = ?", room.ProjectID).Order("user_id").Find(&rows).Error; err != nil {
		return err
	}
	targets := map[int64]int{}
	for _, mention := range msg.Mentions {
		if _, exists := targets[mention.UserID]; !exists {
			targets[mention.UserID] = len(targets)
		}
	}
	var candidates []domain.TestEmployee
	for _, row := range rows {
		var item domain.TestEmployee
		if err := json.Unmarshal(row.Payload, &item); err != nil {
			return err
		}
		if item.UserID == msg.UserID || item.Paused {
			continue
		}
		if err := r.projects.checkEmployeeMember(tx, room.ProjectID, item.UserID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrEmployeeForbidden) {
				continue
			}
			return err
		}
		candidates = append(candidates, item)
	}
	if len(candidates) > 1 {
		// Use the trigger time to vary the initial candidate across messages.
		shift := int(uint64(msg.CreatedAt.UnixNano()) % uint64(len(candidates)))
		candidates = slices.Concat(candidates[shift:], candidates[:shift])
	}
	jobs := make([]employeeDiscussionJobRow, 0, len(candidates))
	now := time.Now().UTC()
	for index, item := range candidates {
		position, mentioned := targets[item.UserID]
		priority := len(targets) + index
		if mentioned {
			priority = position
		}
		jobs = append(jobs, employeeDiscussionJobRow{ProjectID: room.ProjectID, RoomID: room.ID, RoundID: roundID, TriggerID: msg.ID, UserID: item.UserID, EmployeeRevision: item.Revision, Mentioned: mentioned, Priority: priority, Status: "pending", AvailableAt: now, Model: item.Model})
	}
	if len(jobs) == 0 {
		return nil
	}
	slices.SortStableFunc(jobs, func(a, b employeeDiscussionJobRow) int {
		return cmp.Compare(a.Priority, b.Priority)
	})
	jobs = jobs[:min(len(jobs), maxDiscussionEvaluations)]
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&jobs).Error
}

func (r *TestEmployeesRepo) ClaimDiscussionJob(ctx context.Context) (EmployeeDiscussionJob, error) {
	var job employeeDiscussionJobRow
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var worker employeeDiscussionRow
		if err := r.locking(tx).Where("room_id = ?", discussionWorkerLock).Take(&worker).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if worker.LeaseUntil != nil && worker.LeaseUntil.After(now) {
			return gorm.ErrRecordNotFound
		}
		// Expired leases are reclaimable after a crashed process.
		if err := r.locking(tx).Where("(status = ? AND available_at <= ?) OR (status = ? AND lease_until <= ?)", "pending", now, "running", now).
			Order("mentioned DESC, created_at, priority, id").Take(&job).Error; err != nil {
			return err
		}
		lease := now.Add(employeeDiscussionLease)
		if err := tx.Model(&worker).Update("lease_until", lease).Error; err != nil {
			return err
		}
		job.Status, job.LeaseUntil = "running", &lease
		job.Attempts++
		return tx.Save(&job).Error
	})
	return employeeJobView(job), err
}

func (r *TestEmployeesRepo) DiscussionEmployee(job EmployeeDiscussionJob) (domain.TestEmployee, error) {
	var employee domain.TestEmployee
	err := r.DB.Transaction(func(tx *gorm.DB) error {
		_, round, current, err := r.activeDiscussion(tx, job)
		if err != nil {
			return err
		}
		employee = current
		// Retries of the same candidate do not start another evaluation.
		if job.Attempt > 1 {
			return nil
		}
		if round.Evaluations >= maxDiscussionEvaluations {
			return ErrRoomScope
		}
		return tx.Model(&round).UpdateColumn("evaluations", gorm.Expr("evaluations + 1")).Error
	})
	return employee, err
}

// DiscussionJobValid checks ongoing work without consuming another evaluation.
func (r *TestEmployeesRepo) DiscussionJobValid(job EmployeeDiscussionJob) bool {
	err := r.DB.Transaction(func(tx *gorm.DB) error {
		_, _, _, err := r.activeDiscussion(tx, job)
		return err
	})
	return err == nil
}

func (r *TestEmployeesRepo) activeDiscussion(tx *gorm.DB, job EmployeeDiscussionJob) (domain.ChatRoom, employeeDiscussionRow, domain.TestEmployee, error) {
	var round employeeDiscussionRow
	var employee domain.TestEmployee
	if err := r.projects.lockEmployeeProject(tx, job.ProjectID, 0, true); err != nil {
		return domain.ChatRoom{}, round, employee, err
	}
	room, err := r.rooms.groupRoom(tx, job.RoomID, true)
	if err != nil || room.ProjectID != job.ProjectID {
		return room, round, employee, ErrRoomScope
	}
	if err := tx.Where("room_id = ?", room.ID).Take(&round).Error; err != nil {
		return room, round, employee, err
	}
	if !round.Enabled || round.RoundID != job.RoundID || round.Replies >= round.ReplyLimit {
		return room, round, employee, ErrRoomScope
	}
	if err := r.projects.checkEmployeeMember(tx, room.ProjectID, job.UserID); err != nil {
		return room, round, employee, err
	}
	employee, err = r.testEmployee(tx, room.ProjectID, job.UserID)
	if err == nil && (employee.Paused || employee.Revision != job.EmployeeRevision) {
		err = ErrRoomScope
	}
	return room, round, employee, err
}

func (r *TestEmployeesRepo) FinishDiscussionJob(job EmployeeDiscussionJob, revision int64, status, code string, retry bool, message domain.ChatMessage, recheck func(domain.User, domain.EmployeeModelConnection) bool) error {
	return r.DB.Transaction(func(tx *gorm.DB) error {
		// Use the same lock order as manual sending and configuration writes.
		if err := r.projects.lockProjectInTx(tx, job.ProjectID); err != nil {
			return err
		}
		var worker employeeDiscussionRow
		if err := r.locking(tx).Where("room_id = ?", discussionWorkerLock).Take(&worker).Error; err != nil {
			return err
		}
		var row employeeDiscussionJobRow
		if err := r.locking(tx).Where("id = ?", job.ID).Take(&row).Error; err != nil {
			return err
		}
		if row.Attempts != job.Attempt || row.LeaseUntil == nil || worker.LeaseUntil == nil || !row.LeaseUntil.Equal(*worker.LeaseUntil) {
			return nil
		}
		if err := tx.Model(&worker).Update("lease_until", nil).Error; err != nil {
			return err
		}
		if row.Status != "running" {
			return nil
		}

		if status == "posted" || retry {
			room, round, employee, err := r.activeDiscussion(tx, job)
			if err != nil || employee.Revision != revision {
				status, code, retry = "cancelled", "", false
			} else if status == "posted" {
				connection, _, err := r.employeeConnection(tx, room.ProjectID, employee.ConnectionID)
				authorized := err == nil && containsModel(connection.Models, employee.Model)
				if authorized && connection.Source != "" {
					users := &UsersRepo{deps: &deps{DB: tx}}
					owner, ok := users.User(connection.OwnerID)
					authorized = ok && recheck != nil && recheck(owner, connection)
				}
				if !authorized {
					status, code = "cancelled", ""
				} else {
					if err := r.checkMentionTargets(tx, room.ProjectID, message.Mentions); errors.Is(err, ErrRoomMention) {
						message.Mentions = nil
					} else if err != nil {
						return err
					}
					if _, _, err := r.rooms.addTextMessage(tx, message, ""); err != nil {
						return err
					}
					round.Replies++
					if err := tx.Model(&round).Update("replies", round.Replies).Error; err != nil {
						return err
					}
					if round.Replies < round.ReplyLimit && round.Evaluations < maxDiscussionEvaluations {
						if err := r.enqueueDiscussionJobs(tx, room, round.RoundID, message); err != nil {
							return err
						}
					} else if err := tx.Model(&employeeDiscussionJobRow{}).Where("room_id = ? AND round_id = ? AND status = ?", room.ID, round.RoundID, "pending").Update("status", "cancelled").Error; err != nil {
						return err
					}
				}
			}
		}
		row.Status, row.ErrorCode, row.LeaseUntil = status, code, nil
		if retry && row.Attempts < 3 {
			row.Status = "pending"
			row.AvailableAt = time.Now().UTC().Add(time.Duration(row.Attempts*5) * time.Second)
		}
		return tx.Save(&row).Error
	})
}

func (r *TestEmployeesRepo) DiscussionFailures(projectID string) ([]EmployeeDiscussionJob, error) {
	var rows []employeeDiscussionJobRow
	if err := r.DB.Where("project_id = ? AND status = ?", projectID, "failed").Order("updated_at DESC, id DESC").Limit(30).Find(&rows).Error; err != nil {
		return nil, err
	}
	jobs := make([]EmployeeDiscussionJob, 0, len(rows))
	for _, row := range rows {
		jobs = append(jobs, employeeJobView(row))
	}
	return jobs, nil
}

func (r *TestEmployeesRepo) RetryDiscussionJob(projectID string, id, actor int64, admin bool) error {
	return r.DB.Transaction(func(tx *gorm.DB) error {
		if err := r.projects.lockEmployeeProject(tx, projectID, actor, admin); err != nil {
			return err
		}
		var row employeeDiscussionJobRow
		if err := tx.Where("id = ? AND project_id = ? AND status = ?", id, projectID, "failed").Take(&row).Error; err != nil {
			return err
		}
		job := employeeJobView(row)
		current, err := r.testEmployee(tx, projectID, job.UserID)
		if err != nil {
			return err
		}
		job.EmployeeRevision = current.Revision
		if _, _, _, err := r.activeDiscussion(tx, job); err != nil {
			return err
		}
		return tx.Model(&row).Updates(map[string]any{"status": "pending", "attempts": 1, "employee_revision": current.Revision, "model": current.Model, "error_code": "", "available_at": time.Now().UTC()}).Error
	})
}
