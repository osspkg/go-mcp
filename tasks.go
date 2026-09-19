/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD-3-Clause license that can be found in the LICENSE file.
 */

package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// TaskStatus is the lifecycle state of an experimental durable task.
type TaskStatus string

const (
	// TaskStatusWorking indicates that task execution is in progress.
	TaskStatusWorking TaskStatus = "working"
	// TaskStatusInput indicates that task execution is waiting for input.
	TaskStatusInput TaskStatus = "input_required"
	// TaskStatusCompleted indicates successful task execution.
	TaskStatusCompleted TaskStatus = "completed"
	// TaskStatusFailed indicates task execution failed.
	TaskStatusFailed TaskStatus = "failed"
	// TaskStatusCancelled indicates that task execution was cancelled.
	TaskStatusCancelled TaskStatus = "cancelled"
)

var (
	// ErrTaskNotFound indicates an unknown or expired task identifier.
	ErrTaskNotFound = errors.New("mcp: task not found")
	// ErrTaskNotReady indicates that a task has no result yet.
	ErrTaskNotReady = errors.New("mcp: task result is not ready")
	// ErrTaskLimit indicates that the bounded task store cannot accept another
	// running task until an existing task finishes or expires.
	ErrTaskLimit = errors.New("mcp: task limit reached")
)

// TaskMetadata requests task-augmented execution. TTL is expressed in
// milliseconds on the wire, as required by the MCP task schema.
type TaskMetadata struct {
	TTL int64 `json:"ttl,omitempty"`
}

// Task is the externally visible task state.
type Task struct {
	TaskID        string     `json:"taskId"`
	Status        TaskStatus `json:"status"`
	StatusMessage string     `json:"statusMessage,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	LastUpdatedAt time.Time  `json:"lastUpdatedAt"`
	PollInterval  int64      `json:"pollInterval,omitempty"`
	TTL           int64      `json:"ttl"`
}

type taskRecord struct {
	mu     sync.Mutex
	task   Task
	result any
	err    error
	cancel context.CancelFunc
	done   chan struct{}
	notify func(Task)
}

const defaultTaskTTL = time.Hour

const taskIDBytes = 16

const defaultTaskPollInterval = 1000

const maxTaskRecords = 1024

const maxTaskTTLMillis = (1<<63 - 1) / int64(time.Millisecond)

// StartTask starts work asynchronously and returns its durable task handle.
// The task retains its result for the configured default retention period.
func (server *Server) StartTask(ctx context.Context, work func(context.Context) (any, error)) (Task, error) {
	return server.startTask(ctx, 0, work, false)
}

func (server *Server) startTask(ctx context.Context, ttl time.Duration, work func(context.Context) (any, error), detached bool) (Task, error) { //nolint:contextcheck // derives a child task context
	if server == nil {
		return Task{}, errors.New("mcp: nil server")
	}
	if work == nil {
		return Task{}, errors.New("mcp: nil task function")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ttl <= 0 {
		ttl = defaultTaskTTL
	}
	raw := make([]byte, taskIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return Task{}, fmt.Errorf("mcp: generate task ID: %w", err)
	}
	id := hex.EncodeToString(raw)
	now := time.Now().UTC()
	if detached {
		// Durable work must outlive the JSON-RPC request that created it; explicit
		// tasks/cancel or TTL cleanup remain the cancellation boundaries.
		ctx = context.WithoutCancel(ctx)
	}
	taskCtx, cancel := context.WithCancel(ctx)
	var notify func(Task)
	if request, ok := RequestFromContext(ctx); ok && request.Meta.SessionID != "" {
		sessionID := request.Meta.SessionID
		notify = func(task Task) {
			_ = server.NotifyTaskStatus(context.WithoutCancel(taskCtx), sessionID, task)
		}
	}
	record := &taskRecord{task: Task{TaskID: id, Status: TaskStatusWorking, CreatedAt: now, LastUpdatedAt: now, PollInterval: defaultTaskPollInterval, TTL: ttl.Milliseconds()}, cancel: cancel, done: make(chan struct{}), notify: notify}
	if err := server.storeTask(id, record); err != nil {
		cancel()
		return Task{}, err
	}
	go func() {
		var (
			result any
			err    error
		)
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("mcp: task panic: %v", recovered)
				}
			}()
			result, err = work(taskCtx)
		}()
		record.mu.Lock()
		record.result = result
		record.err = err
		record.task.LastUpdatedAt = time.Now().UTC()
		switch {
		case errors.Is(taskCtx.Err(), context.Canceled):
			record.task.Status = TaskStatusCancelled
			record.task.StatusMessage = "task cancelled"
		case err != nil:
			record.task.Status = TaskStatusFailed
			record.task.StatusMessage = err.Error()
		default:
			record.task.Status = TaskStatusCompleted
		}
		record.mu.Unlock()
		if record.notify != nil {
			record.notify(record.snapshot())
		}
		cancel()
		close(record.done)
	}()
	return record.snapshot(), nil
}

func (server *Server) storeTask(id string, record *taskRecord) error {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.cleanupTasksLocked(time.Now())
	if len(server.tasks) >= maxTaskRecords {
		var oldestID string
		var oldest time.Time
		for taskID, item := range server.tasks {
			item.mu.Lock()
			finished := item.task.Status != TaskStatusWorking && item.task.Status != TaskStatusInput
			created := item.task.CreatedAt
			item.mu.Unlock()
			if finished && (oldestID == "" || created.Before(oldest)) {
				oldestID, oldest = taskID, created
			}
		}
		if oldestID == "" {
			return ErrTaskLimit
		}
		delete(server.tasks, oldestID)
	}
	server.tasks[id] = record
	return nil
}

func (record *taskRecord) snapshot() Task {
	record.mu.Lock()
	defer record.mu.Unlock()
	return record.task
}

func (server *Server) task(id string) (*taskRecord, error) {
	if id == "" {
		return nil, ErrTaskNotFound
	}
	server.mu.Lock()
	server.cleanupTasksLocked(time.Now())
	record := server.tasks[id]
	server.mu.Unlock()
	if record == nil {
		return nil, ErrTaskNotFound
	}
	return record, nil
}

// GetTask returns the current state of a task.
func (server *Server) GetTask(_ context.Context, id string) (Task, error) {
	record, err := server.task(id)
	if err != nil {
		return Task{}, err
	}
	return record.snapshot(), nil
}

// GetTaskResult waits for completion or context cancellation and returns the
// original JSON-compatible result.
func (server *Server) GetTaskResult(ctx context.Context, id string) (any, error) { //nolint:contextcheck // waits on the caller context
	if ctx == nil {
		ctx = context.Background() //nolint:contextcheck // a nil caller context has no parent
	}
	record, err := server.task(id)
	if err != nil {
		return nil, err
	}
	select {
	case <-record.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.err != nil {
		return nil, record.err
	}
	return record.result, nil
}

// CancelTask requests cancellation and returns the resulting task state.
func (server *Server) CancelTask(_ context.Context, id string) (Task, error) {
	record, err := server.task(id)
	if err != nil {
		return Task{}, err
	}
	record.mu.Lock()
	if record.task.Status == TaskStatusWorking || record.task.Status == TaskStatusInput {
		record.task.Status = TaskStatusCancelled
		record.task.StatusMessage = "task cancellation requested"
		record.task.LastUpdatedAt = time.Now().UTC()
	}
	record.mu.Unlock()
	record.cancel()
	return record.snapshot(), nil
}

func (server *Server) cleanupTasksLocked(now time.Time) {
	for id, record := range server.tasks {
		record.mu.Lock()
		expires := record.task.CreatedAt.Add(time.Duration(record.task.TTL) * time.Millisecond)
		finished := record.task.Status != TaskStatusWorking && record.task.Status != TaskStatusInput
		record.mu.Unlock()
		if now.After(expires) {
			if !finished {
				record.cancel()
			}
			delete(server.tasks, id)
		}
	}
}

func (server *Server) taskResult(id string) (any, error) {
	record, err := server.task(id)
	if err != nil {
		return nil, err
	}
	select {
	case <-record.done:
	default:
		return nil, ErrTaskNotReady
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.err != nil {
		return nil, record.err
	}
	return record.result, nil
}
