-- LoopForge 0.4.2 project collaboration upgrade. Apply before the new image.
-- Existing user, member, room and message rows keep their IDs and contents.
CREATE TABLE IF NOT EXISTS project_test_employees (
  project_id VARCHAR(64) NOT NULL,
  user_id BIGINT NOT NULL,
  payload JSON NOT NULL,
  PRIMARY KEY (project_id, user_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS employee_model_connections (
  id VARCHAR(64) NOT NULL,
  project_id VARCHAR(64) NOT NULL,
  payload JSON NOT NULL,
  secret BLOB NULL,
  PRIMARY KEY (id),
  KEY idx_employee_model_connections_project_id (project_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS room_mentions (
  message_id VARCHAR(64) NOT NULL,
  user_id BIGINT NOT NULL,
  room_id VARCHAR(64) NOT NULL,
  created_at DATETIME(3) NULL,
  read_at DATETIME(3) NULL,
  PRIMARY KEY (message_id, user_id),
  KEY idx_room_mentions_inbox (user_id, created_at),
  KEY idx_room_mentions_room_id (room_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS employee_room_discussions (
  room_id VARCHAR(64) NOT NULL PRIMARY KEY,
  enabled BOOLEAN,
  reply_limit BIGINT,
  round_id VARCHAR(64),
  replies BIGINT,
  evaluations BIGINT,
  lease_until DATETIME(3)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS employee_discussion_jobs (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  project_id VARCHAR(64),
  room_id VARCHAR(64),
  round_id VARCHAR(64),
  trigger_id VARCHAR(64),
  user_id BIGINT,
  employee_revision BIGINT,
  mentioned BOOLEAN,
  priority BIGINT,
  status VARCHAR(16),
  attempts BIGINT,
  lease_until DATETIME(3),
  available_at DATETIME(3),
  model VARCHAR(250),
  error_code VARCHAR(64),
  created_at DATETIME(3),
  updated_at DATETIME(3),
  UNIQUE KEY uq_employee_discussion_trigger (trigger_id, user_id),
  KEY idx_employee_discussion_jobs_project_id (project_id),
  KEY idx_employee_discussion_jobs_room_id (room_id),
  KEY idx_employee_discussion_pending (status, available_at)
) ENGINE=InnoDB;
