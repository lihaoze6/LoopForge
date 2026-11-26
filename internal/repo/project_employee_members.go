package repo

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agentflow/internal/domain"
	"gorm.io/gorm"
)

func (r *ProjectsRepo) lockEmployeeProject(tx *gorm.DB, projectID string, actor int64, admin bool) error {
	var row projectRow
	if err := r.locking(tx).Where("id = ?", projectID).Take(&row).Error; err != nil {
		return err
	}
	var project domain.Project
	if err := json.Unmarshal(row.Payload, &project); err != nil {
		return err
	}
	if project.ArchivedAt != nil {
		return gorm.ErrRecordNotFound
	}
	if !admin && (row.OwnerUserID == nil || *row.OwnerUserID != actor) {
		return ErrEmployeeForbidden
	}
	return nil
}

func (r *ProjectsRepo) appendProjectMember(tx *gorm.DB, projectID string, userID int64) error {
	return tx.Create(&projectMemberRow{ProjectID: projectID, UserID: userID, Role: "member", Created: timePtr(time.Now().UTC())}).Error
}

func (r *ProjectsRepo) checkEmployeeMember(tx *gorm.DB, projectID string, userID int64) error {
	var project projectRow
	if err := tx.Select("owner_user_id").Where("id = ?", projectID).Take(&project).Error; err != nil {
		return err
	}
	if project.OwnerUserID != nil && *project.OwnerUserID == userID {
		return nil
	}
	var row projectMemberRow
	if err := tx.Where("project_id = ? AND user_id = ?", projectID, userID).Take(&row).Error; err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(row.Role), "viewer") {
		return ErrEmployeeForbidden
	}
	return nil
}

func (r *ProjectsRepo) groupMember(tx *gorm.DB, projectID string, userID int64) (bool, error) {
	var project projectRow
	if err := tx.Select("owner_user_id").Where("id = ?", projectID).Take(&project).Error; err != nil {
		return false, err
	}
	if project.OwnerUserID != nil && *project.OwnerUserID == userID {
		return true, nil
	}
	var count int64
	err := tx.Model(&projectMemberRow{}).Where("project_id = ? AND user_id = ?", projectID, userID).Count(&count).Error
	return count != 0, err
}

func (r *ProjectsRepo) checkGroupWrite(tx *gorm.DB, projectID string, userID int64) error {
	err := r.checkEmployeeMember(tx, projectID, userID)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	// Keep the existing development/legacy behavior for projects with no roster.
	var project projectRow
	if err := tx.Select("owner_user_id").Where("id = ?", projectID).Take(&project).Error; err != nil {
		return err
	}
	var size int64
	if err := tx.Model(&projectMemberRow{}).Where("project_id = ?", projectID).Count(&size).Error; err != nil {
		return err
	}
	if project.OwnerUserID == nil && size == 0 {
		return nil
	}
	return ErrEmployeeForbidden
}
