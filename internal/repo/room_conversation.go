package repo

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"agentflow/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrRoomScope           = errors.New("找不到项目协作群或群消息")
	ErrRoomMention         = errors.New("提及对象已变化，请重新选择项目成员")
	ErrRoomMessageConflict = errors.New("同一客户端消息标识已用于不同的消息")
)

type roomMentionRow struct {
	MessageID string    `gorm:"primaryKey;size:64"`
	UserID    int64     `gorm:"primaryKey;autoIncrement:false;index:idx_room_mentions_inbox"`
	RoomID    string    `gorm:"size:64;not null;index"`
	CreatedAt time.Time `gorm:"index:idx_room_mentions_inbox,priority:2"`
	ReadAt    *time.Time
}

func (roomMentionRow) TableName() string { return "room_mentions" }

func (r *RoomsRepo) GroupRooms(projectID string) ([]domain.ChatRoom, error) {
	var rows []chatRoomRow
	err := r.DB.Where("project_id = ?", projectID).Where("NOT EXISTS (SELECT 1 FROM requirement_spaces WHERE requirement_spaces.id = chat_rooms.id)").Order("updated_at DESC, id DESC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	items := make([]domain.ChatRoom, 0, len(rows))
	for _, row := range rows {
		var item domain.ChatRoom
		if err := json.Unmarshal(row.Payload, &item); err != nil {
			return nil, err
		}
		if row.Updated != nil {
			item.UpdatedAt = *row.Updated
		}
		if item.Kind != domain.RoomKindRequirement {
			items = append(items, item)
		}
	}
	return items, nil
}

func (r *RoomsRepo) GroupRoom(id string) (domain.ChatRoom, error) {
	return r.groupRoom(r.DB, id, false)
}

func (r *RoomsRepo) groupRoom(tx *gorm.DB, id string, lock bool) (domain.ChatRoom, error) {
	var row chatRoomRow
	query := tx.Where("id = ?", id).Where("NOT EXISTS (SELECT 1 FROM requirement_spaces WHERE requirement_spaces.id = chat_rooms.id)")
	if lock {
		query = r.locking(query)
	}
	if err := query.Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.ChatRoom{}, ErrRoomScope
		}
		return domain.ChatRoom{}, err
	}
	var room domain.ChatRoom
	if err := json.Unmarshal(row.Payload, &room); err != nil {
		return room, err
	}
	if room.Kind == domain.RoomKindRequirement {
		return room, ErrRoomScope
	}
	if row.Updated != nil {
		room.UpdatedAt = *row.Updated
	}
	return room, nil
}

func (r *RoomsRepo) RoomMessage(roomID, messageID string) (domain.ChatMessage, error) {
	var row chatMessageRow
	if err := r.DB.Where("room_id = ? AND id = ?", roomID, messageID).Where("(thread_id = '' OR thread_id IS NULL)").Take(&row).Error; err != nil {
		return domain.ChatMessage{}, err
	}
	item, ok := decodeChatMessage(row)
	if !ok {
		return item, ErrRoomScope
	}
	return item, nil
}

func (r *RoomsRepo) RoomMessagePage(roomID, before string, limit int) ([]domain.ChatMessage, bool, error) {
	query := r.DB.Where("room_id = ?", roomID).Where("(thread_id = '' OR thread_id IS NULL)")
	if before != "" {
		var pivot chatMessageRow
		if err := query.Session(&gorm.Session{}).Where("id = ?", before).Take(&pivot).Error; err != nil {
			return nil, false, err
		}
		query = query.Where("(created_at < ? OR (created_at = ? AND id < ?))", pivot.Created, pivot.Created, pivot.ID)
	}
	var rows []chatMessageRow
	if err := query.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return decodeChatMessagesReversed(rows), hasMore, nil
}

func manualRoomMessageID(roomID string, userID int64, clientID string) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%s", roomID, userID, clientID)))
	return fmt.Sprintf("msg-%x", hash[:24])
}

// addTextMessage writes the ordinary message and its mention inbox entries atomically.
func (r *RoomsRepo) addTextMessage(tx *gorm.DB, msg domain.ChatMessage, clientID string) (domain.ChatMessage, bool, error) {
	if clientID != "" {
		msg.ID = manualRoomMessageID(msg.RoomID, msg.UserID, clientID)
		var existing chatMessageRow
		err := tx.Where("id = ?", msg.ID).Take(&existing).Error
		if err == nil {
			stored, ok := decodeChatMessage(existing)
			if !ok || stored.Body != msg.Body || string(mustJSON(stored.Mentions)) != string(mustJSON(msg.Mentions)) {
				return msg, false, ErrRoomMessageConflict
			}
			return stored, false, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return msg, false, err
		}
	}
	scoped := &RoomsRepo{deps: &deps{DB: tx}}
	if err := scoped.PutMessage(msg); err != nil {
		return msg, false, err
	}
	seen := map[int64]bool{}
	for _, mention := range msg.Mentions {
		if seen[mention.UserID] || mention.UserID == msg.UserID {
			continue
		}
		seen[mention.UserID] = true
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&roomMentionRow{MessageID: msg.ID, UserID: mention.UserID, RoomID: msg.RoomID, CreatedAt: msg.CreatedAt}).Error; err != nil {
			return msg, false, err
		}
	}
	if err := tx.Model(&chatRoomRow{}).Where("id = ?", msg.RoomID).Update("updated_at", msg.CreatedAt).Error; err != nil {
		return msg, false, err
	}
	return msg, true, nil
}

func (r *RoomsRepo) RoomMentions(userID int64, projectID string) ([]domain.RoomMentionNotice, int64, error) {
	base := r.DB.Table("room_mentions AS m").Where("m.user_id = ?", userID).
		Joins("JOIN chat_rooms AS room ON room.id = m.room_id").
		Joins("JOIN projects AS p ON p.id = room.project_id").
		Where("room.project_id = ?", projectID).
		Where("(p.owner_user_id = ? OR EXISTS (SELECT 1 FROM project_members AS member WHERE member.project_id = p.id AND member.user_id = ?))", userID, userID).
		Where("NOT EXISTS (SELECT 1 FROM requirement_spaces AS space WHERE space.id = room.id)")
	var unread int64
	if err := base.Session(&gorm.Session{}).Where("m.read_at IS NULL").Count(&unread).Error; err != nil {
		return nil, 0, err
	}
	var rows []struct {
		RoomID, RoomName string
		MessageID        string
		ReadAt           *time.Time
		Payload          []byte
	}
	err := base.Select("m.room_id, room.name AS room_name, m.message_id, m.read_at, msg.payload").
		Joins("JOIN chat_messages AS msg ON msg.id = m.message_id AND msg.room_id = room.id").
		Order("CASE WHEN m.read_at IS NULL THEN 0 ELSE 1 END, m.created_at DESC, m.message_id DESC").Limit(100).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	result := make([]domain.RoomMentionNotice, 0, len(rows))
	for _, row := range rows {
		var msg domain.ChatMessage
		if err := json.Unmarshal(row.Payload, &msg); err != nil {
			return nil, 0, err
		}
		result = append(result, domain.RoomMentionNotice{RoomID: row.RoomID, RoomName: row.RoomName, Message: msg, Read: row.ReadAt != nil})
	}
	return result, unread, nil
}

func (r *RoomsRepo) ReadRoomMention(userID int64, roomID, messageID string) error {
	return r.DB.Model(&roomMentionRow{}).Where("user_id = ? AND room_id = ? AND message_id = ? AND read_at IS NULL", userID, roomID, messageID).
		Update("read_at", time.Now().UTC()).Error
}
