package eval

import (
	"fmt"
	"time"
)

// RunRecord 一次评测任务的数据库记录
type RunRecord struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	Name       string `json:"name"`
	Config     string `json:"config"`
	Status     string `json:"status"` // pending | running | done | error
	Report     string `json:"report,omitempty"`
	Detail     string `json:"-"`
	ErrorMsg   string `json:"error_msg,omitempty"`
	CreatedAt  string `json:"created_at"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// CreateRun 创建一条评测任务记录
func (r *Repo) CreateRun(projectID, name, config string) (*RunRecord, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	id := fmt.Sprintf("run-%s-%s", projectID[:min(10, len(projectID))], now[0:19])
	// 清理 ID 中的特殊字符
	id = cleanID(id)

	_, err := r.db.Exec(
		`INSERT INTO eval_runs (id, project_id, name, config, status, created_at) VALUES (?, ?, ?, ?, 'pending', ?)`,
		id, projectID, name, config, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}
	return r.GetRun(id)
}

// ListRuns 列出项目的所有评测任务
func (r *Repo) ListRuns(projectID string) ([]RunRecord, error) {
	rows, err := r.db.Query(
		`SELECT id, project_id, name, config, status, COALESCE(report,''), COALESCE(error_msg,''),
		        created_at, COALESCE(started_at,''), COALESCE(finished_at,'')
		 FROM eval_runs WHERE project_id=? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()

	var out []RunRecord
	for rows.Next() {
		var rr RunRecord
		if err := rows.Scan(&rr.ID, &rr.ProjectID, &rr.Name, &rr.Config, &rr.Status,
			&rr.Report, &rr.ErrorMsg, &rr.CreatedAt, &rr.StartedAt, &rr.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, rr)
	}
	return out, rows.Err()
}

// GetRun 获取单条评测任务记录
func (r *Repo) GetRun(runID string) (*RunRecord, error) {
	rr := &RunRecord{}
	err := r.db.QueryRow(
		`SELECT id, project_id, name, config, status, COALESCE(report,''), COALESCE(detail,''),
		        COALESCE(error_msg,''), created_at, COALESCE(started_at,''), COALESCE(finished_at,'')
		 FROM eval_runs WHERE id=?`, runID,
	).Scan(&rr.ID, &rr.ProjectID, &rr.Name, &rr.Config, &rr.Status,
		&rr.Report, &rr.Detail, &rr.ErrorMsg, &rr.CreatedAt, &rr.StartedAt, &rr.FinishedAt)
	if err != nil {
		return nil, fmt.Errorf("get run %s: %w", runID, err)
	}
	return rr, nil
}

// UpdateRunStatus 更新任务状态
func (r *Repo) UpdateRunStatus(runID, status, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	var err error
	switch status {
	case "running":
		_, err = r.db.Exec(`UPDATE eval_runs SET status='running', started_at=? WHERE id=?`, now, runID)
	case "done":
		_, err = r.db.Exec(`UPDATE eval_runs SET status='done', finished_at=? WHERE id=?`, now, runID)
	case "error":
		_, err = r.db.Exec(`UPDATE eval_runs SET status='error', error_msg=?, finished_at=? WHERE id=?`, errMsg, now, runID)
	}
	if err != nil {
		return fmt.Errorf("update run status: %w", err)
	}
	return nil
}

// SaveRunResult 保存评测报告和详情
func (r *Repo) SaveRunResult(runID, report, detail string) error {
	_, err := r.db.Exec(`UPDATE eval_runs SET report=?, detail=?, status='done', finished_at=? WHERE id=?`,
		report, detail, time.Now().UTC().Format(time.RFC3339), runID)
	if err != nil {
		return fmt.Errorf("save run result: %w", err)
	}
	return nil
}

// DeleteRun 删除评测任务
func (r *Repo) DeleteRun(runID string) error {
	_, err := r.db.Exec(`DELETE FROM eval_runs WHERE id=?`, runID)
	if err != nil {
		return fmt.Errorf("delete run: %w", err)
	}
	return nil
}

func cleanID(s string) string {
	result := make([]byte, 0, len(s))
	for _, b := range []byte(s) {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_' {
			result = append(result, b)
		}
	}
	return string(result)
}
