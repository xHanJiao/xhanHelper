ALTER TABLE delayed_tasks ADD INDEX idx_delayed_tasks_type_status_at (task_type, status, process_at);
ALTER TABLE commitments ADD INDEX idx_commitments_status_deadline (status, next_deadline);
