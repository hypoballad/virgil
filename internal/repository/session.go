package repository

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/hypoballad/virgil/internal/db"
)

type Session struct {
	ID              string
	StartedAt       int64
	EndedAt         sql.NullInt64
	ProjectPath     string
	TaskDescription string
	Status          string
	Model           string
	Metadata        sql.NullString
}

type SessionRepository struct {
	db *db.DB
}

func NewSessionRepository(database *db.DB) *SessionRepository {
	return &SessionRepository{db: database}
}

func (r *SessionRepository) Create(model, projectPath, taskDescription string) (*Session, error) {
	s := &Session{
		ID:              uuid.New().String(),
		StartedAt:       time.Now().Unix(),
		Status:          "running",
		Model:           model,
		ProjectPath:     projectPath,
		TaskDescription: taskDescription,
	}

	_, err := r.db.SqlDB.Exec(`
		INSERT INTO sessions (id, started_at, project_path, task_description, status, model)
		VALUES (?, ?, ?, ?, ?, ?)`,
		s.ID, s.StartedAt, s.ProjectPath, s.TaskDescription, s.Status, s.Model)
	if err != nil {
		return nil, err
	}

	return s, nil
}

func (r *SessionRepository) End(id string, status string) error {
	_, err := r.db.SqlDB.Exec(`
		UPDATE sessions SET ended_at = ?, status = ? WHERE id = ?`,
		time.Now().Unix(), status, id)
	return err
}

func (r *SessionRepository) Get(id string) (*Session, error) {
	s := &Session{}
	err := r.db.SqlDB.QueryRow(`
		SELECT id, started_at, ended_at, project_path, task_description, status, model, metadata
		FROM sessions WHERE id = ?`, id).Scan(
		&s.ID, &s.StartedAt, &s.EndedAt, &s.ProjectPath, &s.TaskDescription, &s.Status, &s.Model, &s.Metadata)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (r *SessionRepository) ListRecent(limit int) ([]*Session, error) {
	rows, err := r.db.SqlDB.Query(`
		SELECT id, started_at, ended_at, project_path, task_description, status, model, metadata
		FROM sessions ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*Session
	for rows.Next() {
		s := &Session{}
		if err := rows.Scan(&s.ID, &s.StartedAt, &s.EndedAt, &s.ProjectPath, &s.TaskDescription, &s.Status, &s.Model, &s.Metadata); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}
