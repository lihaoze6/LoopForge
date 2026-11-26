package repo

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"agentflow/internal/domain"
	"gorm.io/gorm"
)

var (
	ErrEmployeeForbidden  = errors.New("只有平台管理员或项目 Owner 可以管理测试员工")
	ErrEmployeeConflict   = errors.New("员工配置已变化，请刷新后重试")
	ErrEmployeeConnection = errors.New("模型连接不可用或未授权")
)

type testEmployeeRow struct {
	ProjectID string `gorm:"primaryKey;size:64"`
	UserID    int64  `gorm:"primaryKey;autoIncrement:false"`
	Payload   []byte `gorm:"type:json;not null"`
}

func (testEmployeeRow) TableName() string { return "project_test_employees" }

type employeeConnectionRow struct {
	ID        string `gorm:"primaryKey;size:64"`
	ProjectID string `gorm:"size:64;index;not null"`
	Payload   []byte `gorm:"type:json;not null"`
	Secret    []byte `gorm:"type:blob"`
}

func (employeeConnectionRow) TableName() string { return "employee_model_connections" }

type TestEmployeesRepo struct {
	*deps
	users    *UsersRepo
	projects *ProjectsRepo
	rooms    *RoomsRepo
}

func (r *TestEmployeesRepo) CreateTestEmployee(actor int64, admin bool, login, name, password string, employee domain.TestEmployee, connection *domain.EmployeeModelConnection, key string) (domain.TestEmployee, error) {
	if IsAllowlistedPlatformAdmin(login) {
		return employee, ErrLoginTaken
	}
	hash, err := localPasswordHash(password)
	if err != nil {
		return employee, err
	}
	err = r.DB.Transaction(func(tx *gorm.DB) error {
		if err := r.projects.lockEmployeeProject(tx, employee.ProjectID, actor, admin); err != nil {
			return err
		}
		user, err := r.users.createLocalAccount(tx, login, name, hash, false)
		if err != nil {
			return err
		}
		employee.UserID, employee.Revision = user.ID, 1
		if err := r.projects.appendProjectMember(tx, employee.ProjectID, user.ID); err != nil {
			return err
		}
		if err := r.bindEmployeeConnection(tx, &employee, connection, key); err != nil {
			return err
		}
		return tx.Create(&testEmployeeRow{ProjectID: employee.ProjectID, UserID: user.ID, Payload: mustJSON(employee)}).Error
	})
	return employee, err
}

func (r *TestEmployeesRepo) TestEmployees(projectID string) ([]domain.TestEmployee, error) {
	var rows []testEmployeeRow
	if err := r.DB.Where("project_id = ?", projectID).Order("user_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]domain.TestEmployee, 0, len(rows))
	for _, row := range rows {
		var item domain.TestEmployee
		if err := json.Unmarshal(row.Payload, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (r *TestEmployeesRepo) testEmployee(tx *gorm.DB, projectID string, userID int64) (domain.TestEmployee, error) {
	var employee domain.TestEmployee
	var row testEmployeeRow
	if err := tx.Where("project_id = ? AND user_id = ?", projectID, userID).Take(&row).Error; err != nil {
		return employee, err
	}
	err := json.Unmarshal(row.Payload, &employee)
	return employee, err
}

func (r *TestEmployeesRepo) UpdateTestEmployee(actor int64, admin bool, employee domain.TestEmployee, paused *bool, connection *domain.EmployeeModelConnection, key string) (domain.TestEmployee, error) {
	err := r.DB.Transaction(func(tx *gorm.DB) error {
		selfPause := paused != nil && actor == employee.UserID
		if err := r.projects.lockEmployeeProject(tx, employee.ProjectID, actor, admin || selfPause); err != nil {
			return err
		}
		old, err := r.testEmployee(tx, employee.ProjectID, employee.UserID)
		if err != nil {
			return err
		}
		if err := r.projects.checkEmployeeMember(tx, employee.ProjectID, employee.UserID); err != nil {
			return err
		}
		switch {
		case paused != nil:
			employee = old
			employee.Paused = *paused
			if !employee.Paused {
				if err := r.bindEmployeeConnection(tx, &employee, nil, ""); err != nil {
					return err
				}
			}
		case employee.Revision != old.Revision:
			return ErrEmployeeConflict
		default:
			employee.Paused = old.Paused
			if err := r.bindEmployeeConnection(tx, &employee, connection, key); err != nil {
				return err
			}
		}
		if !employee.Paused && (employee.ConnectionID == "" || employee.Model == "") {
			return ErrEmployeeConnection
		}
		if old.ConnectionID != employee.ConnectionID {
			if err := r.deleteEmployeeConnection(tx, old.ProjectID, old.UserID, old.ConnectionID); err != nil {
				return err
			}
		}
		employee.Revision++
		return tx.Model(&testEmployeeRow{}).Where("project_id = ? AND user_id = ?", employee.ProjectID, employee.UserID).Update("payload", mustJSON(employee)).Error
	})
	return employee, err
}

func (r *TestEmployeesRepo) bindEmployeeConnection(tx *gorm.DB, employee *domain.TestEmployee, connection *domain.EmployeeModelConnection, key string) error {
	if connection != nil {
		connection.EmployeeID = employee.UserID
		if err := r.saveEmployeeConnection(tx, *connection, key); err != nil {
			return err
		}
		employee.ConnectionID = connection.ID
	}
	if employee.ConnectionID == "" || employee.Model == "" {
		return ErrEmployeeConnection
	}
	modelConnection, _, err := r.employeeConnection(tx, employee.ProjectID, employee.ConnectionID)
	if err != nil || (modelConnection.EmployeeID != 0 && modelConnection.EmployeeID != employee.UserID) || !containsModel(modelConnection.Models, employee.Model) {
		return ErrEmployeeConnection
	}
	return nil
}

func containsModel(models []string, wanted string) bool {
	for _, model := range models {
		if model == wanted {
			return true
		}
	}
	return false
}

func (r *TestEmployeesRepo) EmployeeConnections(projectID string) ([]domain.EmployeeModelConnection, error) {
	var rows []employeeConnectionRow
	if err := r.DB.Select("payload").Where("project_id = ?", projectID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]domain.EmployeeModelConnection, 0, len(rows))
	for _, row := range rows {
		var item domain.EmployeeModelConnection
		if err := json.Unmarshal(row.Payload, &item); err != nil {
			return nil, err
		}
		// Shared endpoints can contain account-specific paths.
		if item.Source != "" {
			item.BaseURL = ""
		}
		items = append(items, item)
	}
	return items, nil
}

func (r *TestEmployeesRepo) employeeConnection(tx *gorm.DB, projectID, id string) (domain.EmployeeModelConnection, []byte, error) {
	var item domain.EmployeeModelConnection
	var row employeeConnectionRow
	if err := tx.Where("project_id = ? AND id = ?", projectID, id).Take(&row).Error; err != nil {
		return item, nil, err
	}
	err := json.Unmarshal(row.Payload, &item)
	return item, row.Secret, err
}

func (r *TestEmployeesRepo) EmployeeConnection(projectID, id string) (domain.EmployeeModelConnection, string, error) {
	item, secret, err := r.employeeConnection(r.DB, projectID, id)
	if err != nil || item.Source != "" {
		return item, "", err
	}
	aead, err := employeeCredentialCipher()
	if err != nil {
		return item, "", err
	}
	plain, err := aead.Open(nil, nil, secret, []byte(projectID+"/"+id))
	return item, string(plain), err
}

func (r *TestEmployeesRepo) SaveEmployeeConnection(item domain.EmployeeModelConnection, admin bool) error {
	return r.DB.Transaction(func(tx *gorm.DB) error {
		if err := r.projects.lockEmployeeProject(tx, item.ProjectID, item.OwnerID, true); err != nil {
			return err
		}
		if !admin {
			member, err := r.projects.groupMember(tx, item.ProjectID, item.OwnerID)
			if err != nil {
				return err
			}
			if !member {
				return ErrEmployeeForbidden
			}
		}
		return r.saveEmployeeConnection(tx, item, "")
	})
}

func (r *TestEmployeesRepo) saveEmployeeConnection(tx *gorm.DB, item domain.EmployeeModelConnection, key string) error {
	row := employeeConnectionRow{ID: item.ID, ProjectID: item.ProjectID, Payload: mustJSON(item)}
	if item.Source == "" {
		aead, err := employeeCredentialCipher()
		if err != nil {
			return err
		}
		row.Secret = aead.Seal(nil, nil, []byte(key), []byte(item.ProjectID+"/"+item.ID))
	}
	return tx.Create(&row).Error
}

func employeeCredentialCipher() (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("AGENTFLOW_EMPLOYEE_CREDENTIAL_KEY")))
	if err != nil || len(key) != 32 {
		return nil, errors.New("请配置 AGENTFLOW_EMPLOYEE_CREDENTIAL_KEY（Base64 编码的 32 字节密钥）")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func (r *TestEmployeesRepo) RevokeEmployeeConnection(projectID, id string, actor int64, admin bool) error {
	return r.DB.Transaction(func(tx *gorm.DB) error {
		if err := r.projects.lockProjectInTx(tx, projectID); err != nil {
			return err
		}
		item, _, err := r.employeeConnection(tx, projectID, id)
		if err != nil {
			return err
		}
		if item.Source == "" || (!admin && item.OwnerID != actor) {
			return ErrEmployeeForbidden
		}
		return tx.Delete(&employeeConnectionRow{}, "project_id = ? AND id = ?", projectID, id).Error
	})
}

func (r *TestEmployeesRepo) deleteEmployeeConnection(tx *gorm.DB, projectID string, userID int64, id string) error {
	if id == "" {
		return nil
	}
	connection, _, err := r.employeeConnection(tx, projectID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil || connection.EmployeeID != userID {
		return err
	}
	return tx.Delete(&employeeConnectionRow{}, "id = ?", id).Error
}
