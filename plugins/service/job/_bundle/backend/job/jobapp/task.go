package jobapp

import "context"

type TaskID string

type TaskRequest struct {
	RunID   string
	Payload []byte
}

type TaskResult struct {
	Output []byte
}

type Task interface {
	ID() TaskID
	Run(context.Context, TaskRequest) (TaskResult, error)
}
